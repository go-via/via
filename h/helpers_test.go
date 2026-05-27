package h_test

import (
	"strings"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

// Tests for the composition helpers and short-form constructors —
// T, Style/Styles, variadic Class, Tag/VoidTag/NewTag, Maybe, With,
// Static. Each test names one user-facing behaviour.

func r(t *testing.T, n h.H) string {
	t.Helper()
	if n == nil {
		return ""
	}
	var buf strings.Builder
	if err := n.Render(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// T (text shorthand)

func TestT_aliasesText(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "<h1>Counter</h1>", r(t, h.H1(h.T("Counter"))))
}

func TestT_emptyStringRendersNothing(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "<div></div>", r(t, h.Div(h.T(""))))
}

// Regression guard against a future swap of Style and StyleEl: Style
// must emit an inline `style="..."` attribute, not a <style> element.

func TestStyle_emitsInlineAttribute(t *testing.T) {
	t.Parallel()
	got := r(t, h.Div(h.Style("color:red"), h.T("x")))
	assert.Equal(t, `<div style="color:red">x</div>`, got,
		"Style must produce the inline style attribute, not the <style> element")
}

func TestStyleEl_emitsElement(t *testing.T) {
	t.Parallel()
	got := r(t, h.StyleEl(h.Raw("body{color:red}")))
	assert.Equal(t, "<style>body{color:red}</style>", got)
}

// Styles helper

func TestStyles_joinsWithSemicolon(t *testing.T) {
	t.Parallel()
	got := r(t, h.Div(h.Styles("flex:1", "padding:0.5rem"), h.T("x")))
	assert.Equal(t, `<div style="flex:1;padding:0.5rem">x</div>`, got)
}

func TestStyles_skipsEmptyParts(t *testing.T) {
	t.Parallel()
	got := r(t, h.Div(h.Styles("flex:1", "", h.IfStr(false, "skipped")), h.T("x")))
	assert.Equal(t, `<div style="flex:1">x</div>`, got,
		"empty segments must not produce trailing or leading separators")
}

func TestStyles_allEmptyProducesNoAttribute(t *testing.T) {
	t.Parallel()
	got := r(t, h.Div(h.Styles("", "")))
	assert.Equal(t, "<div></div>", got)
}

// Variadic Class

func TestClass_singleString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `<div class="btn"></div>`, r(t, h.Div(h.Class("btn"))))
}

func TestClass_variadicJoinsWithSpace(t *testing.T) {
	t.Parallel()
	got := r(t, h.Div(h.Class("btn", "primary", "lg")))
	assert.Equal(t, `<div class="btn primary lg"></div>`, got)
}

func TestClass_skipsEmptySegments(t *testing.T) {
	t.Parallel()
	got := r(t, h.Div(h.Class("btn", h.IfStr(false, "active"), "lg")))
	assert.Equal(t, `<div class="btn lg"></div>`, got)
}

// Tag / VoidTag / NewTag

func TestTag_emitsCustomElement(t *testing.T) {
	t.Parallel()
	got := r(t, h.Tag("my-card", h.Class("card"), h.T("body")))
	assert.Equal(t, `<my-card class="card">body</my-card>`, got)
}

func TestVoidTag_omitsClosingTag(t *testing.T) {
	t.Parallel()
	got := r(t, h.VoidTag("custom-icon", h.Attr("name", "star"), h.T("dropped")))
	assert.Equal(t, `<custom-icon name="star">`, got)
}

func TestNewTag_returnsReusableConstructor(t *testing.T) {
	t.Parallel()
	SVG := h.NewTag("svg")
	got := r(t, SVG(h.Attr("xmlns", "http://www.w3.org/2000/svg"), h.T("shapes")))
	assert.Equal(t, `<svg xmlns="http://www.w3.org/2000/svg">shapes</svg>`, got)
}

// Maybe

func TestMaybe_emitsWhenNonZero(t *testing.T) {
	t.Parallel()
	got := r(t, h.Maybe("alice", func(s string) h.H { return h.P(h.T("hi "), h.T(s)) }))
	assert.Equal(t, "<p>hi alice</p>", got)
}

func TestMaybe_dropsWhenZeroValue(t *testing.T) {
	t.Parallel()
	got := r(t, h.Maybe("", func(s string) h.H { return h.P(h.T(s)) }))
	assert.Equal(t, "", got)
}

func TestMaybe_dropsOnNilFn(t *testing.T) {
	t.Parallel()
	got := r(t, h.Maybe("x", nil))
	assert.Equal(t, "", got)
}

// With

func TestWith_appendsToExistingElement(t *testing.T) {
	t.Parallel()
	card := h.Div(h.Class("card"), h.T("body"))
	got := r(t, h.With(card, h.ID("c1")))
	assert.Equal(t, `<div class="card" id="c1">body</div>`, got)
}

func TestWith_doesNotMutateBase(t *testing.T) {
	t.Parallel()
	// Two With calls extending the same base must each see only their
	// own extras — the base is shared so a mutation would leak between
	// them.
	base := h.Div(h.Class("base"), h.T("x"))
	a := r(t, h.With(base, h.ID("a")))
	b := r(t, h.With(base, h.ID("b")))
	assert.Equal(t, `<div class="base" id="a">x</div>`, a)
	assert.Equal(t, `<div class="base" id="b">x</div>`, b)
	assert.Equal(t, `<div class="base">x</div>`, r(t, base))
}

func TestWith_nonElementWrapsInFragment(t *testing.T) {
	t.Parallel()
	got := r(t, h.Div(h.With(h.T("hello"), h.T(" world"))))
	assert.Equal(t, "<div>hello world</div>", got)
}

func TestWith_noExtrasReturnsBaseUnchanged(t *testing.T) {
	t.Parallel()
	got := r(t, h.With(h.Div(h.Class("c"), h.T("x"))))
	assert.Equal(t, `<div class="c">x</div>`, got)
}

// Static

func TestStatic_renderIsAlwaysIdentical(t *testing.T) {
	t.Parallel()
	frag := h.Static(h.Header(h.Nav(h.A(h.Href("/"), h.T("home")))))
	got1 := r(t, frag)
	got2 := r(t, frag)
	assert.Equal(t,
		`<header><nav><a href="/">home</a></nav></header>`, got1)
	assert.Equal(t, got1, got2)
}

func TestStatic_doesNotSeeLaterTreeMutation(t *testing.T) {
	t.Parallel()
	// Static captures bytes at construction time, so wrapping it later
	// must not re-evaluate the original tree.
	frag := h.Static(h.P(h.T("frozen")))
	got := r(t, h.Div(h.ID("x"), frag))
	assert.Equal(t, `<div id="x"><p>frozen</p></div>`, got)
}

func TestStatic_nilReturnsNil(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", r(t, h.Static(nil)))
}
