package h_test

import (
	"strings"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func TestBoolAttr_offRendersNothingAtAll(t *testing.T) {
	t.Parallel()
	got := render(t, h.Button(h.Disabled(false), h.Str("go")))
	assert.NotContains(t, got, "disabled", "an off boolean attribute must not appear in the tag in any form")
	assert.Equal(t, "<button>go</button>", got)
}

func TestBoolAttr_onRendersTheBareName(t *testing.T) {
	t.Parallel()
	got := render(t, h.Button(h.Disabled(true), h.Str("go")))
	assert.Equal(t, "<button disabled>go</button>", got)
}

func TestBoolAttrs_everyHelperIsPresentOrAbsent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		fn   func(bool) h.Attr
	}{
		{"disabled", h.Disabled},
		{"checked", h.Checked},
		{"required", h.Required},
		{"readonly", h.ReadOnly},
		{"selected", h.Selected},
		{"multiple", h.Multiple},
		{"autofocus", h.AutoFocus},
		{"hidden", h.Hidden},
		{"open", h.Open},
		{"novalidate", h.NoValidate},
		{"async", h.Async},
		{"defer", h.Defer},
		{"inert", h.Inert},
		{"loop", h.Loop},
		{"muted", h.Muted},
		{"controls", h.Controls},
		{"playsinline", h.PlaysInline},
		{"reversed", h.Reversed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, "<div "+tt.name+"></div>", render(t, h.Div(tt.fn(true))), tt.name+" on")
			assert.Equal(t, "<div></div>", render(t, h.Div(tt.fn(false))), tt.name+" off")
		})
	}
}

func TestValueAttrs_eachRendersItsOwnName(t *testing.T) {
	t.Parallel()
	for want, attr := range map[string]h.Attr{
		`id="x"`:                        h.ID("x"),
		`style="color:red"`:             h.Style("color:red"),
		`type="text"`:                   h.Type("text"),
		`name="body"`:                   h.Name("body"),
		`value="7"`:                     h.Value(7),
		`placeholder="say something"`:   h.Placeholder("say something"),
		`title="tip"`:                   h.Title("tip"),
		`alt="a cat"`:                   h.Alt("a cat"),
		`rel="noopener"`:                h.Rel("noopener"),
		`for="field"`:                   h.For("field"),
		`target="_blank"`:               h.Target("_blank"),
		`lang="en"`:                     h.Lang("en"),
		`role="button"`:                 h.Role("button"),
		`method="post"`:                 h.Method("post"),
		`enctype="multipart/form-data"`: h.Enctype("multipart/form-data"),
		`accept=".png"`:                 h.Accept(".png"),
		`autocomplete="off"`:            h.AutoComplete("off"),
		`width="100%"`:                  h.Width("100%"),
		`height="40"`:                   h.Height(40),
		`min="1"`:                       h.Min(1),
		`max="10"`:                      h.Max(10),
		`step="0.5"`:                    h.Step(0.5),
		`rows="4"`:                      h.Rows(4),
		`cols="80"`:                     h.Cols(80),
		`maxlength="140"`:               h.MaxLength(140),
		`minlength="3"`:                 h.MinLength(3),
		`colspan="2"`:                   h.ColSpan(2),
		`rowspan="3"`:                   h.RowSpan(3),
		`tabindex="-1"`:                 h.TabIndex(-1),
	} {
		assert.Equal(t, "<div "+want+"></div>", render(t, h.Div(attr)), want)
	}
}

func TestValue_stringishTypesAgree(t *testing.T) {
	t.Parallel()
	assert.Equal(t, render(t, h.Div(h.Value("7"))), render(t, h.Div(h.Value(7))))
	assert.Equal(t, render(t, h.Div(h.Value("7"))), render(t, h.Div(h.Value(uint8(7)))))
}

func TestClass_joinsAndDropsEmpties(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `<div class="card wide"></div>`, render(t, h.Div(h.Class("card", "wide"))))
	assert.Equal(t, `<div class="card"></div>`, render(t, h.Div(h.Class("", "card", ""))))
	assert.Equal(t, `<div></div>`, render(t, h.Div(h.Class("", ""))), "no surviving class means no attribute")
	assert.Equal(t, `<div></div>`, render(t, h.Div(h.Class())), "no arguments means no attribute")
}

func TestValueAttrs_stillEscapeTheValue(t *testing.T) {
	t.Parallel()
	for _, attr := range []h.Attr{
		h.Style(`x" onmouseover="alert(1)`),
		h.Title(`x" onmouseover="alert(1)`),
		h.Class(`x" onmouseover="alert(1)`),
		h.Value(`x" onmouseover="alert(1)`),
	} {
		got := render(t, h.Div(attr))
		assert.NotContains(t, got, `onmouseover="`, "a quote in the value must not break out of the attribute")
		assert.True(t, strings.Contains(got, "&#34;"), "the quote must be escaped: "+got)
	}
}

func TestAria_rendersPrefixedName(t *testing.T) {
	t.Parallel()
	assert.Contains(t, render(t, h.Button(h.Aria("label", "Close"))), `aria-label="Close"`)
}

func TestAria_panicsOnInvalidName(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { h.Aria("bad name", "x") })
	assert.Panics(t, func() { h.Aria("", "x") })
}
