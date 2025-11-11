package via

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildOnExpr(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		opts     triggerOpts
		expected string
	}{
		{
			name:     "no signal",
			base:     "@get('/_action/xyz')",
			opts:     triggerOpts{hasSignal: false},
			expected: "@get('/_action/xyz')",
		},
		{
			name: "with int signal",
			base: "@get('/_action/xyz')",
			opts: triggerOpts{
				hasSignal: true,
				signalID:  "abc123",
				value:     "42",
			},
			expected: "$abc123=42;@get('/_action/xyz')",
		},
		{
			name: "with string signal",
			base: "@get('/_action/xyz')",
			opts: triggerOpts{
				hasSignal: true,
				signalID:  "def456",
				value:     "'hello'",
			},
			expected: "$def456='hello';@get('/_action/xyz')",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildOnExpr(tt.base, &tt.opts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOnClick_NoOption(t *testing.T) {
	trigger := &actionTrigger{id: "test123"}
	result := trigger.OnClick()
	assert.NotNil(t, result)
}

func TestOnClick_WithSignalInt(t *testing.T) {
	app := New()
	ctx := newContext("/test", app)
	ctx.signals = make(map[string]*signal)

	sig := ctx.Signal(0)
	trigger := &actionTrigger{id: "xyz"}

	result := trigger.OnClick(WithSignalInt(sig, 42))
	assert.NotNil(t, result)
}

func TestOnClick_WithSignalString(t *testing.T) {
	app := New()
	ctx := newContext("/test", app)
	ctx.signals = make(map[string]*signal)

	sig := ctx.Signal("")
	trigger := &actionTrigger{id: "xyz"}

	result := trigger.OnClick(WithSignal(sig, "new-value"))
	assert.NotNil(t, result)
}

func TestOnChange_WithSignal(t *testing.T) {
	app := New()
	ctx := newContext("/test", app)
	ctx.signals = make(map[string]*signal)

	sig := ctx.Signal("")
	trigger := &actionTrigger{id: "abc"}

	result := trigger.OnChange(WithSignal(sig, "changed"))
	assert.NotNil(t, result)
}

func TestOnKeyDown_WithSignal(t *testing.T) {
	app := New()
	ctx := newContext("/test", app)
	ctx.signals = make(map[string]*signal)

	sig := ctx.Signal(0)
	trigger := &actionTrigger{id: "def"}

	result := trigger.OnKeyDown("Enter", WithSignalInt(sig, 123))
	assert.NotNil(t, result)
}

func TestWithSignalInt(t *testing.T) {
	app := New()
	ctx := newContext("/test", app)
	ctx.signals = make(map[string]*signal)

	sig := ctx.Signal(0)
	opt := WithSignalInt(sig, 100)

	assert.NotNil(t, opt)

	var opts triggerOpts
	opt.apply(&opts)

	assert.True(t, opts.hasSignal)
	assert.Equal(t, sig.ID(), opts.signalID)
	assert.Equal(t, "100", opts.value)
}

func TestWithSignal_StringType(t *testing.T) {
	app := New()
	ctx := newContext("/test", app)
	ctx.signals = make(map[string]*signal)

	sig := ctx.Signal("")
	opt := WithSignal(sig, "test-value")

	assert.NotNil(t, opt)

	var opts triggerOpts
	opt.apply(&opts)

	assert.True(t, opts.hasSignal)
	assert.Equal(t, sig.ID(), opts.signalID)
	assert.Equal(t, "'test-value'", opts.value)
}
