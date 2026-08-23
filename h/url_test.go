package h_test

import (
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
