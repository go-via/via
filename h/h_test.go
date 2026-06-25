package h_test

import (
	"strings"
	"testing"

	"github.com/go-via/via/h"
)

// stubBinder is a minimal Binder for exercising the renderer without the via
// package. It is a genuine in-package double for the I/O-free slot allocator.
type stubBinder struct {
	nextSig int
	init    map[string]any
	actions int
}

func (b *stubBinder) SignalSlot() string {
	s := "s" + itoa(b.nextSig)
	b.nextSig++
	return s
}

func (b *stubBinder) SignalInit(slot string) (any, bool) {
	v, ok := b.init[slot]
	return v, ok
}

func (b *stubBinder) ActionSlot(fn func()) string {
	i := b.actions
	b.actions++
	return itoa(i)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		d = append([]byte{'-'}, d...)
	}
	return string(d)
}

func render(t *testing.T, node h.H) string {
	t.Helper()
	r := h.NewRenderer(&stubBinder{})
	r.Render(node)
	return r.String()
}

func TestChildTextIsHTMLEscapedToPreventInjection(t *testing.T) {
	got := render(t, h.Div(h.Str("<script>&\"'")))
	if strings.Contains(got, "<script>") {
		t.Fatalf("raw <script> leaked into output, XSS risk: %q", got)
	}
	want := "<div>&lt;script&gt;&amp;&#34;&#39;</div>"
	if got != want {
		t.Fatalf("escaping mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestNumericTextRendersAsItsDecimalForm(t *testing.T) {
	got := render(t, h.Span(h.Str(42)))
	if got != "<span>42</span>" {
		t.Fatalf("got %q", got)
	}
}

func TestAttributesRenderInsideOpeningTagAndChildrenInBody(t *testing.T) {
	// Attr children must land in the opening tag regardless of their position
	// among node children; node children stay in the body in order.
	got := render(t, h.Div(
		h.Str("a"),
		h.RawAttr("id", "x"),
		h.Str("b"),
	))
	want := `<div id="x">ab</div>`
	if got != want {
		t.Fatalf("attr/node partition wrong:\n got %q\nwant %q", got, want)
	}
}

func TestAttributeValuesAreEscapedToPreventTagBreakout(t *testing.T) {
	got := render(t, h.Span(h.RawAttr("title", `"><script>`)))
	if strings.Contains(got, `"><script>`) {
		t.Fatalf("attr value broke out of the quoted tag: %q", got)
	}
	want := `<span title="&#34;&gt;&lt;script&gt;"></span>`
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestDataHelperEmitsEscapedDataAttribute(t *testing.T) {
	got := render(t, h.Input(h.Data("signals", `{"s0":1}`)))
	want := `<input data-signals="{&#34;s0&#34;:1}">`
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestVoidElementSelfClosesWithoutBody(t *testing.T) {
	// input is a void element: no closing tag, children-as-nodes dropped.
	got := render(t, h.Input(h.RawAttr("type", "text")))
	want := `<input type="text">`
	if got != want {
		t.Fatalf("void element rendered with a body: %q want %q", got, want)
	}
}

func TestNonVoidElementsAlwaysEmitClosingTag(t *testing.T) {
	for _, tc := range []struct {
		node h.H
		want string
	}{
		{h.Div(), "<div></div>"},
		{h.Span(), "<span></span>"},
		{h.H1(), "<h1></h1>"},
		{h.Button(), "<button></button>"},
		{h.Body(), "<body></body>"},
		{h.El("section"), "<section></section>"},
	} {
		if got := render(t, tc.node); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
}

func TestNestedElementsRenderRecursively(t *testing.T) {
	got := render(t, h.Body(h.Div(h.H1(h.Str("hi")))))
	want := "<body><div><h1>hi</h1></div></body>"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMultipleAttributesKeepSourceOrder(t *testing.T) {
	got := render(t, h.El("a",
		h.RawAttr("href", "/x"),
		h.RawAttr("rel", "next"),
		h.Str("go"),
	))
	want := `<a href="/x" rel="next">go</a>`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBinderIsExposedSoDynamicNodesCanClaimSlots(t *testing.T) {
	// via's signal/action nodes reach the Binder through r.Binder(); the
	// renderer must hand back the exact binder it was built with.
	b := &stubBinder{}
	r := h.NewRenderer(b)
	if r.Binder() != b {
		t.Fatalf("Binder() did not return the injected binder")
	}
}

func TestBytesMatchesStringForZeroCopyWriting(t *testing.T) {
	// via writes the rendered tree straight to the ResponseWriter via Bytes()
	// to avoid a string copy; it must equal String().
	r := h.NewRenderer(&stubBinder{})
	r.Render(h.Span(h.Str("x")))
	if string(r.Bytes()) != r.String() {
		t.Fatalf("Bytes() %q != String() %q", r.Bytes(), r.String())
	}
}

func TestWriteEscapedAndWriteStringDistinguishRawFromEscaped(t *testing.T) {
	r := h.NewRenderer(&stubBinder{})
	r.WriteString("<b>")  // raw, caller pre-escaped
	r.WriteEscaped("<b>") // must be escaped
	if got := r.String(); got != "<b>&lt;b&gt;" {
		t.Fatalf("got %q", got)
	}
}
