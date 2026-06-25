// Package via is a server-driven reactive UI toolkit built on the h DSL and the
// Datastar client. Slice 1 is deliberately narrow: a stateless,
// request/response signal-counter. No SSE, islands, Stream, State or Local yet.
//
// Hard guarantees (the point of the design): no '&' at any user call site, no
// user-facing identifier strings, no reflection, no closures in the public API
// surface, no any in element/child signatures. stdlib only.
package via

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-via/via/h"
)

// datastarJS is the vendored Datastar client, served at /_via/datastar.js.
//
//go:embed datastar.js
var datastarJS []byte

// viewer is the (pointer) contract a root must satisfy: a pure, ctx-free View.
type viewer interface{ View() h.H }

// Ctx is the per-request binder. It assigns positional slot/action ids during a
// render pass, hydrates signals from the request, and records dirty signals for
// the response patch. It implements h.Binder.
type Ctx struct {
	inSignals map[string]json.RawMessage // hydrated from the request
	nextSig   int                        // next signal slot index
	order     []string                   // slots in assignment order
	initial   map[string]any             // per-slot value seen at render time
	actions   []func()                   // positional action table
	dirty     map[string]any             // signals changed this request
}

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

// SignalSlot returns the next "s0","s1",… and advances. h.Binder.
func (c *Ctx) SignalSlot() string {
	slot := "s" + strconv.Itoa(c.nextSig)
	c.nextSig++
	c.order = append(c.order, slot)
	return slot
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

// markDirty records a signal value changed this request; it goes into the
// response patch.
func (c *Ctx) markDirty(slot string, v any) { c.dirty[slot] = v }

// setInitial records the value a signal carried at render time, for the
// page-level data-signals declaration.
func (c *Ctx) setInitial(slot string, v any) { c.initial[slot] = v }

// Signal is a client-resident value that round-trips per request and renders as
// a Datastar text-bound span. T must be JSON-round-trippable; slice 1 exercises
// int and string.
type Signal[T any] struct {
	slot string // assigned on first render this request
	val  T
}

// Get returns the current value.
func (s *Signal[T]) Get() T { return s.val }

// Set assigns the value and marks the signal dirty for the response patch.
func (s *Signal[T]) Set(ctx *Ctx, v T) {
	s.val = v
	ctx.markDirty(s.slot, v)
}

// Node returns an h.H bound to this signal. Pointer receiver, so
// c.Count.Node() auto-addresses — no '&' at the call site. The returned node
// claims the slot, hydrates the value, and emits the reactive span at render.
//
// TODO(bare-handle): once skeleton-caching lands, expose the bare h.H1(c.Count)
// form without the .Node() shim.
func (s *Signal[T]) Node() h.H {
	return h.Dyn(func(r *h.Renderer) {
		s.slot = r.Binder().SignalSlot()
		if raw, ok := r.Binder().SignalInit(s.slot); ok {
			if rm, isRaw := raw.(json.RawMessage); isRaw {
				var v T
				if err := json.Unmarshal(rm, &v); err == nil {
					s.val = v
				}
			}
		}
		// Record the value for the page-level data-signals declaration.
		if b, ok := r.Binder().(*Ctx); ok {
			b.setInitial(s.slot, s.val)
		}
		// Emit <span data-text="$<slot>">escaped current value</span>.
		r.Render(h.Span(h.Data("text", "$"+s.slot), textHandle(s.val)))
	})
}

// Bind returns a data-bind="<slot>" attribute. Requires the slot to be assigned
// (i.e. the signal must have been rendered earlier this pass).
func (s *Signal[T]) Bind() h.Attr { return h.Data("bind", s.slot) }

// textHandle renders an arbitrary value as escaped text. Internal only; it uses
// any so it can serve any signal T without appearing on a public signature.
func textHandle(v any) h.H {
	return h.Dyn(func(r *h.Renderer) { r.WriteEscaped(sprint(v)) })
}

// Num is a concrete numeric signal: Signal[int] with Add. Embedding gives it
// Get/Set/Node/Bind for free.
type Num struct{ Signal[int] }

// Add increments the signal by d and marks it dirty.
func (n *Num) Add(ctx *Ctx, d int) { n.Set(ctx, n.Get()+d) }

// OnClick returns an attribute that wires a click to a POST dispatch. At render
// it claims an action id N, stores a wrapper that runs fn against the live Ctx,
// and emits data-on:click="@post('/_via/a/N')".
//
// fn is typically a method value (e.g. c.Inc) — pointer-bound to the via-owned
// instance, so no '&' at the call site.
func OnClick(fn func(*Ctx)) h.Attr {
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
		// &#39; breaks the @post() call). The value is fully via-generated
		// (fixed template + a numeric action index), so no user input reaches
		// it and there is no injection surface.
		//
		// The key uses Datastar v1's colon syntax (data-on:click). The old dash
		// form (data-on-click) is parsed by v1.0.2 as a nonexistent plugin
		// "on-click" and silently dropped — no listener attaches and the click is
		// dead in the browser, even though every server-side render test passes.
		r.WriteString(` data-on:click="@post('/_via/a/` + idx + `')"`)
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
func renderRoot(v viewer, in map[string]json.RawMessage) (*Ctx, []byte) {
	ctx := newCtx(in)
	rr := h.NewRenderer(ctx)
	rr.Render(v.View())
	var b bytes.Buffer
	b.WriteString(`<div id="root"`)
	writeSignalsAttr(&b, ctx.order, ctx.initial)
	b.WriteString(`>`)
	b.Write(rr.Bytes())
	b.WriteString(`</div>`)
	return ctx, b.Bytes()
}

func Register[T any](root T) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /_via/datastar.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write(datastarJS)
	})

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		inst := root
		v, ok := any(&inst).(viewer)
		if !ok {
			http.Error(w, "root does not implement View() h.H", http.StatusInternalServerError)
			return
		}
		_, body := renderRoot(v, nil)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!doctype html><html><head><meta charset=\"utf-8\">" +
			"<script type=\"module\" src=\"/_via/datastar.js\"></script></head><body>"))
		w.Write(body)
		w.Write([]byte("</body></html>"))
	})

	mux.HandleFunc("POST /_via/a/{n}", func(w http.ResponseWriter, req *http.Request) {
		in := map[string]json.RawMessage{}
		if req.Body != nil {
			json.NewDecoder(req.Body).Decode(&in)
		}

		inst := root
		v, ok := any(&inst).(viewer)
		if !ok {
			http.Error(w, "root does not implement View() h.H", http.StatusInternalServerError)
			return
		}

		// Bind pass: rendering assigns positional slot/action ids, hydrates any
		// client signals from the request, and fills the action table. HTML
		// discarded — we re-render after the mutation below.
		bind, _ := renderRoot(v, in)

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
		bind.actions[n]()

		// Element-patch: re-render the now-mutated instance (no re-hydration, so
		// the fragment reflects post-action server state) and return it as
		// text/html. Datastar morphs it into the live DOM by the #root id.
		_, body := renderRoot(v, nil)
		w.Header().Set("Content-Type", "text/html")
		w.Write(body)
	})

	return mux
}
