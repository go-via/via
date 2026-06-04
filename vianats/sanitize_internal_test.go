package vianats

import "testing"

// Wire keys are arbitrary user strings, but a NATS subject token / KV key cannot
// contain '.', '*', '>' (subject structure) or be empty. sanitize must map any
// such key to a single safe, collision-free token, or a dotted key like
// "chart.x" would silently fan out across subject levels and corrupt routing.
func TestSanitizeMakesArbitraryKeysSafeAndDistinct(t *testing.T) {
	cases := map[string]string{
		"alpha":     "alpha",       // alnum passes through untouched
		"a-b":       "a-b",         // '-' is allowed
		"chart.x":   "chart_2e_x",  // '.' (subject separator) is encoded
		"a*b":       "a_2a_b",      // '*' wildcard encoded
		"a>b":       "a_3e_b",      // '>' wildcard encoded
		"":          "_empty_",     // empty key gets a stable token
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}

	// Distinct inputs must not collide (no two keys share a subject/KV name).
	seen := map[string]string{}
	for _, in := range []string{"a.b", "a-b", "a_b", "ab", "a*b", ""} {
		s := sanitize(in)
		if prev, ok := seen[s]; ok {
			t.Fatalf("sanitize collision: %q and %q both → %q", prev, in, s)
		}
		seen[s] = in
	}
}
