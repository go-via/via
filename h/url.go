package h

import (
	"log"

	"github.com/go-via/via/internal/hcore"
)

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
