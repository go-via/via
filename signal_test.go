package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSignal_stringReturnsFormattedValue verifies String() renders the stored value correctly.
// This guards against signal value loss or type formatting regressions on construction.
func TestSignal_stringReturnsFormattedValue(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"string", "hello", "hello"},
		{"int", 42, "42"},
		{"float", 3.14, "3.14"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sig := captureSignal(func(c *via.Context) signalT { return c.Signal(tt.input) })
			assert.Equal(t, tt.want, sig.String())
		})
	}
}

// TestSignal_boolParsesVariants verifies Bool() handles all truthy/falsy string variants.
// This guards against regressions in browser-side signal value representation.
func TestSignal_boolParsesVariants(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"true", "true", true},
		{"1", "1", true},
		{"yes", "yes", true},
		{"on", "on", true},
		{"TRUE", "TRUE", true},
		{"false", "false", false},
		{"0", "0", false},
		{"no", "no", false},
		{"off", "off", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sig := captureSignal(func(c *via.Context) signalT { return c.Signal(tt.input) })
			assert.Equal(t, tt.want, sig.Bool())
		})
	}
}

// TestSignal_intReturnsZeroOnInvalid verifies Int() returns 0 for non-numeric values.
func TestSignal_intReturnsZeroOnInvalid(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal("abc") })
	assert.Equal(t, 0, sig.Int())
}

// TestSignal_int64ReturnsZeroOnInvalid verifies Int64() returns 0 for non-numeric values.
func TestSignal_int64ReturnsZeroOnInvalid(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal("abc") })
	assert.Equal(t, int64(0), sig.Int64())
}

// TestSignal_floatReturnsZeroOnInvalid verifies Float() returns 0.0 for non-numeric values.
func TestSignal_floatReturnsZeroOnInvalid(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal("abc") })
	assert.Equal(t, 0.0, sig.Float())
}

// TestSignal_bytesReturnsStringBytes verifies Bytes() returns the UTF-8 encoding of String().
func TestSignal_bytesReturnsStringBytes(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal("hello") })
	assert.Equal(t, []byte("hello"), sig.Bytes())
}

// TestSignal_bindRendersDataBindAttr verifies Bind() produces a data-bind attribute referencing the signal ID.
func TestSignal_bindRendersDataBindAttr(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal("x") })
	out := renderH(t, h.Input(sig.Bind()))
	assert.Contains(t, out, "data-bind")
	assert.Contains(t, out, sig.ID())
}

// TestSignal_textRendersDataTextSpan verifies Text() produces a span with data-text referencing the signal ID.
func TestSignal_textRendersDataTextSpan(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal("world") })
	out := renderH(t, h.Div(sig.Text()))
	assert.Contains(t, out, "<span")
	assert.Contains(t, out, "data-text")
	assert.Contains(t, out, sig.ID())
}

// TestSignal_intReturnsValidInt verifies Int() returns the correct value for an integer signal.
func TestSignal_intReturnsValidInt(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal(42) })
	assert.Equal(t, 42, sig.Int())
}

// TestSignal_int64ReturnsValidInt64 verifies Int64() returns the correct value for an integer signal.
func TestSignal_int64ReturnsValidInt64(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal(42) })
	assert.Equal(t, int64(42), sig.Int64())
}

// TestSignal_floatReturnsValidFloat verifies Float() returns the correct value for a float signal.
func TestSignal_floatReturnsValidFloat(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal(3.14) })
	assert.Equal(t, 3.14, sig.Float())
}

// TestSignal_errReturnsNilOnValidSignal verifies Err() is nil for a valid signal.
func TestSignal_errReturnsNilOnValidSignal(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal("x") })
	assert.NoError(t, sig.Err())
}

// TestSignal_setValueUpdatesStringOutput verifies SetValue updates the signal's string representation.
func TestSignal_setValueUpdatesStringOutput(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal("old") })
	sig.SetValue("new")
	assert.Equal(t, "new", sig.String())
}

// TestSignal_setValueClearsErr verifies SetValue clears a previously set error.
func TestSignal_setValueClearsErr(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal(nil) })
	require.Error(t, sig.Err())
	sig.SetValue("recovered")
	assert.NoError(t, sig.Err())
}

// TestSignal_boolFromNativeBool verifies Bool() returns true when signal is initialized with a native bool.
func TestSignal_boolFromNativeBool(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal(true) })
	assert.True(t, sig.Bool())
}

// TestSignal_sliceSerializesToJSON verifies that a slice signal is JSON-serialized on construction.
func TestSignal_sliceSerializesToJSON(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal([]string{"a", "b"}) })
	assert.Contains(t, sig.String(), `["a","b"]`)
}

// TestSignal_structSerializesToJSON verifies that a struct signal is JSON-serialized on construction.
func TestSignal_structSerializesToJSON(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return c.Signal(struct{ N int }{42}) })
	assert.Contains(t, sig.String(), "42")
}

// TestSignal_idReturnsNonEmpty verifies every signal gets a unique non-empty ID.
func TestSignal_idReturnsNonEmpty(t *testing.T) {
	v := via.New()
	var idA, idB string
	v.Page("/", func(c *via.Context) {
		a := c.Signal("a")
		b := c.Signal("b")
		idA = a.ID()
		idB = b.ID()
		c.View(func() h.H { return h.Div() })
	})
	require.NotEmpty(t, idA)
	require.NotEmpty(t, idB)
	assert.NotEqual(t, idA, idB)
}
