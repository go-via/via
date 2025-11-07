package via

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-via/via/h"
)

// Signal represents a value that is reactive in the browser. Signals
// are synct with the server right before an action triggers.
//
// Use Bind() to connect a signal to an input and Text() to display it
// reactively on an html element.
type signal struct {
	id      string
	v       reflect.Value
	t       reflect.Type
	changed bool
	err     error
}

// ID returns the signal ID
func (s *signal) ID() string {
	return s.id
}

// Err returns a signal error or nil if it contains no error.
//
// It is useful to check for errors after updating signals with
// dinamic values.
func (s *signal) Err() error {
	return s.err
}

// Bind binds this signal to an imput element. When the imput changes
// its value the signal updates in real-time in the browser.
//
// Example:
//
//	h.Input(h.Type("number"), mysignal.Bind())
func (s *signal) Bind() h.H {
	return h.Data("bind", s.id)
}

// Text binds the signal value to an html element as text.
//
// Example:
//
//	h.Div(mysignal.Text())
func (s *signal) Text() h.H {
	return h.Data("text", "$"+s.id)
}

// SetValue updates the signal’s value and marks it for synchronization with the browser.
// The change will be propagated to the browser using *Context.Sync() or *Context.SyncSignals().
func (s *signal) SetValue(v any) {
	val := reflect.ValueOf(v)
	typ := reflect.TypeOf(v)
	if typ != s.t {
		s.err = fmt.Errorf("expected type '%s', got '%s'", s.t.String(), typ.String())
		return
	}
	s.v = val
	s.changed = true
	s.err = nil
}

// String return the signal value as a string.
func (s *signal) String() string {
	return fmt.Sprintf("%v", s.v)
}

// Bool tries to read the signal value as a bool.
// Returns the value or false on failure.
func (s *signal) Bool() bool {
	switch s.v.Kind() {
	case reflect.Bool:
		return s.v.Bool()
	case reflect.String:
		val := strings.ToLower(s.v.String())
		return val == "true" || val == "1" || val == "yes" || val == "on"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return s.v.Int() != 0
	case reflect.Float32, reflect.Float64:
		return s.v.Float() != 0
	default:
		return false
	}
}

// Int tries to read the signal value as an int.
// Returns the value or 0 on failure.
func (s *signal) Int() int {
	if n, err := strconv.Atoi(s.v.String()); err == nil {
		return n
	}
	if s.v.CanInt() {
		return int(s.v.Int())
	}
	if s.v.CanFloat() {
		return int(s.v.Float())
	}
	return 0
}

// Int64 tries to read the signal value as an int64.
// Returns the value or 0 on failure.
func (s *signal) Int64() int64 {
	if n, err := strconv.ParseInt(s.v.String(), 10, 64); err == nil {
		return n
	}
	if s.v.CanInt() {
		return s.v.Int()
	}
	if s.v.CanFloat() {
		return int64(s.v.Float())
	}
	return 0
}

// Uint64 tries to read the signal value as an uint64.
// Returns the value or 0 on failure.
func (s *signal) Uint64() uint64 {
	if n, err := strconv.ParseUint(s.v.String(), 10, 64); err == nil {
		return n
	}
	if s.v.CanUint() {
		return s.v.Uint()
	}
	if s.v.CanFloat() {
		return uint64(s.v.Float())
	}
	return 0
}

// Float64 tries to read the signal value as a float64.
// Returns the value or 0.0 on failure.
func (s *signal) Float64() float64 {
	if n, err := strconv.ParseFloat(s.v.String(), 64); err == nil {
		return n
	}
	if s.v.CanFloat() {
		return s.v.Float()
	}
	if s.v.CanInt() {
		return float64(s.v.Int())
	}
	return 0.0
}

// Complex128 tries to read the signal value as a complex128.
// Returns the value or 0 on failure.
func (s *signal) Complex128() complex128 {
	if s.v.Kind() == reflect.Complex128 {
		return s.v.Complex()
	}
	if s.v.Kind() == reflect.String {
		if n, err := strconv.ParseComplex(s.v.String(), 128); err == nil {
			return n
		}
	}
	if s.v.CanFloat() {
		return complex(s.v.Float(), 0)
	}
	if s.v.CanInt() {
		return complex(float64(s.v.Int()), 0)
	}
	return complex(0, 0)
}

// Bytes tries to read the signal value as a []byte
// Returns the value or an empty []byte on failure.
func (s *signal) Bytes() []byte {
	switch s.v.Kind() {
	case reflect.Slice:
		if s.v.Type().Elem().Kind() == reflect.Uint8 {
			return s.v.Bytes()
		}
	case reflect.String:
		return []byte(s.v.String())
	}
	return make([]byte, 0)
}
