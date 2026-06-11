package via

import (
	"reflect"

	"github.com/go-via/via/h"
)

// Signal is a typed reactive value mirrored to the browser. The value lives
// inside the composition struct; Read/Write go through the bound *Ctx so
// changes are tracked and propagated over SSE.
//
//	type Counter struct {
//	    Step via.Signal[int] `via:"step,init=1"`
//	}
//	c.Step.Read(ctx)       // returns int
//	c.Step.Write(ctx, 5)   // marks dirty, browser updates next flush
//	c.Step.Bind()          // <input> two-way bind: data-bind="step"
//	c.Step.Text()          // data-text="$step" attribute (attach to any element)
//	c.Step.TextSpan()      // <span data-text="$step"></span>
//
// Untyped, untagged Signal[T] fields use the lower-cased field name as the
// wire key. Tag form: `via:"name,init=value"`; either part is optional.
type Signal[T any] struct {
	val    T
	slot   uint16
	key    string
	dollar string // "$" + key, precomputed for Text/Show — saves a concat per render
}

// Read returns the current value. The ctx is unused today but kept so
// every reactive-handle Read has the same shape (and so future tab-
// scoped reads can move into the runtime without an API break). Accepts
// either *Ctx (action handlers) or *CtxR (View).
func (s *Signal[T]) Read(_ readCtx) T {
	return s.val
}

// Write stores a new value and marks the signal dirty so the next
// flush patches it to the browser. From inside an action method or a
// via.Stream callback, the flush is automatic. From a raw goroutine
// you started yourself, call ctx.SyncNow() at a coalescing boundary —
// the dirty bit alone won't reach the browser without a flush.
//
// Sugar over Update(ctx, func(T) (T, error) { return v, nil }) — the
// non-fallible path for "replace with a constant." Panics on nil ctx
// for the same reason as Update.
func (s *Signal[T]) Write(ctx *Ctx, v T) {
	if ctx == nil {
		panic("via: Signal.Write called with nil *Ctx")
	}
	_ = s.Update(ctx, func(T) (T, error) { return v, nil })
}

// Update atomically applies fn to the current value. fn receives the
// current T and returns (new T, error). On non-nil error the value is
// unchanged and the error is returned. Saves a Read/Write pair on
// transform-the-current-value patterns and is the only mutation path
// that lets a user reject a write (validation, conflict detection).
//
// Panics on nil ctx: without one, the write cannot reach the browser, so
// silently succeeding would desync server state from the client. From a
// raw goroutine, pass the bound *Ctx and call ctx.SyncNow() at a flush
// boundary.
func (s *Signal[T]) Update(ctx *Ctx, fn func(T) (T, error)) error {
	if ctx == nil {
		panic("via: Signal.Update called with nil *Ctx")
	}
	if fn == nil {
		return nil
	}
	next, err := fn(s.val)
	if err != nil {
		return err
	}
	s.val = next
	ctx.markSignalDirty(s.slot)
	return nil
}

// Bind returns a two-way binding attribute. Use on form inputs.
func (s *Signal[T]) Bind() h.H {
	return h.Data("bind", s.key)
}

// Text returns a reactive `data-text="$key"` attribute that binds this
// signal's value as the text content of whatever element it is attached to.
// For a standalone reactive span use [Signal.TextSpan].
func (s *Signal[T]) Text() h.H {
	return h.Data("text", s.dollar)
}

// TextSpan wraps [Signal.Text] in its own span: <span data-text="$key"></span>.
// Use it where no host element is available to carry the binding.
//
// EXPERIMENTAL: a young convenience helper; may change before 1.0.
func (s *Signal[T]) TextSpan() h.H {
	return h.Span(h.Data("text", s.dollar))
}

// Show returns a data-show attribute that toggles display by truthiness.
func (s *Signal[T]) Show() h.H {
	return h.Data("show", s.dollar)
}

// ShowUnless is the negation of [Signal.Show]: the element is hidden while
// the signal is truthy and shown while falsy. Saves hand-writing the "!$key"
// expression (and re-juggling the $ prefix the typed helpers exist to hide).
//
// EXPERIMENTAL: a young convenience helper; may change before 1.0.
func (s *Signal[T]) ShowUnless() h.H {
	return h.Data("show", "!"+s.dollar)
}

// Class toggles the named CSS class on the host element by this signal's
// truthiness, emitting Datastar's data-class:<name> attribute.
//
// name must be lower-case (or kebab-case). HTML attribute names are folded to
// lower-case by the browser parser, so a mixed-case name like "myThing"
// resolves to the class "mything" at runtime — pass "my-thing" if you need a
// hyphen. For a camelCase class name use Datastar's object form via
// h.DataClass with an explicit expression instead.
func (s *Signal[T]) Class(name string) h.H {
	return h.Data("class:"+name, s.dollar)
}

// Attr returns a data-attr:<name> attribute that mirrors this signal's
// truthiness onto the host element's HTML attribute. Truthy → attribute
// present (boolean form, e.g. `disabled`); falsy → attribute absent.
// For string-valued attributes, the attribute value tracks the signal.
//
//	h.Button(c.Saving.Attr("disabled"), h.Text("Save"))
//	h.A(c.Target.Attr("href"), h.Text("Open"))
func (s *Signal[T]) Attr(name string) h.H {
	return h.Data("attr:"+name, s.dollar)
}

// Style returns a data-style:<prop> attribute that drives an inline CSS
// property from this signal's stringified value. Pairs naturally with
// `Signal[string]` carrying a colour, length, etc.
//
//	h.Div(c.Hue.Style("background-color"))
func (s *Signal[T]) Style(prop string) h.H {
	return h.Data("style:"+prop, s.dollar)
}

// Key returns the wire key (qualified field path). Useful in tests.
func (s *Signal[T]) Key() string { return s.key }

// signalRef is the internal interface implemented by every Signal[T] /
// StateTab[T] handle. It lets the runtime perform reflection-free per-request
// initialization across mixed-type fields.
type signalRef interface {
	bindSlot(slot uint16, key string)
	encode() ([]byte, error)
	decodeRaw(raw any) error
}

// signalMarker tags Signal[T] (and types that embed it). Used by the
// walker to classify a field as a Signal regardless of whether the
// concrete type is Signal[T] or a specialized wrapper (SignalNum[T],
// SignalBool, ...). The marker method is promoted via embedding, so
// every wrapper inherits it for free.
type signalMarker interface{ isSignal() }

func (*Signal[T]) isSignal() {}

func (s *Signal[T]) bindSlot(slot uint16, key string) {
	s.slot = slot
	s.key = key
	s.dollar = "$" + key
}

func (s *Signal[T]) encode() ([]byte, error) {
	return encodeScalar(reflect.ValueOf(s.val))
}

func (s *Signal[T]) decodeRaw(raw any) error {
	return decodeScalarChecked(reflect.ValueOf(&s.val).Elem(), raw)
}
