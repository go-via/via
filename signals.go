package via

import (
	"bytes"
	"encoding/json"
	"log"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// writeSignalsAttr writes the page-level Datastar signal declaration as a
// single-quoted HTML attribute: data-signals='{...}'. The signals map is
// marshaled to JSON, then escaped for the single-quoted attribute context
// before being written.
//
// json.Marshal already unicode-escapes <, > and & inside string VALUES (to
// < etc.), so the only HTML-significant character it leaves raw in its
// output is the single quote. Left verbatim, a string signal carrying an
// apostrophe would close this single-quoted attribute early and let an attacker
// graft live attributes (e.g. a data-on-* Datastar expression) onto
// <div id="root">. We therefore entity-encode the apostrophe for the
// single-quoted context. Double quotes are left intact: they are legal inside a
// single-quoted attribute, keep the JSON readable, and the browser hands the
// decoded value to Datastar either way.
func writeSignalsAttr(buf *bytes.Buffer, order []string, initial map[string]any) {
	sig := make(map[string]any, len(order))
	for _, slot := range order {
		sig[slot] = initial[slot]
	}
	raw, _ := json.Marshal(sig)

	buf.WriteString(` data-signals='`)
	for _, b := range raw {
		if b == '\'' {
			buf.WriteString("&#39;")
			continue
		}
		buf.WriteByte(b)
	}
	buf.WriteByte('\'')
}

// Signal is a client-resident value that round-trips per request and renders as
// a Datastar text-bound span. T must be JSON-round-trippable; slice 1 exercises
// int and string.
type Signal[T any] struct {
	slot   string // stable wire name, assigned lazily on first render
	val    T
	bound  *Ctx // stamped at bind; the pass whose dirty map ships the patch
	warned bool // the never-rendered Set warning fired (once per signal)
}

// Get returns the current value.
func (s *Signal[T]) Get() T { return s.val }

// Set assigns the value and records it as dirty so a live action's dispatch can
// push a signal-patch (the authoritative way to change a client signal from the
// server — a stateless action's element-patch also reflects it on re-render).
//
// Contract: the change reaches the client only for a signal the View actually
// renders (via Bind or Display) — the wire name and the request binding are
// assigned at render, so a Set on a signal the View never renders updates
// server memory but emits no patch. Bind/Display the signals an action mutates.
func (s *Signal[T]) Set(v T) {
	s.val = v
	if s.bound != nil && s.slot != "" {
		s.bound.dirty[s.slot] = v
		return
	}
	// No render has bound this signal: the write lands in server memory but no
	// patch can reach the client. Silent, that reads as "Set does nothing" —
	// warn once per signal.
	if !s.warned {
		s.warned = true
		log.Print("via: Signal.Set on a signal the View never rendered — the value updates server memory " +
			"but no patch reaches the client; Bind or Display the signal in the View")
	}
}

// bind assigns the signal's stable wire name on first render (reused
// thereafter), hydrates the value from the request if present, and declares the
// slot for this render's data-signals. Every render entry point (Display, Bind)
// calls it, so the name is the handle's identity, shared across all of them.
func (s *Signal[T]) bind(r *hcore.Renderer) {
	b := r.Binder()
	s.bound = ctxOf(b)
	if s.slot == "" {
		s.slot = b.SignalName()
	}
	if raw, ok := b.SignalInit(s.slot); ok {
		if rm, isRaw := raw.(json.RawMessage); isRaw {
			var v T
			if json.Unmarshal(rm, &v) == nil {
				s.val = v
			}
		}
	}
	b.DeclareSignal(s.slot, s.val)
}

// Display returns an h.H that renders the signal as a Datastar text-bound span.
// Pointer receiver, so c.Count.Display() auto-addresses — no '&' at the call
// site. Displaying the same signal in more than one place reuses its name, so
// they all update together.
func (s *Signal[T]) Display() h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		s.bind(r)
		r.Render(h.Span(h.Data("text", "$"+s.slot), textHandle(s.val)))
	})
}

// Bind returns a two-way data-bind="<slot>" attribute for an input. It claims
// and declares the signal's slot at render through the same path as Display, so
// the binding is non-empty and shares the signal's name regardless of source
// order or whether the signal is also Displayed.
func (s *Signal[T]) Bind() h.Attr {
	return hcore.DynAttr(func(r *hcore.Renderer) {
		s.bind(r)
		r.Render(h.Data("bind", s.slot))
	})
}

// textHandle renders an arbitrary value as escaped text. Internal only; it uses
// any so it can serve any signal T without appearing on a public signature.
func textHandle(v any) h.H {
	return hcore.Dyn(func(r *hcore.Renderer) { r.WriteEscaped(sprint(v)) })
}

// SignalClientOnly is a signal the client owns. The distinction from Signal is
// DIRECTION, not location: its wire name is underscore-prefixed, and Datastar
// never POSTs an underscore-prefixed signal, so the value can never travel back
// to the server. That asymmetry is why there is no Get and never will be —
// nothing carries the value here. The server writes it one-way with Set.
//
// Reach for it whenever the server does not need to read the value: a toggle,
// show/hide, a tab switcher, an input mirror, a character counter. On a page with
// no live island this is the only state type that reacts at all, and it does so
// with zero round-trips — bind with Bind, show with Display.
type SignalClientOnly[T any] struct {
	slot   string
	val    T
	bound  *Ctx // stamped at bind; the pass whose dirty map ships the patch
	warned bool
}

func (l *SignalClientOnly[T]) bind(r *hcore.Renderer) {
	b := r.Binder()
	l.bound = ctxOf(b)
	if l.slot == "" {
		l.slot = "_" + b.SignalName() // underscore ⇒ Datastar keeps it client-only
	}
	b.DeclareSignal(l.slot, l.val)
}

// Set writes a new value from the server — the one direction that works. Call
// it from an action to clear a search box, reset a wizard step, or drive any
// client-only value the server has a reason to change; it is an ordinary thing to
// do, not an escape hatch. The value ships as a Datastar patch-signals frame on
// the same flush that carries Signal.Set, so the browser reacts with no
// round-trip and without re-rendering any element. Works in a live island and in
// a plain action alike.
//
// It is spelled Set to match Signal and State — the asymmetry is already stated
// by the Get that is not here. Note what it does not buy you: after Set the
// server knows only what it last wrote, never what the client currently holds.
func (l *SignalClientOnly[T]) Set(v T) {
	l.val = v
	if l.bound != nil && l.bound.dirty != nil {
		l.bound.dirty[l.slot] = v
		return
	}
	// No render has bound this signal: there is no slot to patch, so the write
	// cannot reach the client. Silent, that reads as "Set does nothing".
	if !l.warned {
		l.warned = true
		log.Print("via: SignalClientOnly.Set on a signal the View never rendered — " +
			"no patch reaches the client; Bind or Display the signal in the View")
	}
}

// Display renders the client-only signal's value as a text-bound span (updates in
// the browser as the value changes, no server round-trip).
func (l *SignalClientOnly[T]) Display() h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		l.bind(r)
		r.Render(h.Span(h.Data("text", "$"+l.slot), textHandle(l.val)))
	})
}

// Bind returns a two-way data-bind attribute for an input, bound to this
// client-only signal.
func (l *SignalClientOnly[T]) Bind() h.Attr {
	return hcore.DynAttr(func(r *hcore.Renderer) {
		l.bind(r)
		r.Render(h.Data("bind", l.slot))
	})
}
