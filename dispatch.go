package via

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"

	"github.com/go-via/via/internal/hcore"
)

// actionMode is decided by the request, not the route: the bundled Datastar
// client sends Datastar-Request: true on every @post; a native <form> submit
// never does, so it always gets a full-page re-render instead of an
// element-patch.
type actionMode int

const (
	modeDatastar actionMode = iota
	modeNative
)

// tabFormField is the hidden field name via.PostForm renders inside a live
// unit, carrying the tab id as a fallback for a native <form> submit: a real
// browser POST cannot set the X-Via-Tab header (that's fetch-only), so the
// tab id — the CSRF token in this project's threat model — rides in the body
// instead. It is subject to the exact same per-mount ownership check as the
// header (see dispatch): accepting it from the body doesn't weaken that
// check, it only changes where the id is read from.
const tabFormField = "_viatab"

// actionResult is what running a positional action produced. panicked is set
// only on the live path: it happens on the connection's own goroutine, so it
// must be carried back across the channel to the POST that triggered it
// rather than answered where it occurred. body carries a native-form live
// dispatch's whole-page re-render — it must be produced on the same
// goroutine as the mutation (see dispatchLive) because it reads the live
// tree the island goroutine concurrently ticks.
type actionResult struct {
	redirect string
	panicked bool
	body     []byte
	pushWork func() // live path only: the dirty-signals + element push, run by liveConn.run right after acking (see liveRunAction)
}

// mount bundles a page's per-request wiring — built once in Router.Mount and
// shared by its GET, action, and SSE routes so origin floor, guards, OnInit,
// and body caps can't drift between them.
type mount struct {
	cfg         *config
	sessions    *sessionManager
	reg         *registry
	newInst     func() viewer
	guards      []Guard
	patternBase string
	names       []string
	liveCount   *atomic.Int64 // concurrent SSE streams across the whole router, capped at maxLive
	maxLive     int
}

// unit returns the bind pass's unit Ctx for island id: 0 is the root itself;
// k>0 is its k-1'th embedded child. nil when there is no such unit.
func (bind *Ctx) unit(island int) *Ctx {
	if island == 0 {
		return bind
	}
	k := island - 1
	if k < 0 || k >= len(bind.islands) {
		return nil
	}
	return bind.islands[k]
}

// unit returns the connected live unit for island id (0 = root), at any
// embedding depth, nil when this connection has none. Called from the
// dispatching request's own goroutine while replace runs on the island
// goroutine, so it takes the same lock.
func (c *liveConn) unit(island int) *Ctx {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.units[island]
}

// unitAddr is c's own dispatch address in /_via/a/{island}/{n} — 0 for the
// root, islandIdx+1 for an embedded unit at any depth.
func unitAddr(c *Ctx) int {
	if c != nil && c.isIsland {
		return c.islandIdx + 1
	}
	return 0
}

// dispatch is the single entry point for every action POST on a mount — a
// Datastar @post, a native PostForm submit, or a live unit's action — at
// {base}/_via/a/{island}/{n} (the root is island 0). One origin floor, one
// guard run, one OnInit, one body decode: the six transports this replaced
// each re-implemented these and drifted (guards and OnInit never ran on an
// island action; a live/island action silently dropped ctx.Redirect).
func (m *mount) dispatch(w http.ResponseWriter, req *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			recoverToHTTP(w, rec, "action")
		}
	}()
	if !originAllowed(req, m.cfg) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if runGuards(w, req, m.sessions, m.guards) {
		return
	}
	mode := modeNative
	if req.Header.Get("Datastar-Request") == "true" {
		mode = modeDatastar
	}
	in, ok := decodeInput(w, req, mode)
	if !ok {
		return
	}
	if mode == modeNative {
		defer req.MultipartForm.RemoveAll() // drop any spilled temp files
	}
	island, err := strconv.Atoi(req.PathValue("island"))
	if err != nil {
		http.Error(w, "no such island", http.StatusGone)
		return
	}
	n, err := strconv.Atoi(req.PathValue("n"))
	if err != nil {
		http.Error(w, "no such action", http.StatusGone)
		return
	}
	digest := req.URL.Query().Get("v")
	base := concreteBase(m.patternBase, req, m.names)

	tab := req.Header.Get("X-Via-Tab")
	if tab == "" && mode == modeNative {
		// Only a native form submit falls back to the body: a Datastar @post
		// always carries the header, so this never masks a missing header on
		// the fetch path.
		tab = req.PostFormValue(tabFormField)
	}
	if lc, ok := m.reg.get(tab); ok {
		if lc.mount != m {
			// The registry is router-wide (one registry, every mount); a tab id
			// from another mount is otherwise structurally valid here — same
			// header/field, same shape digest space — so this must be checked
			// explicitly rather than relying on anything else to fail first.
			http.Error(w, "no such island", http.StatusGone)
			return
		}
		m.dispatchLive(w, req, mode, lc, island, n, in, digest, base)
		return
	}
	m.dispatchStateless(w, req, mode, island, n, in, base, digest)
}

// decodeInput decodes an action POST's body per mode: a native PostForm
// submit is multipart (files are the payload, so the cap rises to
// maxUploadBytes; only maxActionBody of it is kept in RAM, the rest spills to
// a temp file the caller removes when the handler returns); a Datastar @post
// carries a capped JSON signals object.
func decodeInput(w http.ResponseWriter, req *http.Request, mode actionMode) (map[string]json.RawMessage, bool) {
	if mode == modeNative {
		req.Body = http.MaxBytesReader(w, req.Body, maxUploadBytes)
		if err := req.ParseMultipartForm(maxActionBody); err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return nil, false
			}
			http.Error(w, "malformed form", http.StatusBadRequest)
			return nil, false
		}
		return nil, true
	}
	return decodeActionBody(w, req)
}

// dispatchLive runs action n against a connected live unit on its
// connection's serialized goroutine and WAITS for the mutation's result —
// synchronous, unlike the old fire-and-forget island dispatch, so a
// Redirect, the session cookie, and a panic all resolve on THIS response
// exactly like a stateless action. The wait is bounded by req.Context() as
// well as the connection closing, so a stalled peer elsewhere on the stream
// can't park this POST's goroutine forever (see liveConn.run).
func (m *mount) dispatchLive(w http.ResponseWriter, req *http.Request, mode actionMode, lc *liveConn, island, n int, in map[string]json.RawMessage, digest, base string) {
	u := lc.unit(island)
	if u == nil {
		http.Error(w, "no such island", http.StatusGone)
		return
	}
	if u.shapeDigest() != digest {
		http.Error(w, "stale page", http.StatusGone)
		return
	}
	if n < 0 || n >= len(u.actions) {
		http.Error(w, "no such action", http.StatusGone)
		return
	}
	res, ok := lc.run(req.Context(), func() actionResult {
		result := liveRunAction(w, req, m.sessions, lc, u, in, n)
		if mode == modeNative && !result.panicked {
			// A native <form> submit is a real navigation: the browser replaces
			// the whole document, so it needs a full page, not the element-patch
			// the (still open, about-to-be-abandoned) SSE stream carries
			// separately. lc.pageRoot is the connection's actual top-level
			// instance — rendering through lc lets an already-connected live
			// descendant reuse its own state instead of a fresh by-value copy
			// (see embedViewer), exactly like a real push does. It must run
			// here, on the island's own serialized goroutine, not back on the
			// POST's — this render reads/mutates the same live tree a
			// concurrent tick or push does.
			_, body := renderRootBase(lc.pageRoot, nil, true, base, nil, lc)
			result.body = body
		}
		return result
	})
	if !ok {
		http.Error(w, "live connection closed", http.StatusGone)
		return
	}
	if res.panicked {
		http.Error(w, "action failed", http.StatusInternalServerError)
		return
	}
	if mode == modeNative {
		respond(w, req, mode, res.redirect, func() {
			writeHTMLPage(w, m.cfg, res.body, true, base+"/_via/sse")
		}, nil)
		return
	}
	respond(w, req, mode, res.redirect, nil, nil) // patch: nil — the push already framed it
}

// liveRunAction hydrates unit's signals from in, runs action n, and returns
// as soon as the mutation is known. unit is the bind Ctx the last push
// produced (or, before any push, the connect render) — its actions/hydrators
// table is current because every push is a render, so no render happens here
// before the action runs. It runs on the connection's serialized goroutine
// while the triggering POST blocks in liveConn.run — so w is safe to write to
// here (the session cookie, a queued Redirect) exactly as a stateless action
// would be. dispatchLive already range-checked n against unit.actions before
// calling in here — the shape digest alone does not do this: it fixes the
// action COUNT, not which index was requested, so a forged n within a
// stale/foreign range could still slip past it. The recover below is a
// last-resort backstop, not the primary guard.
//
// The re-render + SSE push (the dirty-signals patch, then the element patch)
// rides back in res.pushWork instead of running here: liveConn.run sends the
// mutation's result to the waiting POST FIRST, then runs pushWork — so a
// stalled peer's write (up to the write timeout) delays only the NEXT
// pulse item, never this response, while still running on the connection's
// one serialized goroutine in the same order the actions themselves ran. A
// detached goroutine doing this enqueue used to race other such goroutines
// from concurrent actions, reordering their pushes; returning it as data
// instead keeps everything on the one goroutine, in order.
func liveRunAction(w http.ResponseWriter, req *http.Request, sessions *sessionManager, lc *liveConn, unit *Ctx, in map[string]json.RawMessage, n int) (res actionResult) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("via: live action panic: %v\n%s", rec, debug.Stack())
			res = actionResult{panicked: true}
		}
	}()
	for slot, raw := range in {
		if hydrate, ok := unit.hydrators[slot]; ok {
			hydrate(raw)
		}
	}
	unit.req = req
	unit.sessions = sessions
	unit.sessW = w
	unit.redirect = ""
	unit.dirty = map[string]any{}
	unit.actions[n]()

	// A deliberate server-driven signal change (e.g. clearing the composer)
	// reaches the client as a signal-patch — the element push omits
	// data-signals, so a morph never clobbers what the user is typing.
	dirty, push := unit.dirty, unit.push
	return actionResult{
		redirect: unit.redirect,
		pushWork: func() {
			if len(dirty) > 0 {
				if raw, err := json.Marshal(dirty); err == nil {
					lc.pushSignals(string(raw))
				}
			}
			push() // re-render this unit and frame the element-patch
		},
	}
}

// dispatchStateless is dispatch's non-live path: bind a fresh instance, run
// OnInit, run the acted-on unit's action, then answer per mode. digest is
// checked against the freshly-bound unit's own shapeDigest, which (unlike an
// order-only, root-signals-only check) also catches a branched View shifting
// a STATELESS ISLAND's action indices.
func (m *mount) dispatchStateless(w http.ResponseWriter, req *http.Request, mode actionMode, island, n int, in map[string]json.RawMessage, base, digest string) {
	inst := m.newInst()
	if runOnInit(inst, w, req, m.sessions) != nil {
		return
	}
	bind, rootBefore := renderRootPatch(inst, in, base, nil, nil)
	bind.islandV = inst // so bind.unit(0)'s liveness reads the same way an embedded island's does
	u := bind.unit(island)
	if u == nil {
		http.Error(w, "no such island", http.StatusGone)
		return
	}
	if u.shapeDigest() != digest {
		http.Error(w, "stale page", http.StatusGone)
		return
	}
	if n < 0 || n >= len(u.actions) {
		http.Error(w, "no such action", http.StatusGone)
		return
	}
	if _, live := u.islandV.(Live); live {
		// A live unit's action only routes through the tab handshake in
		// dispatch; reaching here means the tab was missing/stale, so fail
		// closed rather than mutating a throwaway instance.
		http.Error(w, "no live connection for this tab", http.StatusGone)
		return
	}
	u.req = req
	u.sessions = m.sessions
	u.sessW = w
	u.actions[n]()

	if mode == modeNative {
		respond(w, req, mode, u.redirect, func() {
			_, body := renderRootBase(inst, nil, true, base, nil, nil)
			writeHTMLPage(w, m.cfg, body, false, "")
		}, nil)
		return
	}
	respond(w, req, mode, u.redirect, nil, func() []byte {
		return m.rerenderStateless(island, rootBefore, inst, bind, u, base)
	})
}

// rerenderStateless re-renders the acted-on unit for a Datastar action's
// response: the root re-render restricted to the signals this action wrote
// page-wide, or a stateless island's own container WITH a data-signals
// attribute restricted to the island's own dirty slots — the piece the old
// island-action handler omitted entirely, silently dropping a Signal.Set
// inside a stateless island's action. Returns nil when unchanged (→ 204).
func (m *mount) rerenderStateless(island int, rootBefore []byte, inst viewer, bind, u *Ctx, base string) []byte {
	if island == 0 {
		_, after := renderRootPatch(inst, nil, base, bind.dirtyAll(), nil)
		if bytes.Equal(rootBefore, after) {
			return nil
		}
		return after
	}
	afterCtx, afterInner := renderIslandBind(u.islandIdx, u.islandV, base)
	if bytes.Equal(u.rendered, afterInner) && len(u.dirty) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteString(`<div id="via-i` + strconv.Itoa(u.islandIdx) + `"`)
	writeSignalsAttr(&buf, afterCtx.order, afterCtx.initial, u.dirty)
	buf.WriteString(`>`)
	buf.Write(afterInner)
	buf.WriteString(`</div>`)
	return buf.Bytes()
}

// respond is dispatch's one response policy for every action POST — root,
// island, live, or native form. A queued Redirect wins on a native form
// submit (a 303, admitted only when hcore.SafeURL clears the target — SafeURL
// also gates OnInit's redirect in runOnInit, and rendered link/script/href/src
// URLs in head.go and h/url.go — one policy, four call sites). A Redirect from
// a Datastar @post action cannot navigate the page; it is logged and dropped.
// Otherwise renderNative (a full-page re-render, native-form-only) or
// renderPatch (an element-patch, or nil for "unchanged"/"the live push
// already carried it") decides the body.
func respond(w http.ResponseWriter, req *http.Request, mode actionMode, redirect string, renderNative func(), renderPatch func() []byte) {
	if redirect != "" {
		switch {
		case !hcore.SafeURL(redirect):
			log.Printf("via: unsafe Redirect target %q dropped", redirect)
		case mode == modeNative:
			http.Redirect(w, req, redirect, http.StatusSeeOther)
			return
		default:
			log.Printf("via: Redirect from a @post action is not supported; use PostForm or a link")
		}
	}
	if mode == modeNative {
		renderNative()
		return
	}
	if renderPatch == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b := renderPatch()
	if b == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeSecurityHeaders(w)
	w.Write(b)
}
