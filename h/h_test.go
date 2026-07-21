package h_test

import (
	"strconv"
	"testing"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
	"github.com/stretchr/testify/assert"
)

// stubBinder is a minimal Binder for exercising the renderer without the via
// package. It is a genuine in-package double for the I/O-free slot allocator.
type stubBinder struct {
	nextSig int
	init    map[string]any
	actions int
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

func (b *stubBinder) ActionSlot(func()) string {
	i := b.actions
	b.actions++
	return strconv.Itoa(i)
}

func render(t *testing.T, node h.H) string {
	t.Helper()
	r := hcore.NewRenderer(&stubBinder{})
	r.Render(node)
	return r.String()
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
		node h.H
		want string
	}{
		{h.Div(), "<div></div>"},
		{h.Span(), "<span></span>"},
		{h.H1(), "<h1></h1>"},
		{h.Button(), "<button></button>"},
		{h.Main(), "<main></main>"},
		{h.El("section"), "<section></section>"},
	} {
		assert.Equal(t, tc.want, render(t, tc.node))
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

func TestBinder_isExposedSoDynamicNodesCanClaimSlots(t *testing.T) {
	t.Parallel()
	// via's signal/action nodes reach the Binder through r.Binder(); the
	// renderer must hand back the exact binder it was built with.
	b := &stubBinder{}
	r := hcore.NewRenderer(b)
	assert.Same(t, b, r.Binder(), "Binder() did not return the injected binder")
}

func TestBytes_matchesStringForZeroCopyWriting(t *testing.T) {
	t.Parallel()
	// via writes the rendered tree straight to the ResponseWriter via Bytes()
	// to avoid a string copy; it must equal String().
	r := hcore.NewRenderer(&stubBinder{})
	r.Render(h.Span(h.Str("x")))
	assert.Equal(t, r.String(), string(r.Bytes()), "Bytes() must equal String()")
}

func TestWriteEscapedAndWriteString_distinguishRawFromEscaped(t *testing.T) {
	t.Parallel()
	r := hcore.NewRenderer(&stubBinder{})
	r.WriteString("<b>")  // raw, caller pre-escaped
	r.WriteEscaped("<b>") // must be escaped
	assert.Equal(t, "<b>&lt;b&gt;", r.String())
}

// The full-vocabulary sweep: one constructor per HTML5 tag h ships, each must
// render its own tag; void elements must self-close (no closing tag). Fails if
// a constructor maps to the wrong tag or a void grows a body.
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

// Typed URL attributes go through safeURL: http/https/relative pass verbatim;
// javascript:, data:, protocol-relative (//) are neutralized to "#" — never
// shipped. Fails if the gate opens or a good URL is mangled.
func TestURLAttrs_neutralizeUnsafeSchemes(t *testing.T) {
	t.Parallel()
	assert.Contains(t, render(t, h.A(h.Href("/threads/7"))), `href="/threads/7"`)
	assert.Contains(t, render(t, h.A(h.Href("https://example.com/x"))), `href="https://example.com/x"`)
	assert.Contains(t, render(t, h.A(h.Href("javascript:alert(1)"))), `href="#"`)
	assert.Contains(t, render(t, h.Img(h.Src("data:text/html,x"))), `src="#"`)
	assert.Contains(t, render(t, h.A(h.Href("//evil.example/x"))), `href="#"`)
	assert.Contains(t, render(t, h.Form(h.Action("/login"))), `action="/login"`)
}
