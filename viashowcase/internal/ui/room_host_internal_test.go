package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Internal test: buildNoticeScript is unexported, but its safety property is
// the test target — a malicious room title (or code) must not produce a script
// that closes the surrounding <script> tag, escapes the IIFE parameter binding,
// or interpolates the value into a raw JS string. The pure function is the
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
		{"jsLineSeparator", "before after"},
		{"jsParagraphSeparator", "before after"},
		{"backticks", "`${alert(1)}`"},
		{"templateBreaker", `</script>';alert(1)//`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := buildNoticeScript("ROOMCODE", tt.title)
			// The dynamic values travel as the arguments of a function-call
			// IIFE, JSON-parsed at the call site — isolated data segments, never
			// dropped between two raw JS string fragments. This is the shape
			// CodeQL's "potentially unsafe quoting" rule accepts.
			require.True(t, strings.HasPrefix(script, "(function(code,msg){"),
				"script must be a function-call IIFE taking (code,msg); got %q", script)
			require.True(t, strings.HasSuffix(script, ")"),
				"script must close the IIFE call; got %q", script)
			assert.Contains(t, script, "JSON.parse(",
				"the dynamic value must be parsed from a JSON literal, not concatenated into JS")
			assert.Contains(t, script, ".textContent=msg",
				"the banner text must be assigned via the XSS-safe textContent sink, not innerHTML")
			// A literal </script> in the title would close the surrounding
			// <script> element when the snippet is inlined; json.Marshal
			// HTML-escapes the angle brackets, so it must not survive.
			assert.NotContains(t, script, "</script>",
				"a </script> in the title must not survive literal in the script")
		})
	}
}

// The banner must only appear on tabs viewing this room — App.Broadcast queues
// the script on every live tab app-wide, so an ungated script would spam every
// other room's audience and the home/login pages.
func TestBuildNoticeScript_isScopedToTheRoom(t *testing.T) {
	t.Parallel()
	script := buildNoticeScript("ABC123", "Standup")
	assert.Contains(t, script, `"ABC123"`,
		"the room code must be embedded as a JSON literal, not concatenated")
	// The path check must GATE the banner: an early return before the DOM
	// append, so a tab not viewing this room shows nothing. Merely mentioning
	// location.pathname somewhere is not enough.
	assert.Regexp(t, `location\.pathname[^;]*\)\s*\{?\s*return`, script,
		"the script must early-return when the path doesn't match the room")
	// endsWith (boundary match), not indexOf (substring) — codes are
	// variable-length, so a short code must not match a longer code's path.
	assert.Contains(t, script, "location.pathname.endsWith('/'+code)",
		"the gate must match the code at the path-segment boundary")
	ret := strings.Index(script, "return")
	app := strings.Index(script, "appendChild")
	require.NotEqual(t, -1, ret)
	require.NotEqual(t, -1, app)
	assert.Less(t, ret, app, "the path-guard return must precede the banner append")
}

// A malicious room code must also be JSON-encoded, never concatenated raw, so
// it can't break out of the script the way a crafted title can't.
func TestBuildNoticeScript_safeAcrossMaliciousCodes(t *testing.T) {
	t.Parallel()
	script := buildNoticeScript(`"});alert(1)//`, "Standup")
	require.True(t, strings.HasPrefix(script, "(function(code,msg){"),
		"script must remain a well-formed IIFE; got %q", script)
	require.True(t, strings.HasSuffix(script, ")"), "script must close the IIFE call")
	// The crafted code's leading quote must be backslash-escaped inside a JSON
	// literal (a raw concatenation would emit a bare quote that breaks out of
	// JSON.parse and into executable JS).
	assert.Contains(t, script, `JSON.parse("\"});alert(1)//")`,
		"a crafted code must be JSON-escaped, not concatenated raw")
}
