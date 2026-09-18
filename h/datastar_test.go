package h_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/go-via/via/h"
)

type ex string

func TestData_emitsTheDatastarAttributes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		attr h.Attr
		want string
	}{
		{"show", h.DataShow(ex("$open")), `data-show="$open"`},
		{"text", h.DataText(ex("$count")), `data-text="$count"`},
		{"class", h.DataClass("active", ex("$on")), `data-class:active="$on"`},
		{"attr", h.DataAttr("disabled", ex("$busy")), `data-attr:disabled="$busy"`},
		{"style", h.DataStyle("color", ex("$c")), `data-style:color="$c"`},
		{"on", h.DataOn("click", ex("$n += 1")), `data-on:click="$n += 1"`},
		{"on many", h.DataOn("click", ex("$a = 1"), ex("$b = 2")), `data-on:click="$a = 1; $b = 2"`},
		{"effect", h.DataEffect(ex("draw(el)")), `data-effect="draw(el)"`},
		{"computed", h.DataComputed("total", ex("$a + $b")), `data-computed:total="$a + $b"`},
		{"indicator", h.DataIndicator(ex("busy")), `data-indicator="busy"`},
		{"ref", h.DataRef(ex("box")), `data-ref="box"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, render(t, h.Div(tt.attr)), tt.want)
		})
	}
}

func TestDataIndicator_stripsOneLeadingDollar(t *testing.T) {
	t.Parallel()
	assert.Contains(t, render(t, h.Div(h.DataIndicator(ex("$busy")))), `data-indicator="busy"`)
	assert.Contains(t, render(t, h.Div(h.DataRef(ex("$box")))), `data-ref="box"`)
}

func TestData_escapesTheExpression(t *testing.T) {
	t.Parallel()
	assert.Contains(t, render(t, h.Div(h.DataShow(ex(`$q !== "x"`)))), `data-show="$q !== &#34;x&#34;"`)
}

func TestData_panicsOnNoStatements(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { h.DataOn[ex]("click") })
	assert.Panics(t, func() { h.DataEffect[ex]() })
}

func TestData_panicsOnAnInvalidName(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { h.DataClass("a b", ex("$on")) })
	assert.Panics(t, func() { h.DataOn("click\" onload=x", ex("$a")) })
}

func TestDataIgnoreMorph_rendersTheBareDatastarAttribute(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `<div id="chart" data-ignore-morph></div>`, render(t, h.Div(h.ID("chart"), h.DataIgnoreMorph())))
}
