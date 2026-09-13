package h_test

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
	"github.com/stretchr/testify/assert"
)

// urlCorpus is the hostile set both gates have to agree on. Each entry pairs a
// URL with whether the policy admits it verbatim.
var urlCorpus = []struct {
	url  string
	safe bool
}{
	{"/threads/7", true},
	{"threads/7", true},
	{"", false},
	{"https://example.com/x", true},
	{"HTTP://example.com/x", true},
	{"?q=1", true},
	{"#frag", true},
	{"javascript:alert(1)", false},
	{"JavaScript:alert(1)", false},
	{"\njavascript:alert(1)", false},
	{" \t javascript:alert(1)", false},
	{"data:text/html,x", false},
	{"vbscript:msgbox", false},
	{"//evil.example/x", false},
	{`\\evil.example\x`, false},
	{`/\evil.example`, false},
	{`/\/evil.example`, false},
	{`\/evil.example`, false},
	{"\n//evil.example/x", false},
	{" \t//evil.example/x", false},
	{"mailto:a@b.c", false},
}

// The neutralizer behind the typed attributes and the predicate behind via's
// Redirect gate are the same policy, and the point of moving the predicate into
// internal/hcore is that they can no longer be two policies that merely agree.
// This pins the agreement: an attribute is neutralized to "#" exactly when the
// predicate refuses the URL. Change either side alone and this fails.
func TestURLPolicy_attributeGateAgreesWithThePredicate(t *testing.T) {
	t.Parallel()
	for _, c := range urlCorpus {
		assert.Equal(t, c.safe, hcore.SafeURL(c.url), "predicate verdict for %q", c.url)

		rendered := render(t, h.A(h.Href(c.url)))
		neutralized := strings.Contains(rendered, `href="#"`)
		assert.Equal(t, !c.safe, neutralized,
			"the attribute gate and the predicate disagree about %q: rendered %s", c.url, rendered)
	}
}

// Every typed URL attribute goes through the one gate, not just href — a
// policy that covers links but not form actions is not a policy.
func TestURLPolicy_coversEveryTypedAttribute(t *testing.T) {
	t.Parallel()
	assert.Contains(t, render(t, h.A(h.Href("javascript:alert(1)"))), `href="#"`)
	assert.Contains(t, render(t, h.Img(h.Src("javascript:alert(1)"))), `src="#"`)
	assert.Contains(t, render(t, h.Form(h.Action("javascript:alert(1)"))), `action="#"`)
	assert.Contains(t, render(t, h.A(h.Href("/ok"))), `href="/ok"`)
	assert.Contains(t, render(t, h.Img(h.Src("/ok.png"))), `src="/ok.png"`)
	assert.Contains(t, render(t, h.Form(h.Action("/ok"))), `action="/ok"`)
}

// RawAttr is the escape hatch for any attribute name, and a caller can spell
// the same URL-bearing attributes the typed constructors cover — formaction
// being the sharpest, since it hijacks a submit button inside an otherwise
// safe form. Every one of them must go through the same SafeURL gate as
// Href/Src/Action, or RawAttr is a bypass of the typed policy rather than an
// equally-policed alternative to it. Case is attacked too, since HTML
// attribute names are case-insensitive.
func TestRawAttr_gatesEveryURLBearingAttributeName(t *testing.T) {
	t.Parallel()
	// xlink:href is deliberately excluded: RawAttr's name allowlist already
	// rejects any colon outside a data-* prefix (see
	// TestNonDataNamesStillRejectPluginPunctuation), so it is unreachable via
	// RawAttr regardless of the URL gate — nothing to wire here.
	for _, name := range []string{
		"formaction", "FormAction", "action", "href", "src",
		"poster", "data", "cite", "background", "ping", "manifest",
	} {
		got := render(t, h.El("a", h.RawAttr(name, "javascript:alert(1)")))
		assert.Containsf(t, got, `="#"`, "h.RawAttr(%q, javascript:...) must neutralize to \"#\", got %s", name, got)
	}
	// srcset carries a list, not a bare scheme, but a scheme-only payload must
	// still be caught.
	assert.Contains(t, render(t, h.El("img", h.RawAttr("srcset", "javascript:alert(1)"))), `="#"`)

	// A legitimate relative URL on every gated name must render untouched.
	assert.Contains(t, render(t, h.El("a", h.RawAttr("formaction", "/ok"))), `formaction="/ok"`)
	assert.Contains(t, render(t, h.El("a", h.RawAttr("href", "/ok"))), `href="/ok"`)
}

// srcdoc is single-escaped like any other attribute value, but a browser
// entity-decodes the attribute and then parses the decoded string as a
// document: an escaped "<script>" round-trips back to a live <script> tag,
// same-origin. There is no safe escaping strategy for it through RawAttr, so
// it must be refused outright rather than rendered.
func TestRawAttr_rejectsSrcdocOutright(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"srcdoc", "SRCDOC", "SrcDoc"} {
		assert.Panicsf(t, func() { h.RawAttr(name, "<script>alert(1)</script>") },
			"RawAttr(%q) must panic — srcdoc always allows same-origin script", name)
	}
}

// Href/Src/Action neutralize through ONE gate. They used to call h.RawAttr,
// which re-ran the same URL-bearing check on the value they had already
// neutralized; they now go straight to the core attribute writer. This pins
// what the second pass was silently propping up: the surviving gate alone
// still refuses every hostile family, once, with nothing of the URL left in
// the markup.
func TestURLPolicy_typedAttributesNeutralizeThroughASingleGate(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	// The gate runs when the Attr is CONSTRUCTED, not at render, so each node
	// has to be built after the log buffer is reset.
	gates := []struct {
		attr string
		node func(string) h.H
	}{
		{"href", func(u string) h.H { return h.A(h.Href(u)) }},
		{"src", func(u string) h.H { return h.Img(h.Src(u)) }},
		{"action", func(u string) h.H { return h.Form(h.Action(u)) }},
	}
	for _, u := range []string{"javascript:alert(1)", "data:text/html,x", "//evil.example/x"} {
		for _, g := range gates {
			logs.Reset()
			rendered := render(t, g.node(u))
			assert.Contains(t, rendered, g.attr+`="#"`, "%s=%q must neutralize", g.attr, u)
			assert.NotContains(t, rendered, u, "the hostile URL must not survive anywhere in the markup")
			assert.Equal(t, 1, strings.Count(logs.String(), "neutralized"),
				"%s=%q must be neutralized once, not once per gate: %s", g.attr, u, logs.String())
		}
	}
}
