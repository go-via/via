package via

import (
	"reflect"
	"strconv"

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
	val  T
	slot uint16
	key  string
}

// Get returns the current value.
func (s *Signal[T]) Get(ctx *Ctx) T {
	_ = ctx
	return s.val
}

// Set writes a new value and marks the signal dirty so the next flush
// patches it to the browser. From inside an action method or a
// via.Stream callback, the flush is automatic. From a raw goroutine
// you started yourself, call ctx.Sync() at a coalescing boundary —
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
	return h.Span(h.Data("text", "$"+s.key))
}

// Show returns a data-show attribute that toggles display by truthiness.
func (s *Signal[T]) Show() h.H {
	return h.Data("show", "$"+s.key)
}

// Key returns the wire key (qualified field path). Useful in tests.
func (s *Signal[T]) Key() string { return s.key }

// signalRef is the internal interface implemented by every Signal[T] /
// State[T] handle. It lets the runtime perform reflection-free per-request
// initialization across mixed-type fields.
type signalRef interface {
	bindSlot(slot uint16, key string)
	encode() ([]byte, error)
	decodeRaw(raw any)
}

func (s *Signal[T]) bindSlot(slot uint16, key string) {
	s.slot = slot
	s.key = key
}

func (s *Signal[T]) encode() ([]byte, error) {
	return encodeScalar(reflect.ValueOf(s.val))
}

func (s *Signal[T]) decodeRaw(raw any) {
	decodeScalarInto(reflect.ValueOf(&s.val).Elem(), raw)
}

// scalar JSON encoder, no fmt.Sprintf. Falls back to encoding/json for
// composites (handled in state.go via reflect path).
func encodeScalar(v reflect.Value) ([]byte, error) {
	switch v.Kind() {
	case reflect.String:
		return strconv.AppendQuote(nil, v.String()), nil
	case reflect.Bool:
		if v.Bool() {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(nil, v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.AppendUint(nil, v.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.AppendFloat(nil, v.Float(), 'g', -1, 64), nil
	}
	return jsonMarshal(v.Interface())
}

func decodeScalarInto(dst reflect.Value, raw any) {
	if raw == nil {
		return
	}
	switch dst.Kind() {
	case reflect.String:
		if s, ok := raw.(string); ok {
			dst.SetString(s)
		}
	case reflect.Bool:
		if b, ok := raw.(bool); ok {
			dst.SetBool(b)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch n := raw.(type) {
		case float64:
			dst.SetInt(int64(n))
		case int64:
			dst.SetInt(n)
		case int:
			dst.SetInt(int64(n))
		case string:
			if i, err := strconv.ParseInt(n, 10, 64); err == nil {
				dst.SetInt(i)
			}
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch n := raw.(type) {
		case float64:
			dst.SetUint(uint64(n))
		case uint64:
			dst.SetUint(n)
		case string:
			if i, err := strconv.ParseUint(n, 10, 64); err == nil {
				dst.SetUint(i)
			}
		}
	case reflect.Float32, reflect.Float64:
		switch n := raw.(type) {
		case float64:
			dst.SetFloat(n)
		case string:
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				dst.SetFloat(f)
			}
		}
	}
}
