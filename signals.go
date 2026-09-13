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

// writeSignalsAttr writes the Datastar signal declaration as a single-quoted
// attribute: data-signals='{...}'.
//
// json.Marshal already unicode-escapes <, > and & inside string VALUES, so the
// single quote is the only HTML-significant character left raw — and left
// verbatim a signal carrying an apostrophe would close this attribute early and
// let an attacker graft a data-on-* expression onto <div id="root">. Double
// quotes stay intact: legal inside a single-quoted attribute, and the browser
// hands Datastar the same decoded value either way.
//
// only nil declares every slot (the GET first paint). Non-nil declares just the
// slots named, so a plain action patch ships what it wrote without clobbering a
// value the user is mid-edit; if that leaves nothing, no attribute is written.
func writeSignalsAttr(buf *bytes.Buffer, order []string, initial, only map[string]any, seen map[string]bool) {
	sig := make(map[string]any, len(order))
	for _, slot := range order {
		if only != nil {
			_, dirty := only[slot]
			// A slot the pre-action render did not carry is a control that just
			// appeared (a branch opened): the client store has no value for it,
			// or worse a stale one from whatever occupied the slot before, so
			// seed it even though the action never wrote it. seen nil means
			// there is no pre-action render to compare against.
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
// a Datastar text-bound span. T must be JSON-round-trippable.
type Signal[T any] struct {
	slotID // MUST stay the first field: prebindSignals stamps the wire name through a pointer add at the field's offset
	val    T
	bound  *Ctx // stamped at bind; the pass whose dirty map ships the patch
	warned bool // the never-rendered Set warning fired (once per signal)
}

// Ref returns the signal's Datastar expression — "$count" for a field Count,
// "$chat__draft" for a Draft inside an embedded Chat (the DOUBLE underscore
// marks the embed boundary; a plain nested struct joins with a single one) —
// for hand-written Datastar attributes the typed API does not cover:
//
//	h.Div(h.Data("show", p.Open.Ref()), ...)
//
// A Signal must be a plain field of the composition, through plain nested
// structs if you like; that is what names it before the View runs, so Ref reads
// the same name wherever it is called. One reached through a pointer, slice,
// array or map field has no field name and panics when rendered.
func (s *Signal[T]) Ref() string { return "$" + s.slot }

// Get returns the current value.
func (s *Signal[T]) Get() T { return s.val }

// Set assigns the value and records it dirty, which is what carries the change
// to the client. Only the signals an action actually wrote are ever declared,
// so a signal the user is mid-edit is never overwritten behind them: a live
// action pushes a patch-signals frame whose element patch declares nothing, a
// plain action's element patch carries a data-signals restricted to the dirty
// slots.
//
// Contract: the change reaches the client only for a signal the View actually
// renders (Bind or Display) — the wire name and the request binding are
// assigned at render, so a Set on an unrendered signal updates server memory
// and emits no patch. Bind or Display the signals an action mutates.
func (s *Signal[T]) Set(v T) {
	s.val = v
	if s.bound != nil && s.slot != "" {
		s.bound.dirty[s.slot] = v
		return
	}
	// Silent, an unbound Set reads as "Set does nothing" — warn once per signal.
	if !s.warned {
		s.warned = true
		log.Print("via: Signal.Set on a signal the View never rendered — the value updates server memory " +
			"but no patch reaches the client; Bind or Display the signal in the View")
	}
}

// bind assigns the signal's wire name, hydrates it from the request when
// present, and declares the slot for this render's data-signals. Every render
// entry point calls it, so the name is the handle's identity.
//
// writable is true only for Bind(), which emits data-bind. A Display()-only
// signal is a value the server publishes downward, so an inbound value for it
// is not an echo but a forgery — accepting it would let a client overwrite a
// flag OnInit set from the session and mint its own authorization (see OnArg).
// So the hydrator is registered for writable slots only, and an unwritable
// slot's inbound value is ignored on every path.
func (s *Signal[T]) bind(r *hcore.Renderer, writable bool) {
	b := r.Binder()
	s.bound = ctxOf(b)
	if s.bound == nil {
		panic("via: a Signal was rendered outside a via render")
	}
	// Resolved on EVERY bind, not cached: via.Embed copies the child by value,
	// so a signal the parent's View already bound arrives still carrying the
	// parent's prefix and would collide in the page's one signal store. It is
	// also the one place a Signal that is not a plain field is caught.
	s.slot = s.bound.signalSlot(unsafe.Pointer(s))
	if writable {
		if raw, ok := b.SignalInit(s.slot); ok {
			if rm, isRaw := raw.(json.RawMessage); isRaw {
				var v T
				if json.Unmarshal(rm, &v) == nil {
					s.val = v
				}
			}
		}
		// A live unit keeps this table from its last render rather than
		// re-rendering before an action, so the hydration SignalInit did above
		// must also be reachable by slot name.
		b.Hydrator(s.slot, func(raw json.RawMessage) {
			var v T
			if json.Unmarshal(raw, &v) == nil {
				s.val = v
			}
		})
	}
	b.DeclareSignal(s.slot, s.val)
}

// Display renders the signal as a Datastar text-bound span. Displaying the same
// signal in several places reuses its name, so they all update together. Unlike
// Bind it does NOT make the slot client-writable — an inbound value for a
// Display-only signal is ignored (see Signal.bind).
func (s *Signal[T]) Display() h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		s.bind(r, false)
		r.Render(h.Span(h.Data("text", "$"+s.slot), textHandle(s.val)))
	})
}

// Bind returns a two-way data-bind="<slot>" attribute for an input, sharing the
// signal's name with Display regardless of source order. Bind is what puts the
// slot under CLIENT control: its value is thereafter whatever the client last
// set, so never gate an authorization decision on a Bind()ed signal (see OnArg).
func (s *Signal[T]) Bind() h.Attr {
	return hcore.DynAttr(func(r *hcore.Renderer) {
		s.bind(r, true)
		r.Render(h.Data("bind", s.slot))
	})
}

// textHandle uses any so it can serve any signal T without appearing on a
// public signature.
func textHandle(v any) h.H {
	return hcore.Dyn(func(r *hcore.Renderer) { r.WriteEscaped(fmt.Sprint(v)) })
}
