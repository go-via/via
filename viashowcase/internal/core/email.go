package core

import "strings"

// NormalizeEmail canonicalizes an email for storage and lookup: trimmed and
// lowercased. Applying it on both the signup-stored value and the login lookup
// makes accounts case-insensitive, so "Bob@Test.dev" and "bob@test.dev" are the
// same account rather than two.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
