package h

import (
	"log/slog"
	"strings"

	"github.com/go-via/via/internal/hcore"
)

// urlBearingAttrs are the RawAttr names that carry a URL and so must go
// through safeURL just like Href/Src/Action do — otherwise
// h.RawAttr("formaction", "javascript:...") bypasses the typed gate entirely.
// Keys are lower-cased; RawAttr matches case-insensitively.
var urlBearingAttrs = map[string]bool{
	"formaction": true,
	"action":     true,
	"href":       true,
	"src":        true,
	"xlink:href": true,
	"poster":     true,
	"data":       true, // the <object> "data" attribute, not h.Data
	"cite":       true,
	"background": true,
	"ping":       true,
	"manifest":   true,
	"srcset":     true,
}

func isURLBearingAttr(name string) bool { return urlBearingAttrs[strings.ToLower(name)] }

// safeURL admits what hcore.SafeURL admits; everything else — javascript:,
// data:, vbscript:, protocol-relative // and \\ — neutralizes to "#" with a
// loud log. It owns the neutralization, not the policy: the predicate lives in
// internal/hcore so via's Redirect gate cannot drift from this one.
func safeURL(u, where string) string {
	if hcore.SafeURL(u) {
		return u
	}
	// slog.Default(): no Router in scope.
	slog.Default().Warn("h: unsafe URL neutralized to \"#\"", "attr", where, "url", u)
	return "#"
}

// Href is the typed href attribute: http/https/relative URLs pass through, an
// unsafe scheme is neutralized to "#" and logged — a link must never become a
// script gadget.
//
// Href, [Src] and [Action] each run the URL gate exactly once, so an unsafe
// value is logged once rather than per layer.
func Href(u string) Attr { return hcore.RawAttr("href", safeURL(u, "href")) }

// Src is the typed src attribute, gated like Href.
func Src(u string) Attr { return hcore.RawAttr("src", safeURL(u, "src")) }

// Action is the typed form-action attribute, gated like Href.
func Action(u string) Attr { return hcore.RawAttr("action", safeURL(u, "action")) }
