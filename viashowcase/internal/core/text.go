package core

import "strings"

// NormalizeText trims surrounding whitespace from s and, when maxRunes > 0,
// caps it to at most maxRunes runes. Truncation is on rune boundaries (it
// counts and slices runes, never bytes), so the result is always valid UTF-8 —
// fixing the byte-slice truncation that could corrupt a multibyte rune. The cap
// is by rune, not grapheme, so a multi-rune emoji at the boundary may be split;
// that is acceptable for length-bounding untrusted input. maxRunes <= 0 applies
// no cap.
func NormalizeText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}
