package h_test

import (
	"testing"

	"github.com/go-via/via/v2/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An attribute renderer that escapes only the value leaves the name as a raw
// breakout vector: a name like `x" onmouseover="alert(1)` grafts a live event
// handler onto the tag. The DSL's whole promise is safe HTML, so a name that
// could break out of the opening tag must be rejected at construction, loudly,
// rather than silently emitted.
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

// A bare carriage return in rendered text would terminate an SSE data line and
// split a datastar-patch-elements frame mid-payload (Datastar's stream tokenizer
// treats CR as a line end), corrupting the morph — a bug no httptest sees. It
// must be escaped like the other dangerous characters.
func TestText_escapesCarriageReturnThatWouldSplitAnSSEFrame(t *testing.T) {
	t.Parallel()
	got := render(t, h.Span(h.Str("before\rafter")))
	assert.NotContains(t, got, "\r", "a bare CR splits an SSE data line")
	assert.Contains(t, got, "&#13;")
}

// h.Data prepends "data-" but the caller-supplied suffix is the injection
// surface; it is validated against the same allowlist as a raw attribute name
// (the suffix must itself start with a letter — "data-" is a fixed safe prefix).
func TestData_rejectsInjectableSuffix(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{`x" onclick="evil`, "a b", ">", "", "-x"} {
		assert.Panicsf(t, func() { h.Data(suffix, "v") },
			"Data(%q) must panic — suffix is an injection vector", suffix)
	}
}

// The allowlist must still admit every legitimate HTML/data/ARIA attribute
// name, or it breaks real markup. The cases pin the boundary precisely: a
// single letter (leading-letter rule, not merely "non-digit"), uppercase
// (the class is [A-Za-z] not [a-z]), and hyphens/digits after the first letter
// — including a trailing hyphen — must all render unchanged.
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
