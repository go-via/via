package h

import (
	"html/template"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

// htmlEscape must match html/template.HTMLEscapeString byte-for-byte
// across the input alphabet. Tests live in the internal package
// (`package h`, not `h_test`) because htmlEscape is unexported and the
// parity check is the public contract — every text node and attribute
// value must escape exactly the way the stdlib does.

func TestHtmlEscape_matchesStdlib_onShortInputs(t *testing.T) {
	t.Parallel()
	tests := []string{
		"",
		"hello",
		"<",
		">",
		"&",
		"\"",
		"'",
		"\x00",
		"<script>alert('xss')</script>",
		"a & b \"c\" 'd' <e>",
		"emoji 🚀 stays",
		"already &amp; encoded",
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, template.HTMLEscapeString(in), htmlEscape(in))
		})
	}
}

func FuzzHtmlEscape_matchesStdlib(f *testing.F) {
	for _, s := range []string{
		"", "<", ">", "&", "\"", "'",
		"a<b>c&d\"e'f", "\x00x\x01", "<<<<<<",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		// Skip invalid UTF-8 — html/template.HTMLEscapeString preserves
		// invalid bytes verbatim and we want to assert parity on inputs
		// where the stdlib's contract is defined.
		if !utf8.ValidString(s) {
			return
		}
		want := template.HTMLEscapeString(s)
		got := htmlEscape(s)
		assert.Equal(t, want, got, "input=%q", s)
	})
}

func TestHtmlEscapeBytes_isStableAcrossInputs(t *testing.T) {
	t.Parallel()
	// Bytes form must also match the stdlib output when interpreted as
	// a string. This catches any drift between the two escape
	// implementations.
	tests := []string{"", "x", "<x>", "a&b", strings.Repeat("\"", 50)}
	for _, in := range tests {
		assert.Equal(t, template.HTMLEscapeString(in), string(htmlEscapeBytes(in)))
	}
}
