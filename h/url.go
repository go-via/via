package h

import (
	"log/slog"
	"strings"

	"github.com/go-via/via/internal/render"
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
	"poster":     true,
	"data":       true, // the <object> "data" attribute, not h.Data
	"cite":       true,
	"background": true,
	"ping":       true,
	"manifest":   true,
	"srcset":     true,
}

func isURLBearingAttr(name string) bool { return urlBearingAttrs[strings.ToLower(name)] }

// gateURLAttr is safeURL for a RawAttr value. srcset and ping hold a list, and
// the scheme of a list's first URL says nothing about the rest, so each URL is
// gated and one refusal neutralizes the whole value.
func gateURLAttr(name, val string) string {
	var urls []string
	switch strings.ToLower(name) {
	case "srcset":
		urls = srcsetURLs(val)
	case "ping":
		urls = strings.Fields(val)
	}
	if len(urls) == 0 {
		return safeURL(val, name)
	}
	for _, u := range urls {
		if safeURL(u, name) == "#" {
			return "#"
		}
	}
	return val
}

// srcsetURLs splits on every comma, where the browser splits only on one that
// ends a candidate. Each piece's first field is then a URL the browser reads,
// a comma-cut prefix of one (same scheme), or descriptor text, so a URL with a
// comma in it can be refused but a hostile one is never admitted.
func srcsetURLs(v string) []string {
	var urls []string
	for piece := range strings.SplitSeq(v, ",") {
		if f := strings.Fields(piece); len(f) > 0 {
			urls = append(urls, f[0])
		}
	}
	return urls
}

// safeURL admits what render.SafeURL admits (render.SafeHref for an href);
// everything else — javascript:, data:, vbscript:, protocol-relative // and \\
// — neutralizes to "#" with a loud log. It owns the neutralization, not the
// policy: the predicates live in internal/render so via's Redirect gate cannot
// drift from this one.
func safeURL(u, where string) string {
	if strings.EqualFold(where, "href") && render.SafeHref(u) || render.SafeURL(u) {
		return u
	}
	// slog.Default(): no Router in scope.
	slog.Default().Warn("h: unsafe URL neutralized to \"#\"", "attr", where, "url", u)
	return "#"
}

// Href is the typed href attribute: http, https, mailto, tel and relative URLs
// pass through, any other scheme is neutralized to "#" and logged — a link must
// never become a script gadget.
//
// Href, [Src] and [Action] each run the URL gate exactly once, so an unsafe
// value is logged once rather than per layer.
func Href(u string) Attr { return render.RawAttr("href", safeURL(u, "href")) }

// Src is the typed src attribute, gated like Href but without mailto: and tel:.
func Src(u string) Attr { return render.RawAttr("src", safeURL(u, "src")) }

// Action is the typed form-action attribute, gated like [Src].
func Action(u string) Attr { return render.RawAttr("action", safeURL(u, "action")) }
