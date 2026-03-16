package via

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/go-via/via/h"
)

// signalEntry is the internal interface for homogeneous signal storage in sync.Map.
type signalEntry interface {
	getID() string
	displayID() string
	rawValue() any
	setRawValue(v any)
	isChanged() bool
	markSynced()
	hasError() bool
	getErr() error
}

// signalOf is the generic signal implementation.
// Construct with the Signal free function.
type signalOf[T any] struct {
	id      string
	tag     string
	val     T
	changed bool
	err     error
}

// signalEntry implementation

func (s *signalOf[T]) getID() string { return s.id }

func (s *signalOf[T]) displayID() string {
	if s.tag != "" {
		return s.tag + "_" + s.id
	}
	return s.id
}

func (s *signalOf[T]) rawValue() any {
	rv := reflect.ValueOf(any(s.val))
	if rv.IsValid() {
		switch rv.Kind() {
		case reflect.Slice, reflect.Struct:
			if j, err := json.Marshal(s.val); err == nil {
				return string(j)
			}
		}
	}
	return fmt.Sprintf("%v", s.val)
}

func (s *signalOf[T]) setRawValue(v any) {
	if typed, ok := v.(T); ok {
		s.val = typed
	}
	s.changed = true
	s.err = nil
}

func (s *signalOf[T]) isChanged() bool { return s.changed }
func (s *signalOf[T]) markSynced()     { s.changed = false }
func (s *signalOf[T]) hasError() bool  { return s.err != nil }
func (s *signalOf[T]) getErr() error   { return s.err }

// ID returns the signal's unique identifier.
func (s *signalOf[T]) ID() string { return s.id }

// Ref returns "$<displayID>" for use in datastar expressions.
func (s *signalOf[T]) Ref() string { return "$" + s.displayID() }

// Tag sets a human-readable prefix for the signal display ID.
// Must be called before rendering. Affects Bind(), Text(), Show(), and Ref().
func (s *signalOf[T]) Tag(name string) { s.tag = name }

// Get returns the current typed value of the signal.
func (s *signalOf[T]) Get(_ *Context) T { return s.val }

// SetValue updates the signal value and marks it dirty for the next sync.
func (s *signalOf[T]) SetValue(v T) {
	s.val = v
	s.changed = true
	s.err = nil
}

// Bind returns a data-bind attribute for connecting this signal to an input element.
func (s *signalOf[T]) Bind() h.H {
	return h.Data("bind", s.displayID())
}

// Text returns a span element with a data-text attribute referencing this signal.
func (s *signalOf[T]) Text() h.H {
	return h.Span(h.Data("text", "$"+s.displayID()))
}

// Show returns a data-show attribute for conditional element visibility.
func (s *signalOf[T]) Show() h.H {
	return h.Data("show", "$"+s.displayID())
}

// Err returns any error associated with this signal.
func (s *signalOf[T]) Err() error { return s.err }

// Signal creates a typed reactive signal with the given initial value.
// Type is inferred: via.Signal(c, 0) creates a signal of type int.
func Signal[T any](c *Context, initial T) *signalOf[T] {
	sigID := genRandID()

	// Check for nil initial value (handles interface/pointer nil)
	if rv := reflect.ValueOf(any(initial)); !rv.IsValid() {
		c.app.logErr(c, "failed to bind signal: nil signal value")
		var zero T
		return &signalOf[T]{
			id:  sigID,
			val: zero,
			err: fmt.Errorf("context '%s' failed to bind signal '%s': nil signal value", c.id, sigID),
		}
	}

	sig := &signalOf[T]{
		id:      sigID,
		val:     initial,
		changed: false,
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isComponent() {
		c.parentPageCtx.signals.Store(sigID, sig)
	} else {
		c.signals.Store(sigID, sig)
	}
	return sig
}
