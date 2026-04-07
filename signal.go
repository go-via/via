package via

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/go-via/via/h"
)

// signalMeta is the runtime interface for signal metadata on Cmp.
type signalMeta interface {
	displayID() string
	initialTypedValue() any
	initialRawValue() any
	coerce(v any) any
	hasError() bool
}

// signalValue holds the per-tab runtime state for one signal.
type signalValue struct {
	raw     any
	changed bool
}

// signalOf is a typed handle created at definition time, shared across all tabs.
type signalOf[T any] struct {
	id      string
	tag     string
	initial T
	err     error
}

// --- signalMeta implementation (consumed by runtime) ---

func (s *signalOf[T]) displayID() string {
	if s.tag != "" {
		return s.tag + "_" + s.id
	}
	return s.id
}

func (s *signalOf[T]) initialTypedValue() any { return s.initial }

func (s *signalOf[T]) initialRawValue() any {
	rv := reflect.ValueOf(any(s.initial))
	if rv.IsValid() {
		switch rv.Kind() {
		case reflect.Slice, reflect.Map, reflect.Struct, reflect.Pointer:
			if j, err := json.Marshal(s.initial); err == nil {
				return string(j)
			}
		}
	}
	return s.initial
}

func (s *signalOf[T]) coerce(v any) any {
	if _, ok := v.(T); ok {
		return v
	}
	// JSON numbers arrive as float64; coerce to the signal's concrete type.
	if f64, ok := v.(float64); ok {
		var zero T
		switch any(zero).(type) {
		case int:
			return int(f64)
		case int8:
			return int8(f64)
		case int16:
			return int16(f64)
		case int32:
			return int32(f64)
		case int64:
			return int64(f64)
		case uint:
			return uint(f64)
		case uint8:
			return uint8(f64)
		case uint16:
			return uint16(f64)
		case uint32:
			return uint32(f64)
		case uint64:
			return uint64(f64)
		case float32:
			return float32(f64)
		case float64:
			return f64
		}
	}
	return v
}

func (s *signalOf[T]) hasError() bool { return s.err != nil }

// --- public API (consumed by user code) ---

// ID returns the unique identifier for this signal.
func (s *signalOf[T]) ID() string { return s.id }

// Tag prepends a label to the signal's display ID.
func (s *signalOf[T]) Tag(name string) { s.tag = name }

// Get returns the current typed value of the signal for this tab.
func (s *signalOf[T]) Get(ctx *Ctx) T {
	if ctx != nil {
		if sv, ok := ctx.signalValues[s.id]; ok {
			if typed, ok := sv.raw.(T); ok {
				return typed
			}
		}
	}
	return s.initial
}

// SetValue updates the signal value for this tab and marks it dirty for sync.
func (s *signalOf[T]) SetValue(ctx *Ctx, v T) {
	sv := ctx.signalValues[s.id]
	if sv == nil {
		ctx.signalValues[s.id] = &signalValue{raw: v, changed: true}
		return
	}
	sv.raw = v
	sv.changed = true
}

// Err returns any error associated with this signal.
func (s *signalOf[T]) Err() error { return s.err }

// Bind returns an h.H attribute that binds this signal to an input element.
func (s *signalOf[T]) Bind() h.H {
	return h.Data("bind", s.displayID())
}

// Text returns an h.H element that displays the signal value reactively.
func (s *signalOf[T]) Text() h.H {
	return h.Span(h.Data("text", "$"+s.displayID()))
}

// Show returns an h.H attribute that toggles visibility based on the signal value.
func (s *signalOf[T]) Show() h.H {
	return h.Data("show", "$"+s.displayID())
}

// Ref returns the signal reference string for use in datastar expressions.
func (s *signalOf[T]) Ref() string {
	return "$" + s.displayID()
}

// Signal creates a typed reactive signal with the given initial value.
func Signal[T any](cmp *Cmp, initial T) *signalOf[T] {
	sigID := "via_" + genRandID()

	if rv := reflect.ValueOf(any(initial)); !rv.IsValid() {
		var zero T
		return &signalOf[T]{
			id:      sigID,
			initial: zero,
			err:     fmt.Errorf("failed to bind signal '%s': nil signal value", sigID),
		}
	}

	sig := &signalOf[T]{
		id:      sigID,
		initial: initial,
	}

	cmp.signals[sigID] = sig
	return sig
}
