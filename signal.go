package via

import (
	"reflect"

	"github.com/go-via/via/h"
)

// Signal is a typed reactive value mirrored to the browser. The value lives
// inside the composition struct; Get/Set go through the bound *Ctx so
// changes are tracked and propagated over SSE.
//
//	type Counter struct {
//	    Step via.Signal[int] `via:"step,init=1"`
//	}
//	c.Step.Get(ctx)        // returns int
//	c.Step.Set(ctx, 5)     // marks dirty, browser updates next flush
//	c.Step.Bind()          // <input> two-way bind: data-bind="step"
//	c.Step.Text()          // <span data-text="$step"></span>
//
// Untyped, untagged Signal[T] fields use the lower-cased field name as the
// wire key. Tag form: `via:"name,init=value"`; either part is optional.
type Signal[T any] struct {
	val    T
	slot   uint16
	key    string
	dollar string // "$" + key, precomputed for Text/Show — saves a concat per render
}

// Get returns the current value. The ctx is unused today but kept so
// every reactive-handle Get/Set has the same shape (and so future tab-
// scoped reads can move into the runtime without an API break).
func (s *Signal[T]) Get(_ *Ctx) T {
	return s.val
}

// Set writes a new value and marks the signal dirty so the next flush
// patches it to the browser. From inside an action method or a
// via.Stream callback, the flush is automatic. From a raw goroutine
// you started yourself, call ctx.SyncNow() at a coalescing boundary —
// the dirty bit alone won't reach the browser without a flush.
func (s *Signal[T]) Set(ctx *Ctx, v T) {
	s.val = v
	if ctx != nil {
		ctx.markSignalDirty(s.slot)
	}
}

// Update applies fn to the current value and stores the result. Saves
// a Get/Set pair on transform-the-current-value patterns.
func (s *Signal[T]) Update(ctx *Ctx, fn func(T) T) {
	if fn == nil {
		return
	}
	s.val = fn(s.val)
	if ctx != nil {
		ctx.markSignalDirty(s.slot)
	}
}

// Bind returns a two-way binding attribute. Use on form inputs.
func (s *Signal[T]) Bind() h.H {
	return h.Data("bind", s.key)
}

// Text returns a reactive text span: <span data-text="$key"></span>.
func (s *Signal[T]) Text() h.H {
	return h.Span(h.Data("text", s.dollar))
}

// Show returns a data-show attribute that toggles display by truthiness.
func (s *Signal[T]) Show() h.H {
	return h.Data("show", s.dollar)
}

// Attr returns a data-attr-<name> attribute that mirrors this signal's
// truthiness onto the host element's HTML attribute. Truthy → attribute
// present (boolean form, e.g. `disabled`); falsy → attribute absent.
// For string-valued attributes, the attribute value tracks the signal.
//
//	h.Button(c.Saving.Attr("disabled"), h.Text("Save"))
//	h.A(c.Target.Attr("href"), h.Text("Open"))
func (s *Signal[T]) Attr(name string) h.H {
	return h.Data("attr-"+name, s.dollar)
}

// Style returns a data-style-<prop> attribute that drives an inline CSS
// property from this signal's stringified value. Pairs naturally with
// `Signal[string]` carrying a colour, length, etc.
//
//	h.Div(c.Hue.Style("background-color"))
func (s *Signal[T]) Style(prop string) h.H {
	return h.Data("style-"+prop, s.dollar)
}

// Key returns the wire key (qualified field path). Useful in tests.
func (s *Signal[T]) Key() string { return s.key }

// signalRef is the internal interface implemented by every Signal[T] /
// StateTab[T] handle. It lets the runtime perform reflection-free per-request
// initialization across mixed-type fields.
type signalRef interface {
	bindSlot(slot uint16, key string)
	encode() ([]byte, error)
	decodeRaw(raw any)
}

func (s *Signal[T]) bindSlot(slot uint16, key string) {
	s.slot = slot
	s.key = key
	s.dollar = "$" + key
}

func (s *Signal[T]) encode() ([]byte, error) {
	return encodeScalar(reflect.ValueOf(s.val))
}

func (s *Signal[T]) decodeRaw(raw any) {
	decodeScalarInto(reflect.ValueOf(&s.val).Elem(), raw)
}
