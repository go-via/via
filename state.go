package via

import (
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/go-via/via/h"
)

// StateTab is a typed, server-only reactive value. Mutations trigger a view
// re-render and SSE patch. Tab-scoped: each browser tab has its own value.
//
// For session-scoped or app-scoped state use StateSess[T] / StateApp[T].
//
//	type Counter struct {
//	    Hits   via.StateTab[int]
//	    Filter via.StateTab[string] `via:"filter,init=all"`
//	}
//	c.Hits.Read(ctx)       // returns int
//	c.Hits.Write(ctx, 0)   // direct write
//	c.Hits.Update(ctx, func(n int) (int, error) { return n + 1, nil}) // numeric delta
//
// The optional `via:"name,init=value"` tag mirrors Signal[T]: either part
// is optional, and init=… is decoded into the field at bind time.
type StateTab[T any] struct {
	val T
	key string
}

// Read returns the current value. The ctx is unused today but kept so
// StateTab[T] mirrors Signal[T]'s shape (and so future tab-scoped reads
// can move into the runtime without an API break). Accepts either *Ctx
// (action handlers) or *CtxR (View).
func (s *StateTab[T]) Read(_ readCtx) T {
	return s.val
}

// Write stores a new value and marks the composition dirty so the
// next flush re-renders the view fragment. From inside an action
// method or a via.Stream callback, the flush is automatic. From a raw
// goroutine you started yourself, call ctx.SyncNow() at a coalescing
// boundary — the dirty bit alone won't reach the browser without a
// flush.
//
// Sugar over Update(ctx, func(T) (T, error) { return v, nil }) — the
// non-fallible path for "replace with a constant." Panics on nil ctx
// for the same reason as Update.
func (s *StateTab[T]) Write(ctx *Ctx, v T) {
	if ctx == nil {
		panic("via: StateTab.Write called with nil *Ctx")
	}
	_ = s.Update(ctx, func(T) (T, error) { return v, nil })
}

// Update atomically applies fn to the current value. fn receives the
// current T and returns (new T, error). On non-nil error the value is
// unchanged and the error is returned. Saves a Read/Write pair on
// common increment/transform patterns and is the only mutation path
// that lets a user reject a write (validation, conflict detection):
//
//	err := c.Hits.Update(ctx, func(n int) (int, error) {
//	    if n >= max { return 0, errBudget }
//	    return n + 1, nil
//	})
//
// Panics on nil ctx: without one the next flush cannot re-render, so
// silently succeeding would desync server state from the client.
func (s *StateTab[T]) Update(ctx *Ctx, fn func(T) (T, error)) error {
	if ctx == nil {
		panic("via: StateTab.Update called with nil *Ctx")
	}
	if fn == nil {
		return nil
	}
	next, err := fn(s.val)
	if err != nil {
		return err
	}
	s.val = next
	ctx.markStateDirty()
	return nil
}

// Text returns a static text node carrying the current value. Re-renders
// happen as part of the view fragment, not via a client signal. Mirrors
// StateSess/StateApp.Text so every reactive-value Text(ctx) reads the
// same way; the ctx is unused on StateTab (the value lives on the
// struct) and accepted only for signature parity. Accepts either *Ctx
// (action handlers) or *CtxR (View).
func (s *StateTab[T]) Text(_ readCtx) h.H {
	return h.Text(scalarString(reflect.ValueOf(s.val)))
}

// Key returns the local key. Useful in tests.
func (s *StateTab[T]) Key() string { return s.key }

func (s *StateTab[T]) bindSlot(_ uint16, key string) {
	// State doesn't carry a per-slot dirty bit (it uses Ctx.stateDirty)
	// so the slot index is intentionally discarded; the bindSlot
	// signature is fixed by the signalRef interface that Signal[T] also
	// implements.
	s.key = key
}

func (s *StateTab[T]) encode() ([]byte, error) {
	return encodeScalar(reflect.ValueOf(s.val))
}

func (s *StateTab[T]) decodeRaw(raw any) error {
	return decodeScalarChecked(reflect.ValueOf(&s.val).Elem(), raw)
}

// stateTabMarker tags StateTab[T] (and types that embed it). See
// signalMarker for the rationale.
type stateTabMarker interface{ isStateTab() }

func (*StateTab[T]) isStateTab() {}

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
		// reflect.Value.Float widens a float32 to float64; formatting at
		// bitSize 64 would surface the widening (float32(0.1) → 0.10000000149011612).
		bits := 64
		if v.Kind() == reflect.Float32 {
			bits = 32
		}
		return strconv.FormatFloat(v.Float(), 'g', -1, bits)
	}
	b, _ := json.Marshal(v.Interface())
	return string(b)
}
