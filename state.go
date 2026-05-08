package via

import (
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/go-via/via/h"
)

// State is a typed, server-only reactive value. Mutations trigger a view
// re-render and SSE patch. Tab-scoped: each browser tab has its own value.
//
// For session-scoped or app-scoped state use scope.User[T] / scope.App[T].
//
//	type Counter struct {
//	    Hits   via.State[int]
//	    Filter via.State[string] `via:"filter,init=all"`
//	}
//	c.Hits.Get(ctx)        // returns int
//	c.Hits.Set(ctx, 0)     // direct write
//	via.Add(ctx, &c.Hits, 1) // numeric delta via Mutable[T]
//
// The optional `via:"name,init=value"` tag mirrors Signal[T]: either part
// is optional, and init=… is decoded into the field at bind time.
type State[T any] struct {
	val T
	key string
}

// Get returns the current value. The ctx is unused today but kept so
// State[T] mirrors Signal[T]'s shape (and so future tab-scoped reads
// can move into the runtime without an API break).
func (s *State[T]) Get(_ *Ctx) T {
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

func (s *State[T]) bindSlot(_ uint16, key string) {
	// State doesn't carry a per-slot dirty bit (it uses Ctx.stateDirty)
	// so the slot index is intentionally discarded; the bindSlot
	// signature is fixed by the signalRef interface that Signal[T] also
	// implements.
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
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	}
	b, _ := json.Marshal(v.Interface())
	return string(b)
}
