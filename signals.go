package via

import (
	"bytes"
	"encoding/json"
	"log"

	"github.com/go-via/via/h"
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
func (s *Signal[T]) bind(r *h.Renderer) {
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
	return h.Dyn(func(r *h.Renderer) {
		s.bind(r)
		r.Render(h.Span(h.Data("text", "$"+s.slot), textHandle(s.val)))
	})
}

// Bind returns a two-way data-bind="<slot>" attribute for an input. It claims
// and declares the signal's slot at render through the same path as Display, so
// the binding is non-empty and shares the signal's name regardless of source
// order or whether the signal is also Displayed.
func (s *Signal[T]) Bind() h.Attr {
	return h.DynAttr(func(r *h.Renderer) {
		s.bind(r)
		r.Render(h.Data("bind", s.slot))
	})
}

// textHandle renders an arbitrary value as escaped text. Internal only; it uses
// any so it can serve any signal T without appearing on a public signature.
func textHandle(v any) h.H {
	return h.Dyn(func(r *h.Renderer) { r.WriteEscaped(sprint(v)) })
}

// Local is a client-only signal: it lives in the browser, never round-trips to
// the server (its wire name is underscore-prefixed, which Datastar never POSTs),
// and it exposes no server Get/Set by construction. Use it for optimistic UI —
// a toggle, show/hide, an input mirror — where the server never needs the value.
// Two-way bind it with Bind() and show it with Display().
type Local[T any] struct {
	slot string
	val  T
}

func (l *Local[T]) bind(r *h.Renderer) {
	b := r.Binder()
	if l.slot == "" {
		l.slot = "_" + b.SignalName() // underscore ⇒ Datastar keeps it client-only
	}
	b.DeclareSignal(l.slot, l.val)
}

// Display renders the local signal's value as a text-bound span (updates in the
// browser as the value changes, no server round-trip).
func (l *Local[T]) Display() h.H {
	return h.Dyn(func(r *h.Renderer) {
		l.bind(r)
		r.Render(h.Span(h.Data("text", "$"+l.slot), textHandle(l.val)))
	})
}

// Bind returns a two-way data-bind attribute for an input, bound to this
// client-only signal.
func (l *Local[T]) Bind() h.Attr {
	return h.DynAttr(func(r *h.Renderer) {
		l.bind(r)
		r.Render(h.Data("bind", l.slot))
	})
}
