package hcore

import "strings"

// SafeURL admits http, https and relative URLs and rejects everything else —
// javascript:, data:, vbscript:, mailto:, and the protocol-relative // and \\
// forms. It is the policy for src, action and Redirect; href uses [SafeHref].
//
// This is the URL policy: it lives here, below both packages, so h's typed
// attributes and via's Redirect share one implementation rather than two that
// agree today. A second copy of a URL gate is how a javascript: bypass ends up
// admitted by one caller and refused by the other.
//
// Leading control characters and spaces are trimmed before the scheme is read,
// because a browser ignores them ("\njavascript:alert(1)" navigates), and the
// scheme is compared case-folded for the same reason. Tab, CR and LF are
// removed from the whole string first: the URL parser strips them anywhere, so
// "/\t/evil.com" is protocol-relative by the time it navigates.
func SafeURL(u string) bool {
	scheme, ok := urlScheme(u)
	return ok && (scheme == "" || scheme == "http" || scheme == "https")
}

// SafeHref is [SafeURL] plus mailto: and tel:, for href alone. Both schemes
// hand the value to a mail or phone app and carry no script, but they are
// meaningless as a redirect, an image or a form target, so every other caller
// stays on SafeURL.
func SafeHref(u string) bool {
	scheme, ok := urlScheme(u)
	return ok && (scheme == "" || scheme == "http" || scheme == "https" ||
		scheme == "mailto" || scheme == "tel")
}

// urlScheme returns u's lower-cased scheme as the browser would read it, ""
// for a relative URL. ok is false for an empty or protocol-relative URL, which
// no policy admits.
func urlScheme(u string) (scheme string, ok bool) {
	u = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, u)
	trimmed := strings.TrimLeftFunc(u, func(r rune) bool { return r <= ' ' })
	if trimmed == "" || isSlashLike(trimmed, 0) && isSlashLike(trimmed, 1) {
		return "", false
	}
	if i := strings.IndexAny(trimmed, ":/?#"); i >= 0 && trimmed[i] == ':' {
		return strings.ToLower(trimmed[:i]), true
	}
	return "", true
}

// isSlashLike reports whether s[i] is '/' or '\\' — a browser's URL parser
// treats a backslash exactly like a forward slash, so "/\evil.com" and
// "\/evil.com" are protocol-relative just as much as "//evil.com" is; only
// checking for a literal "//" or "\\\\" prefix misses both mixed forms.
func isSlashLike(s string, i int) bool {
	return i < len(s) && (s[i] == '/' || s[i] == '\\')
}
