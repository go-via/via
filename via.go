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
	"net/http"
	"runtime/debug"
	"strconv"

	"github.com/go-via/via/v2/h"
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

// SignalName allocates the next first-use signal name ("s0","s1",…). A handle
// calls it once and caches the result, so a signal's identity is the handle,
// not its render position. h.Binder.
func (c *Ctx) SignalName() string {
	name := "s" + strconv.Itoa(c.nextSig)
	c.nextSig++
	return name
}

// DeclareSignal records that slot participates in this render with the given
// initial value, for the page-level data-signals declaration. Idempotent within
// a render: the first declaration fixes the order, later ones (e.g. a Bind and a
// Display of the same signal) only refresh the value. h.Binder.
func (c *Ctx) DeclareSignal(slot string, initial any) {
	if _, seen := c.initial[slot]; !seen {
		c.order = append(c.order, slot)
	}
	c.initial[slot] = initial
}

// SignalInit returns the hydrated raw value for a slot, if the request carried
// one. The bool reports presence. h.Binder.
func (c *Ctx) SignalInit(slot string) (any, bool) {
	if c.inSignals == nil {
		return nil, false
	}
	raw, ok := c.inSignals[slot]
	if !ok {
		return nil, false
	}
	return raw, true
}

// ActionSlot registers a handler and returns its positional id "0","1",….
// h.Binder.
func (c *Ctx) ActionSlot(fn func()) string {
	idx := len(c.actions)
	c.actions = append(c.actions, fn)
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
		ctx, _ := b.(*Ctx)
		// The action table stores a func(); it closes over the live ctx so
		// dispatch runs fn against the request Ctx.
		idx := b.ActionSlot(func() {
			if ctx != nil {
				fn(ctx)
			}
		})
		// Write the attribute raw, not via h.Data: the value is a Datastar
		// expression whose single-quotes must survive verbatim (escaping them to
		// &#39; breaks the @post() call). The value is fully via-generated (fixed
		// template + the via-controlled event name + a numeric id), so no user
		// input reaches it and there is no injection surface.
		//
		// Datastar v1's colon syntax (data-on:<event>). The old dash form is
		// parsed as a nonexistent plugin and silently dropped — dead in the
		// browser while every server-side render test passes.
		//
		// On a live island the POST must route to THIS connection's instance, so
		// the action echoes the tab id (the _viatab local signal set by the SSE)
		// as the X-Via-Tab header. Stateless pages have no tab and omit it.
		if ctx != nil && ctx.island {
			r.WriteString(` data-on:` + event + `="@post('/_via/a/` + idx + `',{headers:{'X-Via-Tab':$_viatab}})"`)
		} else {
			r.WriteString(` data-on:` + event + `="@post('/_via/a/` + idx + `')"`)
		}
	})
}

// Register builds an http.Handler serving the root component. root is taken by
// value; per request via copies it into an addressable local and operates on
// the pointer, so pointer-receiver methods and handles work without '&' at the
// call site.
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
	ctx := newCtx(in)
	ctx.island = island
	rr := h.NewRenderer(ctx)
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
	mux := http.NewServeMux()

	// A composition that implements OnConnect is a live island: the page carries
	// a one-time SSE bootstrap and via serves the per-tab stream. Detected by
	// interface assertion, never reflection.
	var probe T
	_, isLive := any(PT(&probe)).(Live)
	bodyOpen := "</head><body>"
	if isLive {
		// Pre-declare the _viatab local signal so $_viatab is always defined: the
		// SSE patch-signals frame fills it with the real tab id; a click before
		// the stream connects sends an empty id and gets a graceful 410.
		bodyOpen = `</head><body data-init="@get('/_via/sse')" data-signals='{"_viatab":""}'>`
	}

	mux.HandleFunc("GET /_via/datastar.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(datastarJS)
	})

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		inst := root
		_, body := renderRoot(PT(&inst), nil, isLive, true)
		nonce := genCSPNonce()
		writeSecurityHeaders(w, nonce)
		w.Write([]byte("<!doctype html><html><head><meta charset=\"utf-8\">" +
			"<script type=\"module\" nonce=\"" + nonce + "\" src=\"/_via/datastar.js\"></script>" +
			themeStyle(cfg.theme, nonce) +
			reconnectScript(isLive && !cfg.noReconnect, nonce) +
			bodyOpen))
		w.Write(body)
		w.Write([]byte("</body></html>"))
	})

	if isLive {
		mux.HandleFunc("GET /_via/sse", func(w http.ResponseWriter, req *http.Request) {
			if _, ok := w.(http.Flusher); !ok {
				http.Error(w, "streaming unsupported", http.StatusInternalServerError)
				return
			}
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("via: live stream panic: %v\n%s", rec, debug.Stack())
				}
			}()
			inst := root
			pv := PT(&inst)
			live, _ := any(pv).(Live)
			island := newCtx(nil)
			island.req = req // the connect request; OnConnect + its ticks/subs read it
			if err := live.OnConnect(island); err != nil {
				// OnConnect may have registered disposers (a Subscribe paired with
				// OnDispose(sub.Stop)) before failing — run them so the
				// subscription is not orphaned in its Topic.
				for _, d := range island.disposers {
					d()
				}
				http.Error(w, "connect failed", http.StatusInternalServerError)
				return
			}
			// A half-open peer never cancels req.Context(); a failed frame write
			// is the only signal it's gone. Derive a cancelable context so a write
			// failure (or the per-frame deadline) tears the island down here.
			streamCtx, cancel := context.WithCancel(req.Context())
			defer cancel()
			stream := &sseStream{
				w:       w,
				rc:      http.NewResponseController(w),
				timeout: cfg.sseWriteTimeout,
				cancel:  cancel,
			}

			writeSSEHeaders(w)
			w.WriteHeader(http.StatusOK)

			// Register this connection so a POST action routes to ITS island, and
			// hand the client its tab id as the _viatab local signal (echoed back
			// on the action POST as the X-Via-Tab header). The id is an unguessable
			// 128-bit token: combined with the origin floor it is a sound CSRF
			// second factor. It is NOT secret from same-origin scripts (it lives in
			// the page's signal store) — the origin floor, not secrecy, blocks
			// cross-origin use.
			id := genCSPNonce()
			pulse := make(chan func(*Ctx))
			reg.put(id, &liveConn{
				inst:  pv,
				pulse: pulse,
				// streamCtx, not req.Context(): a write-error teardown cancels it, so
				// a POST racing a just-dropped tab gets a clean 410 instead of blocking.
				done: streamCtx.Done(),
				// Written on the island goroutine (the dispatched fn runs there),
				// the same goroutine as the element push — so the two never race.
				pushSignals: func(j string) { stream.frame(func(w io.Writer) { writeSignalsFrame(w, j) }) },
			})
			defer reg.del(id)
			stream.frame(func(w io.Writer) { writeSignalsFrame(w, `{"_viatab":"`+id+`"}`) })

			interval := cfg.sseHeartbeat
			if interval <= 0 {
				interval = defaultHeartbeat
			}
			runLiveStream(streamCtx, island, pulse, func() {
				_, body := renderRoot(pv, nil, true, false) // push omits data-signals
				stream.frame(func(w io.Writer) { writePatchFrame(w, body) })
			}, func() {
				stream.frame(writeKeepaliveFrame)
			}, interval)
		})
	}

	mux.HandleFunc("POST /_via/a/{n}", func(w http.ResponseWriter, req *http.Request) {
		// A View or action that panics must not crash the server or wedge the
		// connection: contain it as a 500. The action and the response render
		// both run before any bytes are written, so this never double-writes.
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("via: action handler panic: %v\n%s", rec, debug.Stack())
				http.Error(w, "action failed", http.StatusInternalServerError)
			}
		}()

		// Origin floor: this endpoint changes server state, so reject anything
		// that is not provably same-origin (or explicitly trusted) before doing
		// any work. See originAllowed for the precedence.
		if !originAllowed(req, cfg) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}

		// Decode the client signals under a body cap. An empty body is the
		// common stateless-action case (no signals); a malformed or oversize
		// body fails loudly rather than silently binding nothing.
		in := map[string]json.RawMessage{}
		if req.Body != nil {
			dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxActionBody))
			if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "malformed request body", http.StatusBadRequest)
				return
			}
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
		if isLive {
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
			dispatched := lc.Dispatch(func(*Ctx) {
				bind, _ := renderRoot(lc.inst, in, true, false)
				bind.req = req // the action POST that triggered this live action
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
			})
			if !dispatched {
				http.Error(w, "live connection closed", http.StatusGone)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		inst := root

		// Bind pass: rendering assigns positional slot/action ids, hydrates any
		// client signals from the request, and fills the action table. The bytes
		// are the pre-action view (the client's current DOM, reconstructed from
		// the request) — kept so we can tell whether the action changed anything.
		bind, before := renderRoot(PT(&inst), in, false, true)

		// Render-shape guard. Binding is positional, so a dispatched action index
		// is only meaningful if this bind pass produced the SAME signal-slot set
		// the client was served and echoed back. A divergence means the View
		// branched on a value and the indices no longer line up; reject with 410
		// so the client re-bootstraps instead of being silently misrouted.
		if !shapeMatches(bind.order, in) {
			http.Error(w, "render-shape mismatch", http.StatusGone)
			return
		}

		n, err := strconv.Atoi(req.PathValue("n"))
		if err != nil || n < 0 || n >= len(bind.actions) {
			http.Error(w, "no such action", http.StatusGone)
			return
		}
		bind.req = req // the action POST that triggered this action
		bind.actions[n]()

		// Re-render the now-mutated instance (no re-hydration, so it reflects
		// post-action server state). If it is byte-identical to the pre-action
		// render, the action changed nothing the View reads — return 204 rather
		// than re-send an identical #root the browser would morph onto itself.
		// Comparing the rendered output (not via-handle writes) is the sound
		// signal: it catches a change through any path — an injected dependency,
		// a Signal, a State — and can't be fooled by mutations via never sees.
		_, after := renderRoot(PT(&inst), nil, false, true)
		if bytes.Equal(before, after) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Element-patch: text/html that Datastar morphs into the live DOM by the
		// #root id. It is morphed into the live document, so it ships the same
		// hardening headers as the page.
		writeSecurityHeaders(w, genCSPNonce())
		w.Write(after)
	})

	return mux
}
