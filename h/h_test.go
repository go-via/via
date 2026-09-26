package h_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubBinder is a minimal Binder for exercising the renderer without the via
// package. It is a genuine in-package double for the I/O-free slot allocator.
type stubBinder struct {
	nextSig int
	init    map[string]any
}

func (b *stubBinder) SignalName() string {
	s := "s" + strconv.Itoa(b.nextSig)
	b.nextSig++
	return s
}

func (b *stubBinder) DeclareSignal(string, any) {}

func (b *stubBinder) SignalInit(slot string) (any, bool) {
	v, ok := b.init[slot]
	return v, ok
}

func (b *stubBinder) Hydrator(string, func(json.RawMessage) bool) {}

func render(t *testing.T, node h.H) string {
	t.Helper()
	r := hcore.NewRenderer(&stubBinder{})
	r.Render(node)
	return string(r.Bytes())
}

func TestChildText_isHTMLEscapedToPreventInjection(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Str("<script>&\"'")))
	assert.NotContains(t, got, "<script>", "raw <script> leaked into output, XSS risk")
	assert.Equal(t, "<div>&lt;script&gt;&amp;&#34;&#39;</div>", got, "escaping mismatch")
}

func TestNumericText_rendersAsItsDecimalForm(t *testing.T) {
	t.Parallel()
	got := render(t, h.Span(h.Str(42)))
	assert.Equal(t, "<span>42</span>", got)
}

func TestAttributes_renderInsideOpeningTagAndChildrenInBody(t *testing.T) {
	t.Parallel()
	// Attr children must land in the opening tag regardless of their position
	// among node children; node children stay in the body in order.
	got := render(t, h.Div(
		h.Str("a"),
		h.RawAttr("id", "x"),
		h.Str("b"),
	))
	assert.Equal(t, `<div id="x">ab</div>`, got, "attr/node partition wrong")
}

func TestAttributeValues_areEscapedToPreventTagBreakout(t *testing.T) {
	t.Parallel()
	got := render(t, h.Span(h.RawAttr("title", `"><script>`)))
	assert.NotContains(t, got, `"><script>`, "attr value broke out of the quoted tag")
	assert.Equal(t, `<span title="&#34;&gt;&lt;script&gt;"></span>`, got)
}

func TestDataHelper_emitsEscapedDataAttribute(t *testing.T) {
	t.Parallel()
	got := render(t, h.Input(h.Data("signals", `{"s0":1}`)))
	assert.Equal(t, `<input data-signals="{&#34;s0&#34;:1}">`, got)
}

func TestVoidElement_selfClosesWithoutBody(t *testing.T) {
	t.Parallel()
	// input is a void element: no closing tag, children-as-nodes dropped.
	got := render(t, h.Input(h.RawAttr("type", "text")))
	assert.Equal(t, `<input type="text">`, got, "void element rendered with a body")
}

func TestNonVoidElements_alwaysEmitClosingTag(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		node h.H
		want string
	}{
		{"div", h.Div(), "<div></div>"},
		{"span", h.Span(), "<span></span>"},
		{"h1", h.H1(), "<h1></h1>"},
		{"button", h.Button(), "<button></button>"},
		{"main", h.Main(), "<main></main>"},
		{"custom element", h.El("section"), "<section></section>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, render(t, tc.node))
		})
	}
}

func TestNestedElements_renderRecursively(t *testing.T) {
	t.Parallel()
	got := render(t, h.Main(h.Div(h.H1(h.Str("hi")))))
	assert.Equal(t, "<main><div><h1>hi</h1></div></main>", got)
}

func TestMultipleAttributes_keepSourceOrder(t *testing.T) {
	t.Parallel()
	got := render(t, h.El("a",
		h.RawAttr("href", "/x"),
		h.RawAttr("rel", "next"),
		h.Str("go"),
	))
	assert.Equal(t, `<a href="/x" rel="next">go</a>`, got)
}

func TestElements_fullVocabularyRendersItsTags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		node h.H
		want string
	}{
		{h.A(), "<a></a>"}, {h.Table(), "<table></table>"}, {h.Tr(), "<tr></tr>"},
		{h.Td(), "<td></td>"}, {h.Nav(), "<nav></nav>"}, {h.Section(), "<section></section>"},
		{h.Article(), "<article></article>"}, {h.Textarea(), "<textarea></textarea>"},
		{h.Select(), "<select></select>"}, {h.Option(), "<option></option>"},
		{h.H6(), "<h6></h6>"}, {h.Video(), "<video></video>"}, {h.Canvas(), "<canvas></canvas>"},
		{h.Img(), "<img>"}, {h.Br(), "<br>"}, {h.Hr(), "<hr>"}, {h.Wbr(), "<wbr>"},
		{h.Source(), "<source>"}, {h.Track(), "<track>"}, {h.Embed(), "<embed>"},
		{h.Col(), "<col>"}, {h.Area(), "<area>"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, render(t, c.node))
	}
}

func TestURLAttrs_neutralizeUnsafeSchemes(t *testing.T) {
	t.Parallel()
	assert.Contains(t, render(t, h.A(h.Href("/threads/7"))), `href="/threads/7"`)
	assert.Contains(t, render(t, h.A(h.Href("https://example.com/x"))), `href="https://example.com/x"`)
	assert.Contains(t, render(t, h.A(h.Href("javascript:alert(1)"))), `href="#"`)
	assert.Contains(t, render(t, h.Img(h.Src("data:text/html,x"))), `src="#"`)
	assert.Contains(t, render(t, h.A(h.Href("//evil.example/x"))), `href="#"`)
	assert.Contains(t, render(t, h.Form(h.Action("/login"))), `action="/login"`)
}

func TestRawAttr_rejectsNamesThatCanBreakOutOfTheTag(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		`x" onmouseover="alert(1)`, // quote + space: classic attribute graft
		"on click",                 // bare space starts a new attribute
		`a=b`,                      // '=' smuggles a value
		">",                        // closes the tag early
		"data-x\ty",                // tab is whitespace too
		"",                         // empty name is meaningless
		"1abc",                     // must start with a letter
		"naïve",                    // non-ASCII outside the allowlist
	} {
		assert.Panicsf(t, func() { h.RawAttr(name, "v") },
			"RawAttr(%q) must panic — it is an attribute-injection vector", name)
	}
}

func TestText_escapesCarriageReturnThatWouldSplitAnSSEFrame(t *testing.T) {
	t.Parallel()
	got := render(t, h.Span(h.Str("before\rafter")))
	assert.NotContains(t, got, "\r", "a bare CR splits an SSE data line")
	assert.Contains(t, got, "&#13;")
}

func TestData_rejectsInjectableSuffix(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{`x" onclick="evil`, "a b", ">", "", "-x"} {
		assert.Panicsf(t, func() { h.Data(suffix, "v") },
			"Data(%q) must panic — suffix is an injection vector", suffix)
	}
}

func TestRawAttr_acceptsOrdinaryHTMLAttributeNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, want string }{
		{"href", `<a href="x"></a>`},
		{"aria-label", `<a aria-label="x"></a>`},
		{"data-foo-bar", `<a data-foo-bar="x"></a>`},
		{"x2", `<a x2="x"></a>`},
		{"a", `<a a="x"></a>`},
		{"HREF", `<a HREF="x"></a>`},
		{"x-", `<a x-="x"></a>`},
	} {
		var got string
		require.NotPanicsf(t, func() {
			got = render(t, h.El("a", h.RawAttr(tc.name, "x")))
		}, "RawAttr(%q) must be accepted", tc.name)
		assert.Equal(t, tc.want, got)
	}
}

func TestData_rendersDatastarPluginNamesVerbatim(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ suffix, val, want string }{
		{"on:click", "@post('/x')", `data-on:click="@post(&#39;/x&#39;)"`},
		{"on:keydown__debounce.300ms", "@get('/s')", `data-on:keydown__debounce.300ms="@get(&#39;/s&#39;)"`},
		{"class:active", "$open", `data-class:active="$open"`},
		{"attr:disabled", "$busy", `data-attr:disabled="$busy"`},
		{"show", "$open", `data-show="$open"`},
	} {
		got := render(t, h.Div(h.Data(tc.suffix, tc.val)))
		assert.Containsf(t, got, tc.want, "h.Data(%q) must render the Datastar attribute verbatim", tc.suffix)

		raw := render(t, h.Div(h.RawAttr("data-"+tc.suffix, tc.val)))
		assert.Containsf(t, raw, tc.want, "h.RawAttr(%q) must render the Datastar attribute verbatim", "data-"+tc.suffix)
	}
}

func TestRawAttr_rejectsInlineEventHandlers(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"onclick", "onerror", "ONCLICK", "onload", "OnMouseOver", "onfocus"} {
		assert.Panicsf(t, func() { h.RawAttr(name, "alert(1)") },
			"RawAttr(%q) must panic — inline event handlers are script sinks", name)
		assert.Panicsf(t, func() { hcore.BoolAttr(name, true) },
			"BoolAttr(%q) must panic — inline event handlers are script sinks", name)
	}
	// The prefix rule must not swallow data-on:*, which is Datastar, not a DOM
	// handler, and must not reject legitimate names that merely start with "o".
	assert.NotPanics(t, func() { h.RawAttr("data-on:click", "@post('/x')") })
	assert.NotPanics(t, func() { h.RawAttr("open", "") })
}

func TestRawAttr_rejectsPluginPunctuationOutsideDataAttrs(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"xmlns:foo", "a_b", "a.b", "data-", "data-:x"} {
		assert.Panicsf(t, func() { h.RawAttr(name, "v") }, "RawAttr(%q) must panic", name)
	}
}

func maybe(show bool) h.H {
	if !show {
		return nil
	}
	return h.Span(h.Str("shown"))
}

func TestEl_rendersANilChildAsNothing(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "<div><b></b></div>", render(t, h.Div(nil, h.B(), nil)))
}

func TestEl_rendersANilFromAHelperAsNothing(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "<div><span>shown</span></div>", render(t, h.Div(maybe(true), maybe(false))))
}

func TestEl_panicsOnAScriptTagInAnyCase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tag  string
	}{
		{"lower case", "script"},
		{"upper case", "SCRIPT"},
		{"mixed case", "Script"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.PanicsWithError(t,
				`h: El("`+tt.tag+`") is refused: Datastar gives a patched-in <script> the page's nonce, `+
					"so it would run; declare scripts in via.Meta.Assets",
				func() { h.El(tt.tag, h.Str("alert(1)")) })
		})
	}
}
