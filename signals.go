package via

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"unsafe"

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
// only restricts which slots are declared. nil declares every slot — the GET
// first paint, seeding the whole client store. A non-nil only declares just the
// slots it names: that is how a plain action patch ships the signals it
// wrote without overwriting the ones it did not, so a value the user is mid-edit
// survives the morph. If the restriction leaves nothing, no attribute is written.
func writeSignalsAttr(buf *bytes.Buffer, order []string, initial, only map[string]any, seen map[string]bool) {
	sig := make(map[string]any, len(order))
	for _, slot := range order {
		if only != nil {
			_, dirty := only[slot]
			// A slot the pre-action render did not carry is a control that has
			// just appeared (a branch opened). The client store has no value
			// for it — or, worse, a stale one from whatever occupied the slot
			// before — so it is seeded here even though the action never wrote
			// it. seen nil means "no pre-action render to compare against".
			if !dirty && (seen == nil || seen[slot]) {
				continue
			}
		}
		sig[slot] = initial[slot]
	}
	if len(sig) == 0 && only != nil {
		return
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
	slotID // MUST stay the first field: prebindSignals stamps the wire name through a pointer add at the field's offset
	val    T
	bound  *Ctx // stamped at bind; the pass whose dirty map ships the patch
	warned bool // the never-rendered Set warning fired (once per signal)
}

// Ref returns the signal's Datastar expression — "$count" for a field named
// Count, "$chat__draft" for a Draft inside an embedded Chat (the DOUBLE
// underscore marks the embed scope boundary; a plain nested struct joins with
// a single one) — for hand-written
// Datastar attributes the typed API does not cover:
//
//	h.Div(h.Data("show", p.Open.Ref()), ...)
//
// Valid for a signal held as a struct field of the composition (the normal
// case): every such signal is named before the View runs, so Ref reads the
// same name wherever in the tree it is called. A signal reached only through a
// pointer or slice field has no field name and is named in render order at its
// first Bind/Display, so Ref on one is empty until then — Bind or Display it
// first, or hold it as a plain field.
func (s *Signal[T]) Ref() string { return "$" + s.slot }

// Get returns the current value.
func (s *Signal[T]) Get() T { return s.val }

// Set assigns the value and records it as dirty, which is what carries the change
// to the client. There are two channels and they follow one rule: only the
// signals an action actually wrote are ever declared, so a signal the user is
// mid-edit is never overwritten behind them. A live action pushes a
// patch-signals frame and its element patch carries no declaration at all; a
// plain action has no second frame, so its element patch carries a
// data-signals attribute restricted to the dirty slots.
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
	if s.bound != nil {
		s.bound.warnAddressable(unsafe.Pointer(s))
	}
	// Re-mint when the scope moved: via.Embed copies the child by value, so a
	// signal the parent's View already bound arrives in the embed carrying an
	// unprefixed root slot, which would collide in the page's one signal store.
	if s.slot == "" || (s.bound != nil && !s.bound.slotInScope(s.scope)) {
		if s.bound != nil {
			s.slot, s.scope = s.bound.signalSlot(unsafe.Pointer(s)), s.bound.scopePrefix()
		} else {
			s.slot, s.scope = b.SignalName(), ""
		}
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
	// A live unit keeps this table from its last render rather than
	// re-rendering before an action, so the hydration a fresh render would
	// have done from SignalInit above must also be reachable by slot name.
	b.Hydrator(s.slot, func(raw json.RawMessage) {
		var v T
		if json.Unmarshal(raw, &v) == nil {
			s.val = v
		}
	})
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
	return hcore.Dyn(func(r *hcore.Renderer) { r.WriteEscaped(fmt.Sprint(v)) })
}
