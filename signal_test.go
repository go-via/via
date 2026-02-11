package via

import (
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

// TestSignal_TypeSafe demonstrates type-safe signal creation
func TestSignal_TypeSafe(t *testing.T) {
	v := New()

	v.Page("/", func(c *Composition) {
		// Type inference works - no casting needed
		count := Signal(c, 0)      // *SignalHandle[int]
		name := Signal(c, "")      // *SignalHandle[string]
		enabled := Signal(c, true) // *SignalHandle[bool]

		c.View(func(s *Session) h.H {
			// All methods are type-safe
			assert.IsType(t, 0, count.Get(s))
			assert.IsType(t, "", name.Get(s))
			assert.IsType(t, true, enabled.Get(s))

			return h.Div()
		})
	})
}

// TestSignal_GetReturnsInitial verifies initial values
func TestSignal_GetReturnsInitial(t *testing.T) {
	v := New()

	v.Page("/", func(c *Composition) {
		count := Signal(c, 42)

		c.View(func(s *Session) h.H {
			assert.Equal(t, 42, count.Get(s))
			return h.Div()
		})
	})
}

// TestSignal_SetUpdatesValue verifies signal mutation
func TestSignal_SetUpdatesValue(t *testing.T) {
	s := NewSession()
	count := Signal(&Composition{signals: []signalRegistration{}}, 0)

	count.Set(s, 99)
	got := count.Get(s)

	assert.Equal(t, 99, got)
}

// TestSignal_BindHelpers verifies DSL helpers
func TestSignal_BindHelpers(t *testing.T) {
	c := &Composition{signals: []signalRegistration{}}
	name := Signal(c, "test")

	// Verify helper methods return correct data attributes
	bind := name.Bind()
	text := name.Text()
	show := name.Show()

	assert.Contains(t, renderToString(bind), `data-bind="`)
	assert.NotContains(t, renderToString(bind), `data-bind="$`)
	assert.Contains(t, renderToString(text), `data-text="$`)
	assert.Contains(t, renderToString(show), `data-show="$`)
}

// TestSignal_MultipleTypes verifies all supported types
func TestSignal_MultipleTypes(t *testing.T) {
	c := &Composition{signals: []signalRegistration{}}

	tests := []struct {
		name    string
		initial any
	}{
		{"int", 42},
		{"int8", int8(42)},
		{"int16", int16(42)},
		{"int32", int32(42)},
		{"int64", int64(42)},
		{"uint", uint(42)},
		{"uint8", uint8(42)},
		{"uint16", uint16(42)},
		{"uint32", uint32(42)},
		{"uint64", uint64(42)},
		{"float32", float32(3.14)},
		{"float64", 3.14},
		{"string", "hello"},
		{"bool", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This should compile without type assertion
			switch v := tt.initial.(type) {
			case int:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case int8:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case int16:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case int32:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case int64:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case uint:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case uint8:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case uint16:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case uint32:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case uint64:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case float32:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case float64:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case string:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			case bool:
				sig := Signal(c, v)
				assert.Equal(t, v, sig.initial)
			}
		})
	}
}

func renderToString(node h.H) string {
	buf := &bufferWriter{buf: make([]byte, 0, 256)}
	_ = node.Render(buf)
	return string(buf.buf)
}
