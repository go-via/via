package h

import (
	"log"
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

// isURLBearingAttr reports whether name (case-insensitively) is one of
// urlBearingAttrs.
func isURLBearingAttr(name string) bool { return urlBearingAttrs[strings.ToLower(name)] }

// safeURL admits what hcore.SafeURL admits; everything else — javascript:,
// data:, vbscript:, protocol-relative // and \\ — neutralizes to "#" with a
// loud log. It owns the neutralization, not the policy: the predicate lives in
// internal/hcore so via's Redirect gate cannot drift from this one.
func safeURL(u, where string) string {
	if hcore.SafeURL(u) {
		return u
	}
	log.Printf("h: unsafe %s URL %q neutralized to \"#\"", where, u)
	return "#"
}

// Href is the typed href attribute: http/https/relative URLs pass through, an
// unsafe scheme is neutralized to "#" and logged — a link must never become a
// script gadget.
func Href(u string) Attr { return RawAttr("href", safeURL(u, "href")) }

// Src is the typed src attribute, gated like Href.
func Src(u string) Attr { return RawAttr("src", safeURL(u, "src")) }

// Action is the typed form-action attribute, gated like Href.
func Action(u string) Attr { return RawAttr("action", safeURL(u, "action")) }
