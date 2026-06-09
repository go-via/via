package vianats

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Wire keys are arbitrary user strings, but a NATS subject token / KV key cannot
// contain '.', '*', '>' (subject structure) or be empty. sanitize must map any
// such key to a single safe, collision-free token, or a dotted key like
// "chart.x" would silently fan out across subject levels and corrupt routing.
func TestSanitize_makesArbitraryKeysSafeAndDistinct(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"alpha":   "alpha",      // alnum passes through untouched
		"a-b":     "a-b",        // '-' is allowed
		"a_b":     "a_5f_b",     // '_' is the escape delimiter, so it is itself escaped
		"chart.x": "chart_2e_x", // '.' (subject separator) is encoded
		"a*b":     "a_2a_b",     // '*' wildcard encoded
		"a>b":     "a_3e_b",     // '>' wildcard encoded
		"":        "_empty_",    // empty key gets a stable token
	}
	for in, want := range cases {
		assert.Equalf(t, want, sanitize(in), "sanitize(%q)", in)
	}

	// Distinct inputs must not collide (no two keys share a subject/KV name).
	seen := map[string]string{}
	for _, in := range []string{"a.b", "a-b", "a_b", "ab", "a*b", ""} {
		s := sanitize(in)
		prev, ok := seen[s]
		require.Falsef(t, ok, "sanitize collision: %q and %q both → %q", prev, in, s)
		seen[s] = in
	}
}
