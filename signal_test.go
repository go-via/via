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

		c.View(func(ctx *Context) h.H {
			// All methods are type-safe
			assert.IsType(t, 0, count.Get(ctx))
			assert.IsType(t, "", name.Get(ctx))
			assert.IsType(t, true, enabled.Get(ctx))

			return h.Div()
		})
	})
}

// TestSignal_GetReturnsInitial verifies initial values
func TestSignal_GetReturnsInitial(t *testing.T) {
	v := New()

	v.Page("/", func(c *Composition) {
		count := Signal(c, 42)

		c.View(func(ctx *Context) h.H {
			assert.Equal(t, 42, count.Get(ctx))
			return h.Div()
		})
	})
}

// TestSignal_SetUpdatesValue verifies signal mutation
func TestSignal_SetUpdatesValue(t *testing.T) {
	ctx := NewContext(nil)
	count := Signal(&Composition{signals: []signalRegistration{}}, 0)

	count.Set(ctx, 99)
	got := count.Get(ctx)

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

// Test Signal.Set in view mode warns
func TestSignal_SetInViewModeWarns(t *testing.T) {
	var warned bool
	var warnMsg string
	warnFn := func(format string, args ...any) {
		warned = true
		warnMsg = format
	}

	ctx := &Context{
		store: newStore(),
		mode:  sessionModeView,
		warn:  warnFn,
	}

	c := &Composition{
		signals: []signalRegistration{},
	}
	signal := Signal(c, 42)

	signal.Set(ctx, 100)

	assert.True(t, warned, "Expected warning when Signal.Set called in view mode")
	assert.Contains(t, warnMsg, "SignalHandle.Set()")

	// Value should NOT be set
	got := signal.Get(ctx)
	assert.Equal(t, 42, got, "Value should remain at initial (not mutated in view mode)")
}

// Test that Get returns initial value instead of panicking on invalid type
func TestSignal_GetInvalidTypeReturnsInitial(t *testing.T) {
	c := &Composition{
		signals: []signalRegistration{},
	}
	signal := Signal(c, 42)

	ctx := NewContext(nil)
	// Manually inject an invalid type (a struct instead of int)
	ctx.store.signals[signal.id] = struct{}{}

	// Should return initial value, not panic
	got := signal.Get(ctx)
	assert.Equal(t, 42, got, "Should return initial value on invalid type")
}

// Test Get handles float64 conversion (JSON unmarshaling)
func TestSignal_GetFloat64Conversion(t *testing.T) {
	c := &Composition{
		signals: []signalRegistration{},
	}

	// Test various numeric types with float64 input
	tests := []struct {
		name     string
		signal   any
		input    float64
		expected any
	}{
		{"int", Signal(c, 0), 42.0, 42},
		{"int8", Signal(c, int8(0)), 42.0, int8(42)},
		{"int16", Signal(c, int16(0)), 42.0, int16(42)},
		{"int32", Signal(c, int32(0)), 42.0, int32(42)},
		{"int64", Signal(c, int64(0)), 42.0, int64(42)},
		{"uint", Signal(c, uint(0)), 42.0, uint(42)},
		{"uint8", Signal(c, uint8(0)), 42.0, uint8(42)},
		{"uint16", Signal(c, uint16(0)), 42.0, uint16(42)},
		{"uint32", Signal(c, uint32(0)), 42.0, uint32(42)},
		{"uint64", Signal(c, uint64(0)), 42.0, uint64(42)},
		{"float32", Signal(c, float32(0)), 3.14, float32(3.14)},
		{"float64", Signal(c, float64(0)), 3.14, 3.14},
		{"bool from 1", Signal(c, false), 1.0, true},
		{"bool from 0", Signal(c, true), 0.0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext(nil)
			switch sig := tt.signal.(type) {
			case *SignalHandle[int]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[int8]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[int16]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[int32]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[int64]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[uint]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[uint8]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[uint16]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[uint32]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[uint64]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[float32]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[float64]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[bool]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			}
		})
	}
}

// Test Get handles string conversion
func TestSignal_GetStringConversion(t *testing.T) {
	c := &Composition{
		signals: []signalRegistration{},
	}

	tests := []struct {
		name     string
		signal   any
		input    string
		expected any
	}{
		{"int", Signal(c, 0), "42", 42},
		{"int8", Signal(c, int8(0)), "42", int8(42)},
		{"int16", Signal(c, int16(0)), "42", int16(42)},
		{"int32", Signal(c, int32(0)), "42", int32(42)},
		{"int64", Signal(c, int64(0)), "42", int64(42)},
		{"uint", Signal(c, uint(0)), "42", uint(42)},
		{"uint8", Signal(c, uint8(0)), "42", uint8(42)},
		{"uint16", Signal(c, uint16(0)), "42", uint16(42)},
		{"uint32", Signal(c, uint32(0)), "42", uint32(42)},
		{"uint64", Signal(c, uint64(0)), "42", uint64(42)},
		{"float32", Signal(c, float32(0)), "3.14", float32(3.14)},
		{"float64", Signal(c, float64(0)), "3.14", 3.14},
		{"bool true", Signal(c, false), "true", true},
		{"bool false", Signal(c, true), "false", false},
		{"string", Signal(c, ""), "hello", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext(nil)
			switch sig := tt.signal.(type) {
			case *SignalHandle[int]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[int8]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[int16]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[int32]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[int64]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[uint]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[uint8]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[uint16]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[uint32]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[uint64]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[float32]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[float64]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[bool]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			case *SignalHandle[string]:
				ctx.store.signals[sig.id] = tt.input
				assert.Equal(t, tt.expected, sig.Get(ctx))
			}
		})
	}
}

// Test Get returns initial on invalid string value
func TestSignal_GetInvalidStringReturnsInitial(t *testing.T) {
	c := &Composition{
		signals: []signalRegistration{},
	}
	signal := Signal(c, 42)

	ctx := NewContext(nil)
	// Inject invalid string for int
	ctx.store.signals[signal.id] = "not-a-number"

	// Should return initial value, not panic or zero
	got := signal.Get(ctx)
	assert.Equal(t, 42, got, "Should return initial value on invalid string")
}
