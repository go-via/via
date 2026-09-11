package via

import (
	"html"
	"net/url"
	"strings"
)

// Head describes the document shell — everything between <head> and the body
// via itself owns. Title/Lang/InlineStyle are the fixed, security-relevant
// slots; Raw is the escape hatch for everything else (meta, links, external
// scripts) — the app writes that HTML itself, emitted verbatim right after
// via's own <meta charset>. The origin lists are the app's declaration of
// which off-origin hosts the CSP must admit for what Raw references: via
// cannot derive them by parsing Raw (that would mean sniffing arbitrary HTML
// for security-relevant meaning), so the app states them directly, one list
// per directive so a stylesheet CDN is not also trusted for script.
//
// The zero Head means "inherit": an empty field is not rendered and does not
// widen the policy, so a partially-filled Head is always valid.
type Head struct {
	// Title is the document title. Empty means no <title> element.
	Title string

	// Lang is the <html lang> value (e.g. "en", "pt-PT"). Empty omits it.
	Lang string

	// Raw is head markup emitted verbatim after <meta charset> — stylesheets,
	// icons, viewport meta, external scripts, whatever the app needs. via does
	// not parse or validate it; the app owns its own shape.
	Raw string

	// InlineStyle is a single inline stylesheet, admitted by its own sha256 —
	// the one inline case a strict policy can carry safely.
	InlineStyle string

	// ScriptOrigins are off-origin hosts admitted in script-src, for an
	// external <script> Raw references.
	ScriptOrigins []string

	// StyleOrigins are off-origin hosts admitted in style-src, for an
	// external stylesheet Raw references.
	StyleOrigins []string

	// FontOrigins are extra origins font files may be fetched from. A remote
	// stylesheet's own origin does not cover this: the origin its @font-face
	// rules point at is inside a file via never sees, so it has to be
	// declared separately.
	FontOrigins []string
}

// WithDocumentHead sets the document shell for every page this app serves —
// title, lang, raw head markup, one inline stylesheet, and the off-origin
// hosts that markup needs the CSP to admit.
//
// Invalid heads panic at startup rather than serving a broken document: a
// Lang that isn't a language tag, an InlineStyle containing "</style", or an
// origin list entry that isn't an absolute http(s) origin.
func WithDocumentHead(head Head) Option {
	return func(c *config) { c.head = head }
}

// validate rejects a malformed Head at startup. Every one of these would
// otherwise surface as a blocked request in the browser, long after Register
// returned.
func (hd Head) validate() {
	if hd.Lang != "" && !isLangTag(hd.Lang) {
		panic("via: WithDocumentHead: Lang " + quote(hd.Lang) + " is not a language tag (want e.g. \"en\" or \"pt-PT\")")
	}
	if strings.Contains(strings.ToLower(hd.InlineStyle), "</style") {
		panic("via: WithDocumentHead: InlineStyle contains \"</style\", which would end the element early")
	}
	validOrigins("ScriptOrigin", hd.ScriptOrigins)
	validOrigins("StyleOrigin", hd.StyleOrigins)
	validOrigins("FontOrigin", hd.FontOrigins)
}

// render writes the head's elements. Title is escaped; Raw is emitted
// verbatim (the app's own markup, not via's to sanitize); InlineStyle is
// escaped-free too — "</style" is the only sequence that could end the
// element early, and that's rejected at startup.
func (hd Head) render(b *strings.Builder) {
	if hd.Title != "" {
		b.WriteString("<title>" + html.EscapeString(hd.Title) + "</title>")
	}
	b.WriteString(hd.Raw)
	if hd.InlineStyle != "" {
		b.WriteString("<style>" + hd.InlineStyle + "</style>")
	}
}

// htmlOpen returns the <html> open tag, carrying lang when declared.
func (hd Head) htmlOpen() string {
	if hd.Lang == "" {
		return "<html>"
	}
	return `<html lang="` + html.EscapeString(hd.Lang) + `">`
}

// isLangTag accepts the shape of a BCP 47 tag — alphanumeric subtags joined by
// hyphens. It is a syntax gate, not a registry lookup: the point is to reject
// anything that could break out of the attribute or carry markup, not to police
// which languages exist.
func isLangTag(s string) bool {
	if len(s) > 35 {
		return false
	}
	for sub := range strings.SplitSeq(s, "-") {
		if sub == "" {
			return false
		}
		for i := range len(sub) {
			c := sub[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				return false
			}
		}
	}
	return s != ""
}

// originOf returns scheme://host for an absolute http(s) URL, or "" for a
// relative URL (same-origin) or anything else.
func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// validOrigins panics if any entry in list is not an absolute http(s) origin
// — name (e.g. "ScriptOrigin") only names the field in the panic message.
func validOrigins(name string, list []string) {
	for _, o := range list {
		if originOf(o) == "" {
			panic("via: WithDocumentHead: " + name + " " + quote(o) + " is not an absolute http(s) origin")
		}
	}
}

func quote(s string) string { return `"` + s + `"` }
