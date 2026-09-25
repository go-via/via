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
// URL with whether the policy admits it verbatim; href widens it by mailto:
// and tel: only.
var urlCorpus = []struct {
	url  string
	safe bool
	href bool
}{
	{"/threads/7", true, true},
	{"threads/7", true, true},
	{"", false, false},
	{"https://example.com/x", true, true},
	{"HTTP://example.com/x", true, true},
	{"?q=1", true, true},
	{"#frag", true, true},
	{"javascript:alert(1)", false, false},
	{"JavaScript:alert(1)", false, false},
	{"\njavascript:alert(1)", false, false},
	{" \t javascript:alert(1)", false, false},
	{"data:text/html,x", false, false},
	{"vbscript:msgbox", false, false},
	{"//evil.example/x", false, false},
	{`\\evil.example\x`, false, false},
	{`/\evil.example`, false, false},
	{`/\/evil.example`, false, false},
	{`\/evil.example`, false, false},
	{"\n//evil.example/x", false, false},
	{" \t//evil.example/x", false, false},
	{"mailto:a@b.c", false, true},
	{"MailTo:a@b.c?subject=hi", false, true},
	{"tel:+15550100", false, true},
	{" \ttel:+15550100", false, true},
	{"mail\tto:a@b.c", false, true},
	{"sms:+15550100", false, false},
	{"mailto//a@b.c", true, true},
	{"/\t/evil.example", false, false},
	{"/\n/evil.example", false, false},
	{"/\r\n/evil.example", false, false},
	{"\\\t\\evil.example", false, false},
	{"java\tscript:alert(1)", false, false},
	{"java\r\nscript:alert(1)", false, false},
	{"/threads/\t7", true, true},
}

func TestURLPolicy_attributeGateAgreesWithThePredicate(t *testing.T) {
	t.Parallel()
	for _, c := range urlCorpus {
		assert.Equal(t, c.safe, hcore.SafeURL(c.url), "predicate verdict for %q", c.url)
		assert.Equal(t, c.href, hcore.SafeHref(c.url), "href predicate verdict for %q", c.url)

		rendered := render(t, h.A(h.Href(c.url)))
		neutralized := strings.Contains(rendered, `href="#"`)
		assert.Equal(t, !c.href, neutralized,
			"the href gate and the predicate disagree about %q: rendered %s", c.url, rendered)

		rendered = render(t, h.Img(h.Src(c.url)))
		neutralized = strings.Contains(rendered, `src="#"`)
		assert.Equal(t, !c.safe, neutralized,
			"the src gate and the predicate disagree about %q: rendered %s", c.url, rendered)
	}
}

func TestURLPolicy_coversEveryTypedAttribute(t *testing.T) {
	t.Parallel()
	assert.Contains(t, render(t, h.A(h.Href("javascript:alert(1)"))), `href="#"`)
	assert.Contains(t, render(t, h.Img(h.Src("javascript:alert(1)"))), `src="#"`)
	assert.Contains(t, render(t, h.Form(h.Action("javascript:alert(1)"))), `action="#"`)
	assert.Contains(t, render(t, h.A(h.Href("/ok"))), `href="/ok"`)
	assert.Contains(t, render(t, h.Img(h.Src("/ok.png"))), `src="/ok.png"`)
	assert.Contains(t, render(t, h.Form(h.Action("/ok"))), `action="/ok"`)
}

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

	assert.Contains(t, render(t, h.El("a", h.RawAttr("formaction", "/ok"))), `formaction="/ok"`,
		"a legitimate relative URL on a gated name must render untouched")
	assert.Contains(t, render(t, h.El("a", h.RawAttr("href", "/ok"))), `href="/ok"`,
		"a legitimate relative URL on a gated name must render untouched")
}

func TestURLPolicy_mailtoAndTelPassInHrefOnly(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"mailto:a@b.c", "tel:+15550100"} {
		assert.Contains(t, render(t, h.A(h.Href(u))), `href="`+u+`"`)
		assert.Contains(t, render(t, h.El("a", h.RawAttr("href", u))), `href="`+u+`"`)
		assert.Contains(t, render(t, h.El("a", h.RawAttr("HREF", u))), `="`+u+`"`)
		assert.Contains(t, render(t, h.Img(h.Src(u))), `src="#"`)
		assert.Contains(t, render(t, h.Form(h.Action(u))), `action="#"`)
		for _, name := range []string{"src", "action", "formaction", "ping", "poster"} {
			assert.Containsf(t, render(t, h.El("a", h.RawAttr(name, u))), `="#"`,
				"h.RawAttr(%q, %q) must neutralize: only href admits mailto:/tel:", name, u)
		}
	}
}

func TestRawAttr_rejectsSrcdocOutright(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"srcdoc", "SRCDOC", "SrcDoc"} {
		assert.Panicsf(t, func() { h.RawAttr(name, "<script>alert(1)</script>") },
			"RawAttr(%q) must panic — srcdoc always allows same-origin script", name)
	}
}

func TestURLPolicy_typedAttributesNeutralizeThroughASingleGate(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	// The gate runs when the Attr is constructed, not at render, so each node
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
