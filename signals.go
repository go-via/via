package via

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"unsafe"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// writeSignalsAttr writes the Datastar signal declaration as a single-quoted
// attribute: data-signals='{...}'.
//
// json.Marshal already unicode-escapes <, > and & inside string values, so the
// single quote is the only HTML-significant character left raw — and left
// verbatim a signal carrying an apostrophe would close this attribute early and
// let an attacker graft a data-on-* expression onto <div id="root">. Double
// quotes stay intact: legal inside a single-quoted attribute, and the browser
// hands Datastar the same decoded value either way.
//
// only nil declares every slot (the GET first paint). Non-nil declares just the
// slots named, so a plain action patch ships what it wrote without clobbering a
// value the user is mid-edit; if that leaves nothing, no attribute is written.
func writeSignalsAttr(log *slog.Logger, buf *bytes.Buffer, order []string, initial, only map[string]any, seen map[string]bool) {
	want := make([]string, 0, len(order))
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
		want = append(want, slot)
	}
	sort.Strings(want) // encoding/json sorted the map this used to build; keep the bytes stable

	// Marshalled one slot at a time, not as one map: a single unmarshalable
	// value used to fail the whole object and emit data-signals='', wiping the
	// client store for every signal on the page with nothing in the log. Drop
	// the offender, keep the rest — the same treatment an unmarshalable OnArg
	// value gets.
	var obj bytes.Buffer
	obj.WriteByte('{')
	n := 0
	for _, slot := range want {
		val, err := json.Marshal(initial[slot])
		if err != nil {
			log.Warn("via: a signal holds a value encoding/json cannot marshal; it is left out of "+
				"data-signals and the client store has no value for it — make the type JSON-round-trippable",
				"slot", slot, "err", err)
			continue
		}
		if n > 0 {
			obj.WriteByte(',')
		}
		n++
		key, _ := json.Marshal(slot)
		obj.Write(key)
		obj.WriteByte(':')
		obj.Write(val)
	}
	obj.WriteByte('}')
	if n == 0 && only != nil {
		return
	}
	raw := obj.Bytes()

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

// revertSet records how to put a signal's server-authored value back after a
// render that applied the client's posted values on top of it.
//
// It exists because a live unit outlives the request: hydration writes straight
// into the instance's field, so without an undo the client's value became the
// server's own state and every later push re-rendered from it — which is how a
// posted signal used to open a gated branch and leave its action dispatchable
// for the life of the connection. livePush reverts, renders the authority, then
// re-applies for display, so the client can still steer what it sees and never
// what it may call.
//
// First write wins per slot: two hydrations in one cycle must both unwind to
// the value the server last authored, not to the first client value. A
// Signal.Set drops the slot's entry — a server write supersedes the client's
// and must survive the revert.
type revertSet struct{ undo map[string]func() }

func newRevertSet() *revertSet { return &revertSet{undo: map[string]func(){}} }

func (r *revertSet) note(slot string, fn func()) {
	if r == nil {
		return
	}
	if _, dup := r.undo[slot]; dup {
		return
	}
	r.undo[slot] = fn
}

func (r *revertSet) drop(slot string) {
	if r != nil {
		delete(r.undo, slot)
	}
}

func (r *revertSet) restore() {
	if r == nil {
		return
	}
	for _, fn := range r.undo {
		fn()
	}
	clear(r.undo)
}

// Signal is a client-resident value that round-trips per request and renders as
// a Datastar text-bound span. T must be JSON-round-trippable.
//
// Not safe for concurrent use. Call it only from via callbacks (OnInit, an
// action handler, a Tick or Listen handler); to reach a unit from a goroutine
// of your own, publish to a [topic.Topic] the unit Listens to. See the package
// doc for the goroutine model.
type Signal[T any] struct {
	slotID // MUST stay the first field: prebindSignals stamps it through a pointer add at the field's offset
	val    T
}

// Ref returns the signal's Datastar expression — "$count" for a field Count,
// "$chat__draft" for a Draft inside an embedded Chat (the double underscore
// marks the child boundary; a plain nested struct joins with a single one) —
// for hand-written Datastar attributes the typed API does not cover:
//
//	h.Div(h.Data("show", p.Open.Ref()), ...)
//
// A Signal must be a plain field of the composition, through plain nested
// structs if you like; that is what names it before the View runs, so Ref reads
// the same name wherever it is called. One reached through a pointer, slice,
// array or map field has no field name, and Ref on it panics — the same
// verdict rendering it gives, moved to the call that would otherwise have
// produced a bare "$" and a silently dead Datastar expression.
func (s *Signal[T]) Ref() string {
	if s.slot == "" {
		panic("via: Signal.Ref on a signal with no wire name — a Signal must be a plain field of the " +
			"composition (through plain nested structs if you like), not one reached through a pointer, " +
			"slice, array or map field; \"$\" alone is not a Datastar expression")
	}
	return "$" + s.slot
}

// Get returns the server-side value: what the last Set wrote, or what the
// client posted back for a Bind()ed signal on this request.
func (s *Signal[T]) Get() T { return s.val }

// Set assigns the value and declares the slot, which is what carries the change
// to the client: a live action pushes a patch-signals frame, a plain action's
// element patch carries a data-signals restricted to the slots it wrote. Only
// the signals an action actually wrote are declared, so a signal the user is
// mid-edit is never overwritten behind them.
//
// The View need not render the signal — Set is itself the declaration — so a
// Set in OnInit seeds a client-side island that reads the slot and nothing
// else, with no Bind or Display anywhere. Declaring is not hydrating: only Bind
// makes a slot client-writable.
func (s *Signal[T]) Set(v T) {
	s.val = v
	if s.bound == nil || s.slot == "" {
		return
	}
	s.bound.declareSignal(s.slot, v)
	if !s.bound.viewRan {
		return // a seed: the render about to run declares it, and dirtying it would evict the client's own value
	}
	s.bound.dirty[s.slot] = v
	// A server write supersedes whatever the client posted for this slot, so it
	// must survive the revert livePush does before the next render.
	s.bound.rev.drop(s.slot)
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
	// Resolved on every bind, not cached: via.Child copies the child by value,
	// so a signal the parent's View already bound arrives still carrying the
	// parent's prefix and would collide in the page's one signal store. It is
	// also the one place a Signal that is not a plain field is caught.
	s.slot = s.bound.signalSlot(unsafe.Pointer(s))
	if writable {
		// The one hydration door, deliberately: it is the only one that records
		// an undo (revertSet), which is what keeps a live unit's instance
		// server-authored between renders. A second path that wrote s.val
		// straight from the request would silently re-open the escalation
		// livePush closes.
		b.Hydrator(s.slot, func(raw json.RawMessage) {
			var v T
			if json.Unmarshal(raw, &v) != nil {
				return
			}
			if c := s.bound; c != nil {
				prev := s.val
				c.rev.note(s.slot, func() { s.val = prev })
			}
			s.val = v
		})
	}
	b.DeclareSignal(s.slot, s.val)
}

// Display renders the signal as a Datastar text-bound span. Displaying the same
// signal in several places reuses its name, so they all update together. Unlike
// Bind it does not make the slot client-writable — an inbound value for a
// Display-only signal is ignored (see Signal.bind).
func (s *Signal[T]) Display() h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		s.bind(r, false)
		r.Render(h.Span(h.Data("text", "$"+s.slot), textHandle(s.val)))
	})
}

// Bind returns a two-way data-bind="<slot>" attribute for an input, sharing the
// signal's name with Display regardless of source order. Bind is what puts the
// slot under client control: its value is thereafter whatever the client last
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

// SignalCS is a client-only signal: the server never reads or writes it. It
// starts at T's zero value and its wire name is "_"-prefixed, which Datastar's
// fetch filter drops from every POST. There is no Set and no Get; a value the
// server needs to know is a [Signal].
type SignalCS[T any] struct {
	slotID // MUST stay the first field: prebindSignals stamps it through a pointer add at the field's offset
}

func (*SignalCS[T]) isViaSignalCS() {}

func (*SignalCS[T]) csZero() any { var z T; return z }

// Ref returns the signal's Datastar expression — "$_open" for a field Open.
// Like [Signal.Ref] it panics on a signal reached through a pointer, slice,
// array or map field, which has no field name to be named by.
func (s *SignalCS[T]) Ref() string {
	if s.slot == "" {
		panic("via: SignalCS.Ref on a signal with no wire name — a SignalCS must be a plain field of the " +
			"composition (through plain nested structs if you like), not one reached through a pointer, " +
			"slice, array or map field; \"$\" alone is not a Datastar expression")
	}
	return "$" + s.slot
}

func (s *SignalCS[T]) bind(r *hcore.Renderer) {
	b := r.Binder()
	c := ctxOf(b)
	if c == nil {
		panic("via: a SignalCS was rendered outside a via render")
	}
	// Re-resolved per bind for the same reason Signal.bind does it: via.Child
	// copies the child by value, so a slot stamped under the parent's prefix
	// must be re-minted under the child's.
	s.slot = c.signalSlot(unsafe.Pointer(s))
	// No hydrator: an inbound value for an "_" name is never an echo of one via
	// sent, so accepting it would only admit a forgery.
	var zero T
	b.DeclareSignal(s.slot, zero)
}

// Display renders the signal as a Datastar text-bound span, showing T's zero
// value until the client changes it.
func (s *SignalCS[T]) Display() h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		s.bind(r)
		var zero T
		r.Render(h.Span(h.Data("text", "$"+s.slot), textHandle(zero)))
	})
}

// Bind returns a two-way data-bind="<slot>" attribute for an input. The value
// stays in the browser: unlike [Signal.Bind] it makes nothing server-readable.
func (s *SignalCS[T]) Bind() h.Attr {
	return hcore.DynAttr(func(r *hcore.Renderer) {
		s.bind(r)
		r.Render(h.Data("bind", s.slot))
	})
}
