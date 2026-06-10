package h_test

import (
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func TestAria_emitsPrefixedHTMLEscapedAttribute(t *testing.T) {
	t.Parallel()
	got := render(t, h.Button(h.Aria("label", `Cl<o>"se`)))
	assert.Contains(t, got, `aria-label="Cl&lt;o&gt;&#34;se"`)
}

func TestShorthands_emitNamedHTMLEscapedAttribute(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		attr h.H
		want string
	}{
		{"alt", h.Alt("a&b"), `alt="a&amp;b"`},
		{"width", h.Width("100"), `width="100"`},
		{"height", h.Height("50"), `height="50"`},
		{"target", h.Target("_blank"), `target="_blank"`},
		{"action", h.Action("/submit"), `action="/submit"`},
		{"method", h.Method("post"), `method="post"`},
		{"autocomplete", h.AutoComplete("email"), `autocomplete="email"`},
		{"tabindex", h.TabIndex("0"), `tabindex="0"`},
		{"colspan", h.ColSpan("2"), `colspan="2"`},
		{"rowspan", h.RowSpan("3"), `rowspan="3"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, render(t, h.Td(tt.attr)), tt.want)
		})
	}
}

func TestConstraintAttrs_emitNativeFormValidationAttributes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		attr h.H
		want string
	}{
		{"pattern", h.Pattern(`[a-z]+`), `pattern="[a-z]+"`},
		{"minlength", h.MinLength(3), `minlength="3"`},
		{"maxlength", h.MaxLength(60), `maxlength="60"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, render(t, h.Input(tt.attr)), tt.want)
		})
	}
}

func TestAttrNum_formatsIntegersAndFloatsWithoutManualConversion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		attr h.H
		want string
	}{
		{"int", h.AttrNum("data-count", 42), `data-count="42"`},
		{"negative int", h.AttrNum("data-delta", -7), `data-delta="-7"`},
		{"float", h.AttrNum("data-ratio", 0.5), `data-ratio="0.5"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, render(t, h.Div(tt.attr)), tt.want)
		})
	}
}

func TestNumericRangeSiblings_avoidStrconvAtCallSite(t *testing.T) {
	t.Parallel()
	got := render(t, h.Input(
		h.MinNum(0),
		h.MaxNum(10),
		h.StepNum(0.5),
		h.ValueNum(3),
	))
	assert.Contains(t, got, `min="0"`)
	assert.Contains(t, got, `max="10"`)
	assert.Contains(t, got, `step="0.5"`)
	assert.Contains(t, got, `value="3"`)
}
