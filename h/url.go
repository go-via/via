package h

import (
	"log"
	"strings"
)

// safeURL admits http, https and relative URLs; everything else — javascript:,
// data:, vbscript:, protocol-relative // and \\ — neutralizes to "#" with a
// loud log. Shared by the typed URL attributes here and via's Redirect: one
// gate, one policy.
func safeURL(u, where string) string {
	trimmed := strings.TrimLeftFunc(u, func(r rune) bool { return r <= ' ' })
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, `\\`) {
		log.Printf("h: unsafe %s URL %q neutralized to \"#\"", where, u)
		return "#"
	}
	if i := strings.IndexAny(trimmed, ":/?#"); i >= 0 && trimmed[i] == ':' {
		scheme := strings.ToLower(trimmed[:i])
		if scheme != "http" && scheme != "https" {
			log.Printf("h: unsafe %s URL %q neutralized to \"#\"", where, u)
			return "#"
		}
	}
	return u
}

// SafeURL reports whether safeURL would pass u through verbatim — the same
// http/https/relative policy the typed attributes enforce, exported for via's
// Redirect so the two can never drift.
func SafeURL(u string) bool {
	trimmed := strings.TrimLeftFunc(u, func(r rune) bool { return r <= ' ' })
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, `\\`) {
		return false
	}
	if i := strings.IndexAny(trimmed, ":/?#"); i >= 0 && trimmed[i] == ':' {
		scheme := strings.ToLower(trimmed[:i])
		return scheme == "http" || scheme == "https"
	}
	return true
}

// Href is the typed href attribute: http/https/relative URLs pass through, an
// unsafe scheme is neutralized to "#" and logged — a link must never become a
// script gadget.
func Href(u string) Attr { return RawAttr("href", safeURL(u, "href")) }

// Src is the typed src attribute, gated like Href.
func Src(u string) Attr { return RawAttr("src", safeURL(u, "src")) }

// Action is the typed form-action attribute, gated like Href.
func Action(u string) Attr { return RawAttr("action", safeURL(u, "action")) }
