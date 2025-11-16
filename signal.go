package via

import (
	"fmt"
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
	val     any
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

// Bind binds this signal to an input element. When the input changes
// its value the signal updates in real-time in the browser.
//
// Example:
//
//	h.Input(h.Type("number"), mysignal.Bind())
func (s *signal) Bind() h.H {
	return h.Data("bind", s.id)
}

// Text binds the signal value to an html span element as text.
//
// Example:
//
//	h.Div(mysignal.Text())
func (s *signal) Text() h.H {
	return h.Span(h.Data("text", "$"+s.id))
}

// SetValue updates the signal’s value and marks it for synchronization with the browser.
// The change will be propagated to the browser using *Context.Sync() or *Context.SyncSignals().
func (s *signal) SetValue(v any) {
	s.val = v
	s.changed = true
	s.err = nil
}

// String return the signal value as a string.
func (s *signal) String() string {
	return fmt.Sprintf("%v", s.val)
}

// Bool tries to read the signal value as a bool.
// Returns the value or false on failure.
func (s *signal) Bool() bool {
	val := strings.ToLower(s.String())
	return val == "true" || val == "1" || val == "yes" || val == "on"
}

// Int tries to read the signal value as an int.
// Returns the value or 0 on failure.
func (s *signal) Int() int {
	if n, err := strconv.Atoi(s.String()); err == nil {
		return n
	}
	return 0
}

// Int64 tries to read the signal value as an int64.
// Returns the value or 0 on failure.
func (s *signal) Int64() int64 {
	if n, err := strconv.ParseInt(s.String(), 10, 64); err == nil {
		return n
	}
	return 0
}

// Float64 tries to read the signal value as a float64.
// Returns the value or 0.0 on failure.
func (s *signal) Float() float64 {
	if n, err := strconv.ParseFloat(s.String(), 64); err == nil {
		return n
	}
	return 0.0
}

// Bytes tries to read the signal value as a []byte
// Returns the value or an empty []byte on failure.
func (s *signal) Bytes() []byte {
	return []byte(s.String())
}
