package hcore

import "strings"

// SafeURL admits http, https and relative URLs and rejects everything else —
// javascript:, data:, vbscript:, and the protocol-relative // and \\ forms.
//
// This is THE URL policy: it lives here, below both packages, so h's typed
// attributes and via's Redirect share one implementation rather than two that
// agree today. A second copy of a URL gate is how a javascript: bypass ends up
// admitted by one caller and refused by the other.
//
// Leading control characters and spaces are trimmed before the scheme is read,
// because a browser ignores them ("\njavascript:alert(1)" navigates), and the
// scheme is compared case-folded for the same reason.
func SafeURL(u string) bool {
	trimmed := strings.TrimLeftFunc(u, func(r rune) bool { return r <= ' ' })
	if trimmed == "" || isSlashLike(trimmed, 0) && isSlashLike(trimmed, 1) {
		return false
	}
	if i := strings.IndexAny(trimmed, ":/?#"); i >= 0 && trimmed[i] == ':' {
		scheme := strings.ToLower(trimmed[:i])
		return scheme == "http" || scheme == "https"
	}
	return true
}

// isSlashLike reports whether s[i] is '/' or '\\' — a browser's URL parser
// treats a backslash exactly like a forward slash, so "/\evil.com" and
// "\/evil.com" are protocol-relative just as much as "//evil.com" is; only
// checking for a literal "//" or "\\\\" prefix misses both mixed forms.
func isSlashLike(s string, i int) bool {
	return i < len(s) && (s[i] == '/' || s[i] == '\\')
}
