// Package via is a server-driven reactive UI toolkit built on the h DSL and the
// Datastar client: stateless request/response pages, SSE-backed live islands,
// server-authoritative State/List/Signal, and always-on sessions.
//
// Hard guarantees (the point of the design): no '&' at any user call site, no
// reflection, no closures in the public API surface, no any in element/child
// signatures. The library is stdlib-only. Identifier strings do appear at the
// edges the caller controls directly — ctx.Param[T]("id"), FormFile("avatar"),
// Mount("/thread/{id}") — but never as an internal wire-name a caller could
// desync (see the Field-Embeddable Types convention).
package via

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// datastarJS is the vendored Datastar client, served at /_via/datastar.js.
//
//go:embed datastar.js
var datastarJS []byte

// viewer is the (pointer) contract a root must satisfy: a pure, ctx-free View.
type viewer interface{ View() h.H }

// ptrViewer is the constraint every Register/Mount call site needs — one named
// alias instead of the same anonymous interface repeated at each generic entry
// point. Exported (rather than kept package-private) so `go doc` renders it
// as `*T; // Has unexported methods` at Register/Mount's signature — an
// unexported alias showed up as a bare, unresolvable name instead.
type ptrViewer[T any] = interface {
	*T
	viewer
}

// Ctx is the per-request binder. It assigns positional slot/action ids during a
// render pass, hydrates signals from the request, and records the per-slot
// initial values for the page-level data-signals declaration. It implements
// hcore.Binder.
type Ctx struct {
	inSignals   map[string]json.RawMessage       // hydrated from the request
	nextSig     int                              // next signal slot index
	order       []string                         // slots in assignment order
	initial     map[string]any                   // per-slot value seen at render time
	actions     []func(*Ctx)                     // positional action table; dispatch calls each with a fresh per-dispatch Ctx, never this one
	hydrators   map[string]func(json.RawMessage) // per-slot value updater, kept from the last render so a live action can hydrate without re-rendering
	ticks       []tickReg                        // live-island timer registrations
	subs        []subStarter                     // live-island external subscriptions
	disposers   []func()                         // live-island teardown, run on disconnect
	island      bool                             // true while rendering a live island
	dirty       map[string]any                   // signals an action Set this pass (→ signal-patch)
	declareOnly map[string]any                   // when non-nil, declare only these slots (stateless action patch)
	req         *http.Request                    // the request that triggered this handler (nil during a pure render)
	sessions    *sessionManager                  // per-Register session manager (always constructed; cookie is lazy)
	sessW       http.ResponseWriter              // response writer for issuing the session cookie; nil in a live action
	session     *Session                         // resolved session handle, cached per Ctx
	islands     []*Ctx                           // embedded child islands, in positional order (parent binder only)
	isIsland    bool                             // true when this Ctx binds an embedded island's child View
	islandIdx   int                              // this island's flat, page-wide index (shared via pass), used in its action path
	islandV     viewer                           // the island's child viewer, for re-rendering on action
	rendered    []byte                           // this island's inner HTML from the discovery render (for 204 compare)
	push        func()                           // re-render THIS island and frame it on the stream (set per live unit)
	declare     bool                             // whether this render declares page-level data-signals (first paint, not a push)
	base        string                           // mount path prefix for action POSTs ("" for the single-page root)
	redirect    string                           // pending Redirect target, applied after a handler returns
	pass        *renderPass                      // shared flat-index allocator during a root-level render; nil for an island's own standalone render
	conn        *liveConn                        // set during a root-level render on a live connection, so embedViewer can find an already-connected descendant's own instance
	underLive   bool                             // true when an ancestor (not necessarily the immediate parent) is a live unit — Embed refuses a live child here (see embedViewer)
	digestPH    string                           // this unit's shape-digest placeholder, lazily allocated on first action write and substituted for the real digest once the render ends
	connected   bool                             // true once OnConnect has returned — runLiveStream already snapshotted ticks/subs by then, so a later Tick/Listen on this Ctx would silently no-op; they log loudly instead
}

// Request returns the HTTP request that triggered this handler, for advanced
// request-native wiring (auth headers, cookies, RemoteAddr, query). It is set
// in a stateless action (the action POST), in OnConnect and the ticks and
// subscriptions that run under it (the SSE connect request), and in a live
// action (the action POST that triggered it).
//
// Read-only: the body is already consumed into the request's signals, and for a
// live action — which runs on the island goroutine after the POST has acked —
// the request's Context may already be done. Read headers, cookies, URL,
// RemoteAddr, TLS. On a live island the connect request is retained for the
// connection's lifetime (ticks and subscriptions read it). Returns nil if no
// request is in scope (e.g. a bare render).
func (c *Ctx) Request() *http.Request { return c.req }

// newCtx builds a Ctx with the given hydration map (may be nil for a GET page).
func newCtx(in map[string]json.RawMessage) *Ctx {
	return &Ctx{
		inSignals: in,
		initial:   map[string]any{},
		dirty:     map[string]any{},
		hydrators: map[string]func(json.RawMessage){},
	}
}

// digestPlaceholder lazily allocates c's shape-digest placeholder token from
// its render pass. Every action c writes shares this one token — the digest
// itself isn't knowable until the whole render ends, so writeActionAttr and
// PostForm write the placeholder now and renderRootCore/renderIslandInner
// substitute the real digest into the finished buffer.
func (c *Ctx) digestPlaceholder() string {
	if c.digestPH == "" {
		if c.pass != nil {
			c.digestPH = c.pass.nextDigestToken()
		} else {
			c.digestPH = "\x00vD\x00" // no pass: a bare render with no dispatch behind it
		}
	}
	return c.digestPH
}

// shapeDigest fingerprints this unit's own render shape: its signal order and
// its own action count. dispatch recomputes it fresh and 410s on any
// mismatch — a branched View that shifted an action's index since the
// client's copy was rendered no longer silently misroutes a click, it 410s.
// It deliberately does not fold in embedded islands' shapes: a live island's
// own push re-renders and reframes only itself, never its parent's already-
// shipped URLs, so folding a child's shape in here only left the parent
// permanently 410 the next time the child's (unrelated) shape happened to
// change — see the v0.8 coherence notes on the child-shape-in-parent-digest
// bug.
func (c *Ctx) shapeDigest() string {
	h := sha256.New()
	io.WriteString(h, strings.Join(c.order, ","))
	io.WriteString(h, "|")
	io.WriteString(h, strconv.Itoa(len(c.actions)))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))[:8]
}

// dirtyAll gathers every signal this pass wrote, across the whole page. A Set
// inside an embedded island lands on that island's own binder, not the root's,
// so the root dirty map alone would miss it and the patch would silently drop
// the island's write.
func (c *Ctx) dirtyAll() map[string]any {
	if len(c.islands) == 0 {
		return c.dirty
	}
	all := make(map[string]any, len(c.dirty))
	maps.Copy(all, c.dirty)
	for _, isl := range c.islands {
		maps.Copy(all, isl.dirtyAll())
	}
	return all
}

// binderCtx adapts a Ctx to hcore.Binder so the binder plumbing (signal slots,
// action ids) stays off Ctx's public surface — handlers see a lean Ctx, the
// renderer sees the four binder verbs.
type binderCtx struct{ c *Ctx }

func (b binderCtx) SignalName() string                             { return b.c.signalName() }
func (b binderCtx) DeclareSignal(slot string, initial any)         { b.c.declareSignal(slot, initial) }
func (b binderCtx) SignalInit(slot string) (any, bool)             { return b.c.signalInit(slot) }
func (b binderCtx) Hydrator(slot string, fn func(json.RawMessage)) { b.c.hydrator(slot, fn) }

// ctxOf unwraps the Ctx behind a renderer's binder; nil when the binder is not
// via's own (a bare h render).
func ctxOf(b hcore.Binder) *Ctx {
	if bc, ok := b.(binderCtx); ok {
		return bc.c
	}
	return nil
}

// signalName allocates the next first-use signal name ("s0","s1",…). A handle
// calls it once and caches the result, so a signal's identity is the handle,
// not its render position. hcore.Binder.
func (c *Ctx) signalName() string {
	name := "s" + strconv.Itoa(c.nextSig)
	c.nextSig++
	// An embedded island binds in its own Ctx, so two islands would both mint
	// "s0" and collide in the page's one global Datastar store. Prefix the slot
	// with the island index to keep sibling islands' signals distinct.
	if c.isIsland {
		name = "i" + strconv.Itoa(c.islandIdx) + "_" + name
	}
	return name
}

// declareSignal records that slot participates in this render with the given
// initial value, for the page-level data-signals declaration. Idempotent within
// a render: the first declaration fixes the order, later ones (e.g. a Bind and a
// Display of the same signal) only refresh the value. hcore.Binder.
func (c *Ctx) declareSignal(slot string, initial any) {
	if _, seen := c.initial[slot]; !seen {
		c.order = append(c.order, slot)
	}
	c.initial[slot] = initial
}

// signalInit returns the hydrated raw value for a slot, if the request carried
// one. The bool reports presence. hcore.Binder.
func (c *Ctx) signalInit(slot string) (any, bool) {
	if c.inSignals == nil {
		return nil, false
	}
	raw, ok := c.inSignals[slot]
	if !ok {
		return nil, false
	}
	return raw, true
}

// actionSlot registers a handler and returns its positional id "0","1",….
// Unlike SignalName/DeclareSignal/Hydrator this is not on hcore.Binder — via's
// own OnClick/OnSubmit/OnChange/PostForm are its only callers, so it stays a
// plain Ctx method.
func (c *Ctx) actionSlot(fn func(*Ctx)) string {
	idx := len(c.actions)
	c.actions = append(c.actions, fn)
	return strconv.Itoa(idx)
}

// hydrator records slot's update function. A live unit keeps the table from its
// last render (every push is a render, so it is always current), so a live
// action hydrates the slots the client sent in place, without a re-render.
// hcore.Binder.
func (c *Ctx) hydrator(slot string, fn func(json.RawMessage)) {
	c.hydrators[slot] = fn
}

// PostForm renders a native <form method="post"> whose submit runs handler on
// the server — the server-rendered flow for sign-up/in, file uploads, and
// anything that ends in a Redirect. Unlike OnSubmit (a Datastar @post that
// element-patches in place), this is a real browser navigation: handler reads
// form fields via ctx.Request().FormValue and may via.Redirect. The form is
// always multipart, so a file <input> just works — read it with
// ctx.Request().FormFile(name). handler is a named method value; children are
// the form contents (inputs, button). No '&', no closure. It claims a slot in
// the same action table a @post event binding uses, so a page mixing OnSubmit
// and PostForm never collides on an id.
//
// Inside a live unit it also carries a hidden _viatab field, reactively kept
// in sync with the $_viatab signal: a native form submit is a plain browser
// POST, which cannot set the X-Via-Tab header a Datastar @post uses, so
// dispatch falls back to this field to route the submit to the connection —
// under the exact same per-mount ownership check the header gets.
func PostForm(handler func(*Ctx), children ...h.H) h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		ctx := ctxOf(r.Binder())
		if ctx == nil {
			return
		}
		idx := ctx.actionSlot(handler)
		island := 0
		if ctx.isIsland {
			island = ctx.islandIdx + 1
		}
		r.WriteString(`<form method="post" enctype="multipart/form-data" action="` +
			ctx.base + `/_via/a/` + strconv.Itoa(island) + `/` + idx + `?v=` + ctx.digestPlaceholder() + `">`)
		if ctx.island {
			r.WriteString(`<input type="hidden" name="` + tabFormField + `" data-attr-value="$_viatab">`)
		}
		for _, c := range children {
			r.Render(c)
		}
		r.WriteString(`</form>`)
	})
}

// paramMiss is the panic sentinel Param throws when a real request carries a
// segment that cannot decode into the asked-for type (/thread/abc read as
// Param[int]("id")). The transport recovers it into a 404: the URL space
// simply has no such page. It is a control-flow sentinel, not an error
// value — user code never sees it.
type paramMiss struct {
	name string
	seg  string
}

// Param reads the mount pattern's named {name} segment — the same syntax
// http.ServeMux uses (r.Mount("/thread/{id}", …) → ctx.Param[int]("id")).
// Callable from OnInit and actions (which carry a Ctx); View is ctx-free and
// so cannot read params — load them in OnInit into a field instead. It
// requires a request in scope: a live unit's Ctx has one throughout its
// connection (set once at OnConnect), so Param is also safe from Tick,
// Subscribe, and Listen handlers — but not from a bare render with no
// request behind it at all (see Ctx.Request).
//
// A segment that cannot decode into T answers the request with 404 (the URL
// names a page that doesn't exist — never a silent zero value). Naming a
// segment the mount pattern doesn't have is a wiring mistake and panics.
func (c *Ctx) Param[T any](name string) T {
	seg := c.req.PathValue(name)
	if seg == "" && !strings.Contains(c.req.Pattern, "{"+name+"}") {
		panic("via: Param: the mount pattern has no {" + name + "} segment")
	}
	return decodeSegment[T](seg, name)
}

// decodeSegment turns a raw URL segment into T. Strings pass through verbatim
// (a path segment is not quoted JSON); everything else (int/float/bool) decodes
// as JSON, which parses "42" → 42 without a reflect-driven scalar table. A
// segment that does not decode panics with the paramMiss sentinel (→ 404).
func decodeSegment[T any](seg string, name string) T {
	var v T
	if sp, ok := any(&v).(*string); ok {
		*sp = seg
		return v
	}
	if err := json.Unmarshal([]byte(seg), &v); err != nil {
		panic(paramMiss{name: name, seg: seg})
	}
	return v
}

// Redirect navigates the browser to path after the current handler returns.
// It is a PostForm/OnInit facility: from a PostForm handler it is a 303 See
// Other on the native form submit; from OnInit it 303s before the View ever
// renders. path must be http/https or a same-origin relative path; other
// schemes are rejected. A Redirect set from a Datastar @post action is logged
// and dropped — a live click cannot navigate; use an `<a href>` or a
// PostForm handler instead.
func (c *Ctx) Redirect(path string) {
	c.redirect = path
}

// OnClick wires a click to a POST action. fn is a named method value (e.g.
// c.Inc) — pointer-bound to the via-owned instance, so no '&' at the call site.
func OnClick(fn func(*Ctx)) h.Attr { return onEvent("click", fn) }

// OnSubmit wires a form submit to a POST action. Datastar auto-prevents the
// form's default submit, so no prevent modifier is needed.
func OnSubmit(fn func(*Ctx)) h.Attr { return onEvent("submit", fn) }

// OnChange wires a change event (fires on commit/blur) to a POST action.
func OnChange(fn func(*Ctx)) h.Attr { return onEvent("change", fn) }

// onEvent emits the Datastar event binding for a named method value. At render
// it claims a positional action id and writes data-on:<event>="@post('/_via/a/N')".
func onEvent(event string, fn func(*Ctx)) h.Attr {
	return hcore.DynAttr(func(r *hcore.Renderer) {
		ctx := ctxOf(r.Binder())
		if ctx == nil {
			return
		}
		// fn is stored as-is: dispatch calls it with a fresh per-dispatch Ctx,
		// never the one bound here at render time (see liveRunAction).
		idx := ctx.actionSlot(fn)
		writeActionAttr(r, ctx, event, idx, "")
	})
}

// OnClickArg wires a click to an action that carries a value — the row's own
// datum rides with the click (a query arg), so the handler receives it as a
// typed parameter and acts on THAT item regardless of its render position. fn is
// a named method value (e.g. l.Delete); arg is plain data (e.g. todo.ID), not an
// identifier string. Use it for per-row actions in a list. No '&', no closure.
func OnClickArg[T any](fn func(*Ctx, T), arg T) h.Attr { return onEventArg("click", fn, arg) }

// badActionArg is the panic sentinel a value-carrying action's slot throws
// when ?a= fails to decode into T — the arg is client-controlled input (any
// client can POST a malformed or wrong-typed one), so the honest answer is
// 400, not silently handing the handler a zero value it might act on (e.g.
// deleting row 0). recoverToHTTP and liveRunAction both recognize it.
type badActionArg struct{ err error }

// onEventArg is onEvent for a value-carrying action: it JSON-encodes arg into the
// action's query (?a=…) so the client posts the row's datum, and the dispatched
// slot decodes it from the request and hands it to fn. Identity rides with the
// click, so a renumbered list can't misroute.
func onEventArg[T any](event string, fn func(*Ctx, T), arg T) h.Attr {
	return hcore.DynAttr(func(r *hcore.Renderer) {
		ctx := ctxOf(r.Binder())
		if ctx == nil {
			return
		}
		idx := ctx.actionSlot(func(rc *Ctx) {
			if rc.req == nil {
				return
			}
			var v T
			raw := rc.req.URL.Query().Get("a")
			if raw == "" || raw == "null" {
				panic(badActionArg{err: errors.New("missing action arg")})
			}
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				panic(badActionArg{err: err})
			}
			fn(rc, v)
		})
		query := ""
		if data, err := json.Marshal(arg); err == nil {
			query = "?a=" + url.QueryEscape(string(data))
		}
		writeActionAttr(r, ctx, event, idx, query)
	})
}

// writeActionAttr writes the data-on:<event>="@post('PATH?query'<,opts>)" binding
// for a claimed action slot. Written raw (not via h.Data): the value is a
// Datastar expression whose single-quotes must survive verbatim, and it is fully
// via-generated (fixed template + the via-controlled event name + a numeric id +
// a url-encoded arg), so no user input reaches it and there is no injection
// surface. Datastar v1's colon syntax (data-on:<event>); the old dash form is
// parsed as a nonexistent plugin and silently dropped. Every action posts to
// {base}/_via/a/{island}/{n}?v={digest} — the root is island 0, an embedded
// child is its islandIdx+1, and the digest is this render's shape fingerprint
// (see Ctx.shapeDigest): dispatch 410s a click whose digest no longer matches
// instead of misrouting it against a shifted index. On a live unit the POST
// routes to THIS connection's instance, so it echoes the tab id (the _viatab
// local signal the SSE set) as the X-Via-Tab header; a stateless page omits it.
func writeActionAttr(r *hcore.Renderer, ctx *Ctx, event, idx, query string) {
	base, island, digest := "", 0, ""
	if ctx != nil {
		base = ctx.base // mount prefix: a page at /profile posts to /profile/_via/a/{island}/{n}
		if ctx.isIsland {
			island = ctx.islandIdx + 1
		}
		digest = ctx.digestPlaceholder()
	}
	path := base + "/_via/a/" + strconv.Itoa(island) + "/" + idx
	if digest != "" {
		if query == "" {
			query = "?v=" + digest
		} else {
			query += "&v=" + digest
		}
	}
	opts := ""
	if ctx != nil && ctx.island {
		opts = ",{headers:{'X-Via-Tab':$_viatab}}"
	}
	r.WriteString(` data-on:` + event + `="@post('` + path + query + `'` + opts + `)"`)
}

// decodeActionBody decodes the client signals from an action POST under a body
// cap. An empty body is the common no-signals case; a malformed or oversize body
// writes the error response and returns ok=false so the caller returns.
func decodeActionBody(w http.ResponseWriter, req *http.Request) (map[string]json.RawMessage, bool) {
	in := map[string]json.RawMessage{}
	if req.Body == nil {
		return in, true
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxActionBody))
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return nil, false
	}
	return in, true
}

// renderRootBase renders v into the morph target <div id="root" …>…</div> and
// returns the bind Ctx (slots/actions assigned this pass) plus the bytes. Used
// for the initial page body and for element-patch responses. in hydrates client
// signals during the render; pass nil for no hydration (e.g. the post-action
// response render, which must reflect mutated server state, not request echoes).
// It renders under an explicit action base path — the router mounts a page
// under /path, so its actions must post to /path/_via/a/{n}, not the root
// /_via/a/{n}; base is "" for the single-page Register. declareSignals
// controls the page-level data-signals attribute: the GET first paint
// declares the signals so the client store is seeded, but a LIVE SSE push
// omits it — re-declaring on every push would re-merge (clobber) a client
// signal the user is editing (their half-typed message vanishing when
// someone else's message arrives); deliberate server-driven signal changes
// ride an explicit signal-patch instead. only is threaded to
// writeSignalsAttr (and to embedded islands via ctx.declareOnly) so this one
// render path serves the full first paint (only nil), the declaration-free
// live push (declareSignals false), and the restricted action patch (only
// non-nil, see renderRootPatch). conn is the live connection driving this
// render (nil for first paint and every stateless render), so embedViewer
// can reuse an already-connected descendant's own instance instead of a
// fresh by-value copy.
func renderRootBase(v viewer, in map[string]json.RawMessage, declareSignals bool, base string, only map[string]any, conn *liveConn) (*Ctx, []byte) {
	ctx := newCtx(in)
	_, ctx.island = v.(Live)     // the root is a live unit exactly when it implements OnConnect
	ctx.declare = declareSignals // embedded islands declare their own signals only on a declaring render
	ctx.declareOnly = only
	ctx.base = base
	ctx.pass = &renderPass{} // fresh page-wide index allocator for this discovery walk
	ctx.conn = conn
	rr := hcore.NewRenderer(binderCtx{ctx})
	rr.Render(v.View())
	var b bytes.Buffer
	b.WriteString(`<div id="root"`)
	if declareSignals {
		writeSignalsAttr(&b, ctx.order, ctx.initial, only)
	}
	b.WriteString(`>`)
	b.Write(rr.Bytes())
	b.WriteString(`</div>`)
	out := b.Bytes()
	if ctx.digestPH != "" {
		out = bytes.ReplaceAll(out, []byte(ctx.digestPH), []byte(ctx.shapeDigest()))
	}
	return ctx, out
}

// renderRootPatch renders a stateless action's element-patch response. A
// stateless action answers with plain HTML, not an SSE stream, so the
// data-signals attribute is its only channel for a server-side Set — but
// re-declaring every slot on every action would overwrite the client's whole
// store, clobbering a value the user is mid-edit. That is the same hazard a live
// push avoids by omitting the attribute; here the attribute stays, restricted to
// only, the slots the action actually wrote. A nil only declares nothing.
func renderRootPatch(v viewer, in map[string]json.RawMessage, base string, only map[string]any, conn *liveConn) (*Ctx, []byte) {
	if only == nil {
		only = map[string]any{} // nil would read as "declare everything"
	}
	return renderRootBase(v, in, true, base, only, conn)
}

// Register builds an http.Handler serving the root composition. root is taken
// by value; per request via copies it into an addressable local and operates on
// the pointer (PT), so pointer-receiver methods and handles work without '&' at
// the call site. The PT constraint makes a missing or mistyped View() a
// compile error rather than a first-request 500 — Register(Counter{}) still
// infers T=Counter, PT=*Counter with zero type arguments.
func Register[T any, PT ptrViewer[T]](root T, opts ...Option) http.Handler {
	r := NewRouter(opts...)
	r.Mount[T, PT]("/", root)
	return r
}

// liveUnits collects every Live viewer discovered in bind's render, at any
// embedding depth — the root itself first, when it implements OnConnect,
// then its embedded live islands in render order. Each becomes its own unit
// on the connection; a page with no live content at all yields an empty
// slice.
func liveUnits(bind *Ctx) []*Ctx {
	var units []*Ctx
	if _, ok := bind.islandV.(Live); ok {
		units = append(units, bind)
	}
	appendLiveIslands(bind, &units)
	return units
}

// appendLiveIslands walks the discovered island tree recursively — a live
// grandchild gets no stream wiring unless discovery descends past its
// (live or plain) parent too.
func appendLiveIslands(ctx *Ctx, out *[]*Ctx) {
	for _, isl := range ctx.islands {
		if _, ok := isl.islandV.(Live); ok {
			*out = append(*out, isl)
		}
		appendLiveIslands(isl, out)
	}
}

// connectUnit wires unit's push closure to stream (whole-page at #root for the
// root unit, its own #via-i{idx} container for an island), registers unit on lc
// as this island's current unit, and runs its OnConnect once, recovering a
// panic into a connect error so one broken unit can't crash the rest of the
// handshake.
func connectUnit(unit *Ctx, req *http.Request, w http.ResponseWriter, sessions *sessionManager, stream *sseStream, base string, lc *liveConn) (err error) {
	unit.req = req
	unit.sessions = sessions
	unit.sessW = w
	if unit.isIsland {
		idx, v := unit.islandIdx, unit.islandV
		lc.replace(unit)
		unit.push = islandPush(idx, v, base, stream, lc)
	} else {
		v := unit.islandV
		lc.replace(unit)
		unit.push = rootPush(v, base, stream, lc)
	}
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("via: OnConnect panic: %v\n%s", rec, debug.Stack())
			err = errors.New("via: OnConnect panicked")
		}
	}()
	err = unit.islandV.(Live).OnConnect(unit)
	unit.connected = true // ticks/subs are snapshotted right after this call returns — see Tick/Listen
	return err
}

// rootPush renders v fresh and pushes it as the whole-page element-patch. The
// fresh render's bind Ctx replaces lc.root: it is the one whose actions and
// hydrators table a live action runs against next, so a live action needs no
// render of its own — the previous push already built it.
func rootPush(v viewer, base string, stream *sseStream, lc *liveConn) func() {
	var push func()
	push = func() {
		bind, body := renderRootBase(v, nil, false, base, nil, lc) // push omits data-signals
		bind.push = push
		lc.replace(bind)
		stream.frame(func(w io.Writer) { writePatchFrame(w, body) })
	}
	return push
}

// islandPush is rootPush for an embedded live island: it re-renders island idx
// in place and replaces the connection's current unit for it.
func islandPush(idx int, v viewer, base string, stream *sseStream, lc *liveConn) func() {
	var push func()
	push = func() {
		bind, body := renderIslandPatch(idx, v, base)
		bind.push = push
		lc.replace(bind)
		stream.frame(func(w io.Writer) { writePatchFrame(w, body) })
	}
	return push
}

// connect is the SSE stream's entry point at {base}/_via/sse — dispatch's
// preamble (origin floor, OnInit) followed by the connect render and the
// stream loop. Every mount gets the route; a page with no live content
// answers 404 on it.
func (m *mount) connect(w http.ResponseWriter, req *http.Request) {
	// Origin floor first: the stream opens a long-lived island goroutine +
	// timers and renders the app's HTML, so reject anything that can't prove
	// a same-origin (or explicitly trusted) source before allocating it.
	if !originAllowed(req, m.cfg) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	// The connect is a POST so it can carry the page's signals as a body
	// (capped + decoded); the island hydrates from them. Multiplexing reads
	// per-island state out of this on connect.
	connectSig, ok := decodeInput(w, req, modeDatastar)
	if !ok {
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	// Connection cap: bound concurrent streams (each holds an island
	// goroutine + timers). Increment-then-check so the gauge can't be raced
	// past the limit; on refusal give back the slot and 503. The admitted
	// path's defer below decrements when the stream ends.
	if m.liveCount.Add(1) > int64(m.maxLive) {
		m.liveCount.Add(-1)
		http.Error(w, "stream capacity reached", http.StatusServiceUnavailable)
		return
	}
	defer m.liveCount.Add(-1)
	headersSent := false
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("via: live stream panic: %v\n%s", rec, debug.Stack())
			// A panic before the stream's headers went out (the discovery
			// render, OnInit, connectUnit) would otherwise fall through to
			// Go's default: 200 with an empty body, telling the client the
			// connect succeeded. Once headers ARE sent this can't help (and
			// would log a superfluous-WriteHeader warning), so only answer
			// here for the pre-header case; a mid-stream panic is instead
			// caught per pulse item (see runPulseItem) so it never reaches here.
			if !headersSent {
				http.Error(w, "connect failed", http.StatusInternalServerError)
			}
		}
	}()
	pv := m.newInst()
	if runOnInit(pv, w, req, m.sessions) != nil { // load session/request data into fields first
		return
	}
	base := concreteBase(m.patternBase, req, m.names)
	// A half-open peer never cancels req.Context(); a failed frame write
	// is the only signal it's gone. Derive a cancelable context so a write
	// failure (or the per-frame deadline) tears the island(s) down here.
	streamCtx, cancel := context.WithCancel(req.Context())
	defer cancel()
	stream := &sseStream{
		w:       w,
		rc:      http.NewResponseController(w),
		timeout: sseWriteTimeout,
		cancel:  cancel,
	}
	keepalive := func() { stream.frame(writeKeepaliveFrame) }
	id := randomToken() // per-connection tab id (echoed as X-Via-Tab on actions)
	pulse := make(chan func())

	// The discovery render finds every unit the root's View holds this
	// render — the root itself (bind), when live, plus each embedded live
	// island — so a live root and live children are found the same way; the
	// root unit's own instance is pv, mirroring an island unit's islandV.
	bind, _ := renderRootBase(pv, connectSig, false, base, nil, nil)
	bind.islandV = pv
	units := liveUnits(bind)

	// No live units: this app has no live content (a stateless page POSTing
	// the stream endpoint), so there is nothing to stream.
	if len(units) == 0 {
		http.Error(w, "no live stream", http.StatusNotFound)
		return
	}

	// Built before the connect loop so each unit's push closure can register
	// itself as the connection's current unit for its island on every render —
	// a live action always runs against the last render's actions/hydrators.
	// Bind the connection to whatever session the connect request's cookie
	// already resolves to (nil for an anonymous connect) — this is the
	// credential dispatch requires a match against, closing the gap where a
	// leaked tab id was a bearer token good from any origin with no session
	// at all (see dispatch). Resolved BEFORE OnConnect, which may itself
	// mint or rotate a session; the point is the identity the browser
	// already held when it opened this stream, not one OnConnect creates.
	_, sess, _ := m.sessions.resolve(req)

	lc := &liveConn{
		mount:       m,
		pageRoot:    pv,
		pulse:       pulse,
		done:        streamCtx.Done(),
		pushSignals: func(j string) { stream.frame(func(w io.Writer) { writeSignalsFrame(w, j) }) },
		units:       map[int]*Ctx{},
		sess:        sess,
	}

	// Run each unit's OnConnect once, BEFORE the stream headers flush (so
	// OnConnect can still set the session cookie). A unit not yet connected
	// has no disposers, so disposing the whole (fixed) set on a failure
	// partway through only tears down the ones that actually ran.
	disposeAll := func() {
		for _, u := range units {
			for _, d := range u.disposers {
				d()
			}
		}
	}
	for _, u := range units {
		if err := connectUnit(u, req, w, m.sessions, stream, base, lc); err != nil {
			disposeAll()
			connectError(w, err)
			return
		}
		// OnConnect may have just minted or resolved a session (the
		// README-recommended "log in during OnConnect" pattern) where the
		// connect cookie alone left lc.sess nil — bind it now so the
		// connection isn't left as a bare-tab-id credential (see H1).
		if u.session != nil {
			lc.bindSession(u.session.data)
		}
	}

	// Register this connection so a live action POST (/_via/a/{island}/{n} +
	// X-Via-Tab) routes to the right unit on this connection's goroutine.
	m.reg.put(id, lc)
	defer m.reg.del(id)

	writeSSEHeaders(w)
	w.WriteHeader(http.StatusOK)
	headersSent = true
	stream.frame(func(w io.Writer) { writeSignalsFrame(w, `{"_viatab":"`+id+`"}`) })

	runLiveStream(streamCtx, units, pulse, keepalive, sseHeartbeat)
}
