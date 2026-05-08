package via

import (
	"reflect"

	"github.com/go-via/via/h"
)

// State is a typed, server-only reactive value. Mutations trigger a view
// re-render and SSE patch. Tab-scoped: each browser tab has its own value.
//
// For session-scoped or app-scoped state use scope.User[T] / scope.App[T].
//
//	type Counter struct {
//	    Hits via.State[int]
//	}
//	c.Hits.Get(ctx); c.Hits.Set(ctx, c.Hits.Get(ctx)+1)
type State[T any] struct {
	val  T
	slot uint16
	key  string
}

// Get returns the current value.
func (s *State[T]) Get(ctx *Ctx) T {
	_ = ctx
	return s.val
}

// Set writes a new value and marks the composition dirty so the next
// flush re-renders the view fragment. From inside an action method or
// a via.Stream callback, the flush is automatic. From a raw goroutine
// you started yourself, call ctx.Sync() at a coalescing boundary —
// the dirty bit alone won't reach the browser without a flush.
func (s *State[T]) Set(ctx *Ctx, v T) {
	s.val = v
	if ctx != nil {
		ctx.markStateDirty()
	}
}

// Update applies fn to the current value and stores the result. Saves
// a Get/Set pair on common increment/transform patterns:
//
//	c.Hits.Update(ctx, func(n int) int { return n + 1 })
func (s *State[T]) Update(ctx *Ctx, fn func(T) T) {
	if fn == nil {
		return
	}
	s.val = fn(s.val)
	if ctx != nil {
		ctx.markStateDirty()
	}
}

// Text returns a span whose text content is the current value at render time.
// Re-renders happen as part of the view fragment, not via a client signal.
func (s *State[T]) Text() h.H {
	return h.Text(scalarString(reflect.ValueOf(s.val)))
}

// Key returns the local key. Useful in tests.
func (s *State[T]) Key() string { return s.key }

func (s *State[T]) bindSlot(slot uint16, key string) {
	s.slot = slot
	s.key = key
}

func (s *State[T]) encode() ([]byte, error) {
	return encodeScalar(reflect.ValueOf(s.val))
}

func (s *State[T]) decodeRaw(raw any) {
	decodeScalarInto(reflect.ValueOf(&s.val).Elem(), raw)
}

// scalarString returns the string form of a scalar value without going
// through fmt.Sprintf (which costs interface boxing for every call).
func scalarString(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconvAppendInt(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconvAppendUint(v.Uint())
	case reflect.Float32, reflect.Float64:
		return strconvAppendFloat(v.Float())
	}
	b, _ := jsonMarshal(v.Interface())
	return string(b)
}
