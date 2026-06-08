package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Internal test: buildNoticeScript is unexported, but its safety property is
// the test target — a malicious room title must not produce a script that
// closes the surrounding <script> tag, escapes the IIFE parameter binding,
// or interpolates the title into a raw JS string. The pure function is the
// smallest surface for that claim and is exercised directly here. The
// convention prefers package ui_test, but standing up DB + auth + echarts +
// maplibre to observe the same property through the SSE wire is
// disproportionate.

func TestBuildNoticeScript_safeAcrossMaliciousTitles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		title string
	}{
		{"benign", "Friday Standup"},
		{"scriptCloseAttempt", `</script><img src=x onerror=alert(1)>`},
		{"singleQuote", `O'Brien`},
		{"jsLineSeparator", "before\u2028after"},
		{"jsParagraphSeparator", "before\u2029after"},
		{"backticks", "`${alert(1)}`"},
		{"templateBreaker", `</script>';alert(1)//`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := buildNoticeScript(tt.title)
			// The dynamic value travels as the sole argument of a function-call
			// IIFE, JSON-parsed at the call site — one isolated data segment,
			// never dropped between two raw JS string fragments. This is the
			// shape CodeQL's "potentially unsafe quoting" rule accepts.
			require.True(t, strings.HasPrefix(script, "(function(msg){"),
				"script must be a function-call IIFE; got %q", script)
			require.True(t, strings.HasSuffix(script, ")"),
				"script must close the IIFE call; got %q", script)
			assert.Contains(t, script, "JSON.parse(",
				"the dynamic value must be parsed from a JSON literal, not concatenated into JS")
			assert.Contains(t, script, ".textContent=msg",
				"the banner text must be assigned via the XSS-safe textContent sink, not innerHTML")
			// A literal </script> in the title would close the surrounding
			// <script> element when the snippet is inlined by ExecuteScript;
			// json.Marshal HTML-escapes the angle brackets, so the literal
			// sequence must not survive in the script.
			assert.NotContains(t, script, "</script>",
				"a </script> in the title must not survive literal in the script")
		})
	}
}
