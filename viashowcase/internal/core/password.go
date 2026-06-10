package core

import "unicode/utf8"

// MinPasswordRunes is the signup password floor, in characters (runes) — it
// matches the "At least 8 characters" copy shown on the form.
const MinPasswordRunes = 8

// PasswordLongEnough reports whether pw meets the minimum length, counted in
// runes so the policy matches the characters a user typed rather than the byte
// length of multibyte input.
func PasswordLongEnough(pw string) bool {
	return utf8.RuneCountInString(pw) >= MinPasswordRunes
}
