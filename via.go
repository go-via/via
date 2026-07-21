// Package via is a server-driven reactive UI toolkit built on the h DSL and the
// Datastar client. Slice 1 is deliberately narrow: a hardened, stateless,
// request/response counter. No SSE, islands, Stream, State or Local yet.
//
// Hard guarantees (the point of the design): no '&' at any user call site, no
// user-facing identifier strings, no reflection, no closures in the public API
// surface, no any in element/child signatures. The library is stdlib-only.
package via

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/go-via/via/h"
)

// datastarJS is the vendored Datastar client, served at /_via/datastar.js.
//
//go:embed datastar.js
var datastarJS []byte

// viewer is the (pointer) contract a root must satisfy: a pure, ctx-free View.
type viewer interface{ View() h.H }

// Ctx is the per-request binder. It assigns positional slot/action ids during a
// render pass, hydrates signals from the request, and records the per-slot
// initial values for the page-level data-signals declaration. It implements
// h.Binder.
type Ctx struct {
	inSignals map[string]json.RawMessage // hydrated from the request
	nextSig   int                        // next signal slot index
	order     []string                   // slots in assignment order
	initial   map[string]any             // per-slot value seen at render time
	actions   []func()                   // positional action table
	ticks     []tickReg                  // live-island timer registrations
	subs      []subStarter               // live-island external subscriptions
	disposers []func()                   // live-island teardown, run on disconnect
	island    bool                       // true while rendering a live island
	dirty     map[string]any             // signals an action Set this pass (→ signal-patch)
	req       *http.Request              // the request that triggered this handler (nil during a pure render)
	sessions  *sessionManager            // per-Register session manager (always constructed; cookie is lazy)
	sessW     http.ResponseWriter        // response writer for issuing the session cookie; nil in a live action
	session   *Session                   // resolved session handle, cached per Ctx
	islands   []*Ctx                     // embedded child islands, in positional order (parent binder only)
	isIsland  bool                       // true when this Ctx binds an embedded island's child View
	islandIdx int                        // this island's positional index, used in its action path
	islandV   viewer                     // the island's child viewer, for re-rendering on action
	rendered  []byte                     // this island's inner HTML from the discovery render (for 204 compare)
	push      func()                     // re-render THIS island and frame it on the stream (set per live unit)
	declare   bool                       // whether this render declares page-level data-signals (first paint, not a push)
	base      string                     // mount path prefix for action POSTs ("" for the single-page root)
	forms     []func(*Ctx)               // positional native-form handlers (PostForm)
	uploads   []func(*Ctx, File)         // positional multipart-upload handlers (OnUpload)
	redirect  string                     // pending Redirect target, applied after a form handler returns
	params    []string                   // positional path-param segments ({} in the mount pattern)
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
	}
}

// shapeMatches reports whether the signal slots assigned during a bind pass
// (order) are exactly the slots the client carried in the request (in). The
// positional binding contract is only sound when the hydrated POST render
// reproduces the same slot set the GET page declared; any divergence means the
// View branched on a value and the action/slot indices no longer line up.
func shapeMatches(order []string, in map[string]json.RawMessage) bool {
	if len(order) != len(in) {
		return false
	}
	for _, slot := range order {
		if _, ok := in[slot]; !ok {
			return false
		}
	}
	return true
}

// binderCtx adapts a Ctx to h.Binder so the binder plumbing (signal slots,
// action ids) stays off Ctx's public surface — handlers see a lean Ctx, the
// renderer sees the four binder verbs.
type binderCtx struct{ c *Ctx }

func (b binderCtx) SignalName() string                     { return b.c.signalName() }
func (b binderCtx) DeclareSignal(slot string, initial any) { b.c.declareSignal(slot, initial) }
func (b binderCtx) SignalInit(slot string) (any, bool)     { return b.c.signalInit(slot) }
func (b binderCtx) ActionSlot(fn func()) string            { return b.c.actionSlot(fn) }

// ctxOf unwraps the Ctx behind a renderer's binder; nil when the binder is not
// via's own (a bare h render).
func ctxOf(b h.Binder) *Ctx {
	if bc, ok := b.(binderCtx); ok {
		return bc.c
	}
	return nil
}

// signalName allocates the next first-use signal name ("s0","s1",…). A handle
// calls it once and caches the result, so a signal's identity is the handle,
// not its render position. h.Binder.
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
// Display of the same signal) only refresh the value. h.Binder.
func (c *Ctx) declareSignal(slot string, initial any) {
	if _, seen := c.initial[slot]; !seen {
		c.order = append(c.order, slot)
	}
	c.initial[slot] = initial
}

// signalInit returns the hydrated raw value for a slot, if the request carried
// one. The bool reports presence. h.Binder.
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
// h.Binder.
func (c *Ctx) actionSlot(fn func()) string {
	idx := len(c.actions)
	c.actions = append(c.actions, fn)
	return strconv.Itoa(idx)
}

// PostForm renders a native <form method="post"> whose submit runs handler on
// the server — the server-rendered flow for sign-up/in and anything that ends in
// a Redirect. Unlike OnSubmit (a Datastar @post that element-patches in place),
// this is a real browser navigation: handler reads form fields via
// ctx.Request().FormValue and may via.Redirect. handler is a named method value;
// children are the form contents (inputs, button). No '&', no closure.
func PostForm(handler func(*Ctx), children ...h.H) h.H {
	return h.Dyn(func(r *h.Renderer) {
		ctx := ctxOf(r.Binder())
		if ctx == nil {
			return
		}
		idx := ctx.formSlot(handler)
		r.WriteString(`<form method="post" action="` + ctx.base + `/_via/f/` + idx + `">`)
		for _, c := range children {
			r.Render(c)
		}
		r.WriteString(`</form>`)
	})
}

// paramMiss is the panic sentinel Param throws when a real request carries a
// segment that cannot decode into the asked-for type (/thread/abc read as
// Param[int]). The transport recovers it into a 404: the URL space simply has
// no such page. It is a control-flow sentinel, not an error value — user code
// never sees it.
type paramMiss struct {
	n   int
	seg string
}

// Param reads the nth positional path param — the nth anonymous {} segment in
// the mount pattern (Mount(r, "/thread/{}", …) → Param[int](ctx, 0)). Positional,
// like actions and signals: no identifier string. Callable from OnInit and
// actions (which carry a Ctx); View is ctx-free and so cannot read params —
// load them in OnInit into a field instead.
//
// A segment that cannot decode into T answers the request with 404 (the URL
// names a page that doesn't exist — never a silent zero value). Reading an
// index the mount pattern doesn't have is a wiring mistake and panics.
func Param[T any](ctx *Ctx, n int) T {
	if ctx == nil || n < 0 || n >= len(ctx.params) {
		panic("via: Param index out of range — the mount pattern has no {} segment " + strconv.Itoa(n))
	}
	return decodeSegment[T](ctx.params[n], n)
}

// decodeSegment turns a raw URL segment into T. Strings pass through verbatim
// (a path segment is not quoted JSON); everything else (int/float/bool) decodes
// as JSON, which parses "42" → 42 without a reflect-driven scalar table. A
// segment that does not decode panics with the paramMiss sentinel (→ 404).
func decodeSegment[T any](seg string, n int) T {
	var v T
	if sp, ok := any(&v).(*string); ok {
		*sp = seg
		return v
	}
	if err := json.Unmarshal([]byte(seg), &v); err != nil {
		panic(paramMiss{n: n, seg: seg})
	}
	return v
}

// Guard is a per-route check run before OnInit on every method (page GET,
// action, form). Returning ok=false short-circuits the request with a 303 to
// redirect — the closure-free "middleware" unit: a value passed to Mount, not a
// Group(fn) that takes a closure at the call site.
type Guard func(*Ctx) (redirect string, ok bool)

// RequireSession guards a mount: if the session has no value of type T (i.e. the
// user is not signed in), the request is redirected to loginPath. T is the same
// type used with sess.Put/Get, keyed identically (a typed-nil pointer), so
// RequireSession[User]("/login") gates on a sess.Put(ctx, user) elsewhere.
func RequireSession[T any](loginPath string) Guard {
	return func(ctx *Ctx) (string, bool) {
		if _, ok := ctx.Session().load((*T)(nil)); ok {
			return "", true
		}
		return loginPath, false
	}
}

// File is an uploaded multipart file handed to an OnUpload handler: an io.Reader
// the app drains, plus its metadata. The framework owns no storage — the app
// reads the File and persists it wherever it likes.
type File interface {
	io.Reader
	Name() string        // the client's filename
	Size() int64         // size in bytes
	ContentType() string // the part's declared Content-Type
}

type uploadedFile struct {
	multipart.File
	hdr *multipart.FileHeader
}

func (f uploadedFile) Name() string        { return f.hdr.Filename }
func (f uploadedFile) Size() int64         { return f.hdr.Size }
func (f uploadedFile) ContentType() string { return f.hdr.Header.Get("Content-Type") }

// OnUpload renders a native multipart <form> whose submit uploads a file — the
// file analogue of PostForm/OnClickArg. handler receives the first uploaded file
// part as a via.File (and may read text fields via ctx.Request().FormValue and
// via.Redirect). A file needs a real multipart submit, so this is the one form
// that steps outside the Datastar @post JSON model. handler is a named method
// value; children are the form contents (a file <input>, a button). No '&', no
// closure.
func OnUpload(handler func(*Ctx, File), children ...h.H) h.H {
	return h.Dyn(func(r *h.Renderer) {
		ctx := ctxOf(r.Binder())
		if ctx == nil {
			return
		}
		idx := ctx.uploadSlot(handler)
		r.WriteString(`<form method="post" enctype="multipart/form-data" action="` + ctx.base + `/_via/upload/` + idx + `">`)
		for _, c := range children {
			r.Render(c)
		}
		r.WriteString(`</form>`)
	})
}

// uploadSlot registers a multipart-upload handler and returns its positional id.
func (c *Ctx) uploadSlot(fn func(*Ctx, File)) string {
	idx := len(c.uploads)
	c.uploads = append(c.uploads, fn)
	return strconv.Itoa(idx)
}

// Redirect navigates the browser to path after the current handler returns. From
// a PostForm handler it is a 303 See Other on the native form submit. From a
// Datastar @post action it is shipped as an executable location.assign() script
// stamped with the session's CSP nonce — which requires an active session (the
// nonce the document admits); without one the @post redirect is dropped (no
// navigation), so use PostForm for pre-session flows like sign-in. path must be
// http/https or a same-origin relative path; other schemes are rejected.
func Redirect(ctx *Ctx, path string) {
	if ctx != nil {
		ctx.redirect = path
	}
}

// writeRedirectScript ships a queued via.Redirect as an executable script when a
// @post action requested one. It returns true (response written) only when there
// is a redirect AND it is safe AND a session nonce is available to admit the
// script under the strict CSP; otherwise it returns false and the caller falls
// back to the normal element-patch response. The script carries the session's
// CSP nonce via the datastar-script-attributes header, which the bundle copies
// onto the <script> it creates — so the document's CSP accepts it.
func writeRedirectScript(w http.ResponseWriter, req *http.Request, sessions *sessionManager, target string) bool {
	if target == "" || !safeRedirectURL(target) {
		return false
	}
	nonce := sessions.cspNonce(req)
	if nonce == "" {
		// No session ⇒ no nonce the document will admit; a script would be
		// blocked by CSP. Fall back to the element patch (no navigation).
		return false
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	attrs, _ := json.Marshal(map[string]string{"nonce": nonce})
	w.Header().Set("datastar-script-attributes", string(attrs))
	// target is JSON-encoded into the JS string literal: json.Marshal escapes
	// quotes/backslashes/controls, closing the breakout/XSS vector for any URL
	// that passed safeRedirectURL.
	js, _ := json.Marshal(target)
	w.Write([]byte("location.assign(" + string(js) + ")"))
	return true
}

// safeRedirectURL reports whether url is safe to navigate to client-side: an
// http/https absolute URL or a same-origin relative path. A scheme like
// javascript:/data:/vbscript: or a protocol-relative "//" prefix is rejected —
// the open-redirect / XSS defense, since Redirect interpolates the URL into a
// script. Browsers strip leading control chars before resolving the scheme, so
// trim them first to treat " javascript:" identically.
func safeRedirectURL(url string) bool {
	trimmed := strings.TrimLeftFunc(url, func(r rune) bool { return r <= ' ' })
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, `\\`) {
		return false
	}
	if i := strings.IndexAny(trimmed, ":/?#"); i >= 0 && trimmed[i] == ':' {
		scheme := strings.ToLower(trimmed[:i])
		return scheme == "http" || scheme == "https"
	}
	return true // a relative path (no scheme before the first /?#)
}

// formSlot registers a native-form handler and returns its positional id.
func (c *Ctx) formSlot(fn func(*Ctx)) string {
	idx := len(c.forms)
	c.forms = append(c.forms, fn)
	return strconv.Itoa(idx)
}

// OnClick wires a click to a POST action. fn is a named method value (e.g.
// c.Inc) — pointer-bound to the via-owned instance, so no '&' at the call site.
func OnClick(fn func(*Ctx)) h.Attr { return onEvent("click", fn) }

// OnSubmit wires a form submit to a POST action. Datastar auto-prevents the
// form's default submit, so no prevent modifier is needed.
func OnSubmit(fn func(*Ctx)) h.Attr { return onEvent("submit", fn) }

// OnInput wires an input event (fires on every keystroke) to a POST action.
func OnInput(fn func(*Ctx)) h.Attr { return onEvent("input", fn) }

// OnChange wires a change event (fires on commit/blur) to a POST action.
func OnChange(fn func(*Ctx)) h.Attr { return onEvent("change", fn) }

// onEvent emits the Datastar event binding for a named method value. At render
// it claims a positional action id and writes data-on:<event>="@post('/_via/a/N')".
func onEvent(event string, fn func(*Ctx)) h.Attr {
	return h.DynAttr(func(r *h.Renderer) {
		b := r.Binder()
		ctx := ctxOf(b)
		// The action table stores a func(); it closes over the live ctx so
		// dispatch runs fn against the request Ctx.
		idx := b.ActionSlot(func() {
			if ctx != nil {
				fn(ctx)
			}
		})
		writeActionAttr(r, ctx, event, idx, "")
	})
}

// OnClickArg wires a click to an action that carries a value — the row's own
// datum rides with the click (a query arg), so the handler receives it as a
// typed parameter and acts on THAT item regardless of its render position. fn is
// a named method value (e.g. l.Delete); arg is plain data (e.g. todo.ID), not an
// identifier string. Use it for per-row actions in a list. No '&', no closure.
func OnClickArg[T any](fn func(*Ctx, T), arg T) h.Attr { return onEventArg("click", fn, arg) }

// onEventArg is onEvent for a value-carrying action: it JSON-encodes arg into the
// action's query (?a=…) so the client posts the row's datum, and the dispatched
// slot decodes it from the request and hands it to fn. Identity rides with the
// click, so a renumbered list can't misroute.
func onEventArg[T any](event string, fn func(*Ctx, T), arg T) h.Attr {
	return h.DynAttr(func(r *h.Renderer) {
		b := r.Binder()
		ctx := ctxOf(b)
		idx := b.ActionSlot(func() {
			if ctx == nil || ctx.req == nil {
				return
			}
			var v T
			if raw := ctx.req.URL.Query().Get("a"); raw != "" {
				_ = json.Unmarshal([]byte(raw), &v)
			}
			fn(ctx, v)
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
// parsed as a nonexistent plugin and silently dropped. On a live island the POST
// routes to THIS connection's instance, so it echoes the tab id (the _viatab
// local signal the SSE set) as the X-Via-Tab header; the island id scopes which
// island; a stateless page omits both.
func writeActionAttr(r *h.Renderer, ctx *Ctx, event, idx, query string) {
	base := ""
	if ctx != nil {
		base = ctx.base // mount prefix: a page at /profile posts to /profile/_via/a/{n}
	}
	path := base + "/_via/a/" + idx
	opts := ""
	switch {
	case ctx != nil && ctx.isIsland:
		path = base + "/_via/a/" + strconv.Itoa(ctx.islandIdx) + "/" + idx
		if ctx.island {
			opts = ",{headers:{'X-Via-Tab':$_viatab}}"
		}
	case ctx != nil && ctx.island:
		opts = ",{headers:{'X-Via-Tab':$_viatab}}"
	}
	r.WriteString(` data-on:` + event + `="@post('` + path + query + `'` + opts + `)"`)
}

// Register builds an http.Handler serving the root component. root is taken by
// value; per request via copies it into an addressable local and operates on
// the pointer, so pointer-receiver methods and handles work without '&' at the
// call site.
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
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return nil, false
	}
	return in, true
}

// renderRoot renders v into the morph target <div id="root" …>…</div> and
// returns the bind Ctx (slots/actions assigned this pass) plus the bytes. Used
// for the initial page body and for element-patch responses. in hydrates client
// signals during the render; pass nil for no hydration (e.g. the post-action
// response render, which must reflect mutated server state, not request echoes).
// renderRoot renders v into the #root morph target. declareSignals controls the
// page-level data-signals attribute: the GET first paint declares the signals so
// the client store is seeded, but a LIVE SSE push omits it — re-declaring on
// every push would re-merge (clobber) a client signal the user is editing (their
// half-typed message vanishing when someone else's message arrives). Deliberate
// server-driven signal changes ride an explicit signal-patch instead.
func renderRoot(v viewer, in map[string]json.RawMessage, island, declareSignals bool) (*Ctx, []byte) {
	return renderRootBase(v, in, island, declareSignals, "")
}

// renderRootBase is renderRoot with an explicit action base path — the router
// mounts a page under /path, so its actions must post to /path/_via/a/{n}, not
// the root /_via/a/{n}. base is "" for the single-page Register.
func renderRootBase(v viewer, in map[string]json.RawMessage, island, declareSignals bool, base string) (*Ctx, []byte) {
	ctx := newCtx(in)
	ctx.island = island
	ctx.declare = declareSignals // embedded islands declare their own signals only on a declaring render
	ctx.base = base
	rr := h.NewRenderer(binderCtx{ctx})
	rr.Render(v.View())
	var b bytes.Buffer
	b.WriteString(`<div id="root"`)
	if declareSignals {
		writeSignalsAttr(&b, ctx.order, ctx.initial)
	}
	b.WriteString(`>`)
	b.Write(rr.Bytes())
	b.WriteString(`</div>`)
	return ctx, b.Bytes()
}

// Register builds an http.Handler serving the root composition. root is taken
// by value; per request via copies it into an addressable local and operates on
// the pointer (PT), so pointer-receiver methods and handles work without '&' at
// the call site. The PT constraint makes a missing or mistyped View() a
// compile error rather than a first-request 500 — Register(Counter{}) still
// infers T=Counter, PT=*Counter with zero type arguments.
func Register[T any, PT interface {
	*T
	viewer
}](root T, opts ...Option) http.Handler {
	cfg := newConfig(opts)
	reg := newRegistry()
	maxLive := cfg.maxSSEConn
	if maxLive <= 0 {
		maxLive = defaultMaxSSEConn
	}
	var liveCount atomic.Int64 // concurrent live SSE streams, capped at maxLive
	sessions := newSessionManager(cfg)
	mux := http.NewServeMux()

	// A composition that implements OnConnect is itself a live island (the legacy
	// single-island page). A composition that does NOT, but whose View embeds
	// Island[C] children that implement OnConnect, is a multiplex parent — its
	// live children share one stream. rootLive is the cheap interface assertion;
	// whether a multiplex parent has live islands is decided at GET/connect by
	// inspecting the REAL rendered instance (never the zero probe, whose injected
	// deps would be nil and panic), so detection stays reflection-free and safe.
	var probe T
	_, rootLive := any(PT(&probe)).(Live)

	mux.HandleFunc("GET /_via/datastar.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(datastarJS)
	})

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, req *http.Request) {
		inst := root
		runOnInit(PT(&inst), w, req, sessions, nil) // per-request hook before the ctx-free View
		// Render the real instance (deps injected) — the parent is the island only
		// when it is itself live; multiplex children carry their own island flag.
		ctx, body := renderRoot(PT(&inst), nil, rootLive, true)
		// The page is live (and bootstraps the stream) when the root is a live
		// island or its View embedded any live island.
		hasLive := rootLive || anyLiveIsland(ctx)
		// Pre-declare the _viatab local signal so $_viatab is always defined: the
		// SSE patch-signals frame fills it with the real tab id; a click before
		// the stream connects sends an empty id and gets a graceful 410.
		bodyOpen := "</head><body>"
		if hasLive {
			bodyOpen = `</head><body data-init="@post('/_via/sse')" data-signals='{"_viatab":""}'>`
		}
		nonce := pageNonce(req, sessions) // session-scoped when a session exists, so a @post Redirect's script matches
		writeSecurityHeaders(w, nonce)
		w.Write([]byte("<!doctype html><html><head><meta charset=\"utf-8\">" +
			"<script type=\"module\" nonce=\"" + nonce + "\" src=\"/_via/datastar.js\"></script>" +
			reconnectScript(hasLive, nonce) +
			bodyOpen))
		w.Write(body)
		w.Write([]byte("</body></html>"))
	})

	{
		mux.HandleFunc("POST /_via/sse", func(w http.ResponseWriter, req *http.Request) {
			// Origin floor first: the stream opens a long-lived island goroutine +
			// timers and renders the app's HTML, so reject anything that can't prove
			// a same-origin (or explicitly trusted) source before allocating it.
			if !originAllowed(req, cfg) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
			// The connect is a POST so it can carry the page's signals as a body
			// (capped + decoded); the island hydrates from them. Multiplexing reads
			// per-island state out of this on connect.
			connectSig, ok := decodeActionBody(w, req)
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
			if liveCount.Add(1) > int64(maxLive) {
				liveCount.Add(-1)
				http.Error(w, "stream capacity reached", http.StatusServiceUnavailable)
				return
			}
			defer liveCount.Add(-1)
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("via: live stream panic: %v\n%s", rec, debug.Stack())
				}
			}()
			inst := root
			pv := PT(&inst)
			// A half-open peer never cancels req.Context(); a failed frame write
			// is the only signal it's gone. Derive a cancelable context so a write
			// failure (or the per-frame deadline) tears the island(s) down here.
			streamCtx, cancel := context.WithCancel(req.Context())
			defer cancel()
			stream := &sseStream{
				w:       w,
				rc:      http.NewResponseController(w),
				timeout: cfg.sseWriteTimeout,
				cancel:  cancel,
			}
			keepalive := func() { stream.frame(writeKeepaliveFrame) }
			interval := cfg.sseHeartbeat
			if interval <= 0 {
				interval = defaultHeartbeat
			}
			id := genCSPNonce() // per-connection tab id (echoed as X-Via-Tab on actions)
			pulse := make(chan func())

			// Establish the live unit(s) and run each OnConnect once, BEFORE the
			// stream headers flush (so OnConnect can still set the session cookie).
			// Each unit's push closure re-renders only ITS container.
			var units []*Ctx
			disposeAll := func() {
				for _, u := range units {
					for _, d := range u.disposers {
						d()
					}
				}
			}
			if rootLive {
				// Legacy single island: the root composition is the island, patched
				// at #root.
				island := newCtx(connectSig)
				island.req = req
				island.sessions = sessions
				island.sessW = w
				island.push = func() {
					_, body := renderRoot(pv, nil, true, false) // push omits data-signals
					stream.frame(func(w io.Writer) { writePatchFrame(w, body) })
				}
				lv, _ := any(pv).(Live)
				if err := lv.OnConnect(island); err != nil {
					for _, d := range island.disposers {
						d()
					}
					connectError(w, err)
					return
				}
				units = append(units, island)
				reg.put(id, &liveConn{
					inst:        pv,
					pulse:       pulse,
					done:        streamCtx.Done(),
					push:        island.push,
					pushSignals: func(j string) { stream.frame(func(w io.Writer) { writeSignalsFrame(w, j) }) },
				})
				defer reg.del(id)
			} else {
				// Multiplex: each embedded Island[C] that implements OnConnect is its
				// own live unit, sharing this one stream/goroutine and patched at its
				// own #via-i{n}.
				bind, _ := renderRoot(pv, connectSig, true, false) // discovery render
				for i, isl := range bind.islands {
					lv, ok := isl.islandV.(Live)
					if !ok {
						continue
					}
					uctx := newCtx(connectSig)
					uctx.req = req
					uctx.sessions = sessions
					uctx.sessW = w
					uctx.isIsland = true
					uctx.islandIdx = i
					idx, v := i, isl.islandV
					uctx.islandV = v // the action handler re-binds this island's actions
					uctx.push = func() {
						stream.frame(func(w io.Writer) { writePatchFrame(w, renderIslandPatch(idx, v)) })
					}
					if err := lv.OnConnect(uctx); err != nil {
						disposeAll()
						for _, d := range uctx.disposers {
							d()
						}
						connectError(w, err)
						return
					}
					units = append(units, uctx)
				}
			}

			// No live units: this app has no live content (a stateless page POSTing
			// the stream endpoint), so there is nothing to stream.
			if len(units) == 0 {
				http.Error(w, "no live stream", http.StatusNotFound)
				return
			}

			// Register a multiplex connection's islands so a live action POST
			// (/_via/a/{island}/{n} + X-Via-Tab) routes to the right island on this
			// connection's goroutine. The legacy single-island case registered itself
			// above; here inst/push stay nil and the per-island units carry them.
			if !rootLive {
				islands := make(map[int]*Ctx, len(units))
				for _, u := range units {
					islands[u.islandIdx] = u
				}
				reg.put(id, &liveConn{
					pulse:       pulse,
					done:        streamCtx.Done(),
					pushSignals: func(j string) { stream.frame(func(w io.Writer) { writeSignalsFrame(w, j) }) },
					islands:     islands,
				})
				defer reg.del(id)
			}

			writeSSEHeaders(w)
			w.WriteHeader(http.StatusOK)
			stream.frame(func(w io.Writer) { writeSignalsFrame(w, `{"_viatab":"`+id+`"}`) })

			runLiveStream(streamCtx, units, pulse, keepalive, interval)
		})
	}

	mux.HandleFunc("POST /_via/a/{n}", func(w http.ResponseWriter, req *http.Request) {
		// A View or action that panics must not crash the server or wedge the
		// connection: contain it as a 500. The action and the response render
		// both run before any bytes are written, so this never double-writes.
		defer func() {
			if rec := recover(); rec != nil {
				recoverToHTTP(w, rec, "action")
			}
		}()

		// Origin floor: this endpoint changes server state, so reject anything
		// that is not provably same-origin (or explicitly trusted) before doing
		// any work. See originAllowed for the precedence.
		if !originAllowed(req, cfg) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}

		in, ok := decodeActionBody(w, req)
		if !ok {
			return
		}

		// Live island: route the action to THIS connection's island goroutine,
		// found by the X-Via-Tab header (the _viatab the SSE handed it). The
		// action runs against the connection's own instance — mutating its State —
		// and the SSE push ships the patch, so the POST just acks 204
		// (fire-and-forget: the action runs async on the island goroutine; the
		// result arrives over the SSE, not on this response). The bind-shape guard
		// does not apply here: the island re-render is the authority, not the
		// request echo. An unknown/closed tab is 410 so a stale client
		// re-bootstraps rather than mutating a throwaway.
		//
		// Contract: a live island's View must render a render-stable action set
		// (action ids are positional). A gone/out-of-range index simply no-ops on
		// the island; the next SSE push re-syncs the client either way.
		if rootLive {
			lc, ok := reg.get(req.Header.Get("X-Via-Tab"))
			if !ok {
				http.Error(w, "no live connection for this tab", http.StatusGone)
				return
			}
			n, err := strconv.Atoi(req.PathValue("n"))
			if err != nil {
				http.Error(w, "no such action", http.StatusGone)
				return
			}
			dispatched := lc.Dispatch(func() {
				bind, _ := renderRoot(lc.inst, in, true, false)
				bind.req = req // the action POST that triggered this live action
				// Store-only: a live action runs after its 204, so it can read/write an
				// already-established session but cannot issue a cookie (sessW stays nil).
				bind.sessions = sessions
				if n >= 0 && n < len(bind.actions) {
					bind.actions[n]()
				}
				// A deliberate server-driven signal change (e.g. clearing the
				// composer) reaches the client as a signal-patch — the element
				// push omits data-signals, so morphs never clobber what the user
				// is typing.
				if len(bind.dirty) > 0 {
					if raw, err := json.Marshal(bind.dirty); err == nil {
						lc.pushSignals(string(raw))
					}
				}
				lc.push() // re-render the island and frame the element-patch
			})
			if !dispatched {
				http.Error(w, "live connection closed", http.StatusGone)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Stateless action: the post-decode core is shared with the router's
		// mounted pages (dispatchStateless), so the two can't drift. Base is "" —
		// the single-page root posts to /_via/a/{n}.
		dispatchStateless[T, PT](w, req, root, cfg, sessions, "", nil, in)
	})

	// Embedded-island action: /_via/a/{island}/{n} routes to one island's action
	// table and patches that island's #via-i{n} container, leaving its siblings
	// untouched. The parent is re-rendered to discover the islands positionally
	// (the same bind-pass model as the flat path), then the named island's action
	// runs against its child instance.
	mux.HandleFunc("POST /_via/a/{island}/{n}", func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				recoverToHTTP(w, rec, "island action")
			}
		}()
		if !originAllowed(req, cfg) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		in, ok := decodeActionBody(w, req)
		if !ok {
			return
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

		// Live mux island: the X-Via-Tab header routes to THIS connection's island
		// goroutine, where the action runs against the connection's own instance and
		// the result is pushed over its SSE — so the POST just acks 204. An
		// unknown/closed tab is 410 so a stale client re-bootstraps.
		if lc, ok := reg.get(req.Header.Get("X-Via-Tab")); ok && lc.islands != nil {
			isl, ok := lc.islands[island]
			if !ok {
				http.Error(w, "no such island", http.StatusGone)
				return
			}
			dispatched := lc.Dispatch(func() {
				bind := bindIsland(isl.islandIdx, isl.islandV, in)
				bind.req = req
				bind.sessions = sessions // store-only: a live action can't set a cookie
				if n >= 0 && n < len(bind.actions) {
					bind.actions[n]()
				}
				if len(bind.dirty) > 0 {
					if raw, err := json.Marshal(bind.dirty); err == nil {
						lc.pushSignals(string(raw))
					}
				}
				isl.push() // re-render this island and frame the element-patch
			})
			if !dispatched {
				http.Error(w, "live connection closed", http.StatusGone)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Stateless embedded island: discover positionally and re-render in place.
		inst := root
		bind, _ := renderRoot(PT(&inst), in, false, true) // discovery render → bind.islands
		if island < 0 || island >= len(bind.islands) {
			http.Error(w, "no such island", http.StatusGone)
			return
		}
		isl := bind.islands[island]
		// A LIVE island's action only routes through the tab handshake above; if we
		// reached the stateless path the tab was missing/stale, so fail closed (410)
		// rather than mutating a throwaway. Only genuinely stateless islands
		// re-render in place here.
		if _, live := isl.islandV.(Live); live {
			http.Error(w, "no live connection for this tab", http.StatusGone)
			return
		}
		if n < 0 || n >= len(isl.actions) {
			http.Error(w, "no such action", http.StatusGone)
			return
		}

		before := isl.rendered
		isl.req = req // so a value-carrying action (OnClickArg) can read its arg from the request
		isl.actions[n]()
		after := renderIsland(island, isl.islandV)
		if bytes.Equal(before, after) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Element-patch scoped to this island's container, so the morph replaces
		// only #via-i{island} and never disturbs a sibling island.
		writeSecurityHeaders(w, genCSPNonce())
		w.Write([]byte(`<div id="via-i` + strconv.Itoa(island) + `">`))
		w.Write(after)
		w.Write([]byte(`</div>`))
	})

	return mux
}
