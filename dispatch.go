package via

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
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

// actionResult is what running an action produced. panicked is set
// only on the live path: it happens on the connection's own goroutine, so it
// must be carried back across the channel to the POST that triggered it
// rather than answered where it occurred.
type actionResult struct {
	redirect  string
	panicked  bool
	badArg    error  // set when a value-carrying action's ?a= failed to decode (see badActionArg) — answers 400, not 500
	pushWork  func() // live path only: the dirty-signals + element push, run by liveConn.run right after acking (see liveRunAction)
	gone      string // live path only: set when the unit/action lookup (run on the island goroutine — see dispatchLive) came up invalid; the reason is the response body
	forbidden string // live path only: set when the session-bound check (run on the island goroutine — see dispatchLive) rejects the request
}

// mount bundles a page's per-request wiring — built once in Router.Mount and
// shared by its GET, action, and SSE routes so origin floor, OnInit, and body
// caps can't drift between them.
type mount struct {
	cfg         *config
	sessions    *sessionManager
	reg         *registry
	newInst     func() instance
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
// embedding depth, nil when this connection has none. Called on the island
// goroutine itself (see dispatchLive) so the lookup is atomic with the
// dispatch it guards; it still takes the same lock replace does since a
// future caller reading it from elsewhere shouldn't have to remember to add one.
func (c *liveConn) unit(island int) *Ctx {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.units[island]
}

// unitAddr is c's own dispatch address in /_via/a/{island}/{act} — 0 for the
// root, islandIdx+1 for an embedded unit at any depth.
func unitAddr(c *Ctx) int {
	if c != nil && c.isIsland {
		return c.islandIdx + 1
	}
	return 0
}

// dispatch is the single entry point for every action POST on a mount — a
// Datastar @post, a native PostForm submit, or a live unit's action — at
// {base}/_via/a/{island}/{act} (the root is island 0). One origin floor, one
// OnInit, one body decode: the six transports this replaced each
// re-implemented these and drifted (OnInit never ran on an island action; a
// live/island action silently dropped ctx.Redirect).
func (m *mount) dispatch(w http.ResponseWriter, req *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			recoverToHTTP(w, req, rec, "action")
		}
	}()
	if !originAllowed(req, m.cfg) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
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
	act := req.PathValue("act")
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
			// header/field, same action-id space — so this must be checked
			// explicitly rather than relying on anything else to fail first.
			http.Error(w, "no such island", http.StatusGone)
			return
		}
		// Every action now echoes the tab id, live or not, so a live page's
		// PLAIN child posts one too — and its address was never registered on
		// the connection. Membership is stable (a unit is published before the
		// tab id is, and never removed), so this check is safe off the island
		// goroutine, unlike the staleness lookup dispatchLive still does there.
		if lc.unit(island) != nil {
			m.dispatchLive(w, req, mode, lc, island, act, in, base)
			return
		}
	}
	m.dispatchStateless(w, req, mode, island, act, in, base)
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

// dispatchLive runs action act against a connected live unit on its
// connection's serialized goroutine and WAITS for the mutation's result —
// synchronous, unlike the old fire-and-forget island dispatch, so a
// Redirect, the session cookie, and a panic all resolve on THIS response
// exactly like a stateless action. The wait is bounded by req.Context() as
// well as the connection closing, so a stalled peer elsewhere on the stream
// can't park this POST's goroutine forever (see liveConn.run).
func (m *mount) dispatchLive(w http.ResponseWriter, req *http.Request, mode actionMode, lc *liveConn, island int, act string, in map[string]json.RawMessage, base string) {
	res, ok := lc.run(req.Context(), func() actionResult {
		// A closure queued on pulse runs regardless of what its caller does
		// meanwhile: if req.Context() is already done, run's own second
		// select has already given up and answered 410 to the client (see
		// liveConn.run) — applying the action now would double-apply on a
		// client retry, and passing w into liveRunAction would hand a dead
		// ResponseWriter to Session().Put (SetCookie -> Header() would race
		// the server's post-handler teardown). Skip the mutation entirely —
		// w is never threaded into a Ctx below.
		if req.Context().Err() != nil {
			return actionResult{gone: "request abandoned"}
		}
		// Checked here, on the island goroutine, rather than by the dispatching
		// request's own goroutine before this closure was posted — a
		// cookieless dispatch that passed a pre-queue check while the
		// connection was still unbound could otherwise be applied AFTER a
		// concurrent live login bound it, since the check and the run were on
		// different goroutines with a queue hop between them. Checking here
		// makes the compare and the run atomic on this one serialized
		// goroutine, closing that window.
		if bound := lc.boundSession(); bound != nil {
			// The connection is bound to a real session — either the one open
			// at connect, or one a live action minted/rotated afterward (see
			// liveConn.bindSession) — and a dispatch against it must carry
			// that SAME session (by pointer, not id — a Rotate since connect
			// moves the pointer to a new id, never a new data object).
			// Otherwise a leaked tab id is a bearer credential good from any
			// request, session or none, once the origin floor is open.
			_, s, _ := m.sessions.resolve(req)
			if s != bound {
				return actionResult{forbidden: "session mismatch"}
			}
		}
		// u is looked up here, on the island goroutine, rather than by the
		// dispatching request's own goroutine before this closure was posted —
		// a concurrent push (a tick, another action, Listen fan-out) replaces
		// lc.units between the two, so a unit read earlier could already be
		// stale by the time it runs. A stale unit here is more than an address
		// mismatch: Signal.bind stamps the signal's dirty sink at render time,
		// so acting against a unit older than the signal's current binding
		// would attribute the write to a Ctx nothing downstream ever reads —
		// silently dropping the patch. Reading it here makes it current by
		// construction: nothing else touches lc.units between this line and
		// the dispatch it guards, both on this same serialized goroutine.
		u := lc.unit(island)
		if u == nil {
			return actionResult{gone: "no such island"}
		}
		a, ok := u.actions[act]
		if !ok {
			return actionResult{gone: unknownAction(u, act)}
		}
		return liveRunAction(w, req, m.sessions, lc, u, in, a)
	})
	if !ok {
		http.Error(w, "live connection closed", http.StatusGone)
		return
	}
	if res.forbidden != "" {
		http.Error(w, res.forbidden, http.StatusForbidden)
		return
	}
	if res.gone != "" {
		http.Error(w, res.gone, http.StatusGone)
		return
	}
	if res.badArg != nil {
		http.Error(w, "bad action arg: "+res.badArg.Error(), http.StatusBadRequest)
		return
	}
	if res.panicked {
		http.Error(w, "action failed", http.StatusInternalServerError)
		return
	}
	if mode == modeNative {
		respond(w, req, mode, res.redirect, func() {
			// A native <form> submit is a real navigation: the browser
			// replaces the whole document with whatever this response
			// carries. That is the page a brand-new connection will hold —
			// a fresh instance, OnInit run — exactly like dispatchStateless,
			// not a snapshot of the dying connection's live tree (which the
			// client's own reconnect is about to reseed anyway once this
			// response's data-init opens a new SSE stream).
			inst := m.newInst()
			ctx := newRootCtx(nil, true, base, nil)
			ctx.islandV = inst
			if runOnInit(inst.v, ctx, w, req, m.sessions) != nil {
				return
			}
			body := renderRootWith(ctx, inst.v)
			writeHTMLPage(w, m.cfg, body, len(liveUnits(ctx)) > 0, base+"/_via/sse")
		}, nil)
		return
	}
	respond(w, req, mode, res.redirect, nil, nil) // patch: nil — the push already framed it
}

// liveRunAction hydrates unit's signals from in, runs act, and returns
// as soon as the mutation is known. unit is the bind Ctx the last push
// produced (or, before any push, the connect render) — its actions/hydrators
// table is current because every push is a render, so no render happens here
// before the action runs. It runs on the connection's serialized goroutine
// while the triggering POST blocks in liveConn.run — so w is safe to write to
// here (the session cookie, a queued Redirect) exactly as a stateless action
// would be. dispatchLive already resolved act against unit.actions before
// calling in here. The recover below is a last-resort backstop.
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
func liveRunAction(w http.ResponseWriter, req *http.Request, sessions *sessionManager, lc *liveConn, unit *Ctx, in map[string]json.RawMessage, act action) (res actionResult) {
	defer func() {
		if rec := recover(); rec != nil {
			if bad, ok := rec.(badActionArg); ok {
				res = actionResult{badArg: bad.err}
				return
			}
			log.Printf("via: live action panic: %v\n%s", rec, debug.Stack())
			res = actionResult{panicked: true}
		}
	}()
	for slot, raw := range in {
		if hydrate, ok := unit.hydrators[slot]; ok {
			hydrate(raw)
		}
	}
	// A fresh Ctx per dispatch, not unit itself: unit is the render-time Ctx a
	// Tick or Listen handler holds onto for the life of the connection, so
	// writing req/sessW/redirect onto it here would rewrite what that handler
	// sees. Everything the action mutates through method values (Signal.Set,
	// State.Set) still lands on unit, because those handles were bound to it
	// at render time — only req/session/redirect plumbing moves to rc.
	//
	// beforeSession is resolved from req BEFORE the action runs (not just
	// "rc.session == nil after"): Ctx.Session() lazily resolves the SAME
	// req's cookie regardless of whether the action reads or writes, so a
	// request that already carries a valid (e.g. an attacker's own) cookie
	// would otherwise look identical, post-hoc, to one that minted a session
	// just now — binding the connection to a session the action never
	// created (I1). Binding only when the request arrived with no resolvable
	// session at all, and the action left one in place, means an action must
	// have actually MINTED it (Session().Put/Rotate) for the bind to fire.
	_, beforeSession, _ := sessions.resolve(req)
	rc := &Ctx{req: req, sessions: sessions, sessW: w}
	unit.dirty = map[string]any{}
	act.fn(rc)

	// The action may have just minted a session (Session().Put or .Rotate) on
	// a connection that was anonymous at connect — bind it now so the tab id
	// stops being a bearer credential the instant this action logs it in
	// (see H1; lc.bindSession is a no-op once already bound).
	if beforeSession == nil && rc.session != nil && rc.session.data != nil {
		lc.bindSession(rc.session.data)
	}

	// A deliberate server-driven signal change (e.g. clearing the composer)
	// reaches the client as a signal-patch — the element push omits
	// data-signals, so a morph never clobbers what the user is typing.
	dirty, push := unit.dirty, unit.push
	return actionResult{
		redirect: rc.redirect,
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

// unknownAction is the 410 body for an id the freshly-rendered unit does not
// carry: the handler is gone from this render — a branch closed, or (the
// common wiring mistake) OnInit failed to restore the UI state the View
// branches on. The diagnosis — which ids ARE bound, and the Go method each
// came from — goes to the server log; the response body names only the id the
// client asked for, since the bound list is a map of the render's Go type and
// method names and the client is not entitled to it.
func unknownAction(u *Ctx, act string) string {
	have := make([]string, 0, len(u.actions))
	for id, a := range u.actions {
		have = append(have, id+" ("+a.name+")")
	}
	sort.Strings(have)
	log.Printf("via: no such action %s; this render binds: %s", act, strings.Join(have, ", "))
	return "no such action " + act + "; this render does not bind it"
}

// dispatchStateless is dispatch's non-live path: bind a fresh instance, run
// OnInit, run the acted-on unit's action, then answer per mode.
func (m *mount) dispatchStateless(w http.ResponseWriter, req *http.Request, mode actionMode, island int, act string, in map[string]json.RawMessage, base string) {
	inst := m.newInst()
	bind := newRootCtx(in, true, base, map[string]any{}) // nil only would read as "declare everything"
	bind.islandV = inst                                  // so bind.unit(0)'s liveness reads the same way an embedded island's does
	if runOnInit(inst.v, bind, w, req, m.sessions) != nil {
		return
	}
	rootBefore := renderRootWith(bind, inst.v)
	u := bind.unit(island)
	if u == nil {
		http.Error(w, "no such island", http.StatusGone)
		return
	}
	a, ok := u.actions[act]
	if !ok {
		http.Error(w, unknownAction(u, act), http.StatusGone)
		return
	}
	if u.live {
		// A live unit's action only routes through the tab handshake in
		// dispatch; reaching here means the tab was missing/stale, so fail
		// closed rather than mutating a throwaway instance.
		http.Error(w, "no live connection for this tab", http.StatusGone)
		return
	}
	u.req = req
	u.sessions = m.sessions
	u.sessW = w
	a.fn(u) // no long-lived handler holds this render's Ctx (stateless), so u is its own dispatch Ctx

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
func (m *mount) rerenderStateless(island int, rootBefore []byte, inst instance, bind, u *Ctx, base string) []byte {
	seen := bind.slotSet()
	if island == 0 {
		afterCtx, after := renderRootPatch(inst, nil, base, bind.dirtyAll(), seen)
		if len(liveUnits(bind)) == 0 {
			assertRenderInvariantLiveness(len(liveUnits(afterCtx)) > 0)
		}
		if bytes.Equal(rootBefore, after) {
			return nil
		}
		return after
	}
	afterCtx, afterInner := renderIslandBind(u.islandIdx, u.islandV, base)
	assertRenderInvariantLiveness(afterCtx.live)
	if bytes.Equal(u.rendered, afterInner) && len(u.dirty) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteString(`<div id="via-i` + strconv.Itoa(u.islandIdx) + `"`)
	writeSignalsAttr(&buf, afterCtx.order, afterCtx.initial, u.dirty, seen)
	buf.WriteString(`>`)
	buf.Write(afterInner)
	buf.WriteString(`</div>`)
	return buf.Bytes()
}

// assertRenderInvariantLiveness fails the action that just turned a unit live
// which the page was served non-live. Liveness is decided by the discovery
// render (a Tick/Listen in OnInit, or a State/List in the View), and the page
// only bootstraps an SSE stream when that verdict is yes. A unit whose View
// reaches its State through a branch that was CLOSED at GET is served
// non-live; a stateless action that opens the branch leaves the tab holding a
// unit that now demands a connection it never opened, and every action after
// it 410s "no live connection" — a frozen tab with no clue why.
//
// It panics (→ 500, logged, the transport's own recover) rather than logging
// and carrying on: the failure is deterministic for that render path and the
// alternative is a tab that is already dead but does not say so. Keep the
// liveness verdict render-invariant — see State.Display.
func assertRenderInvariantLiveness(nowLive bool) {
	if !nowLive {
		return
	}
	panic("via: this action turned a unit live that the page was served non-live — " +
		"the tab has no SSE connection and every action after this one would 410. " +
		"Liveness must not depend on a branch an action can open: render the State " +
		"unconditionally, or register a Tick/Listen in OnInit so the page is live from " +
		"the first paint")
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
