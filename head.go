package via

import (
	"html"
	"net/url"
	"strings"
)

// Head describes the document shell. Title/Lang/InlineStyle are the fixed,
// security-relevant slots; Raw is the escape hatch for everything else,
// emitted verbatim. The origin lists declare which off-origin hosts the CSP
// must admit for what Raw references — via cannot derive them without sniffing
// arbitrary HTML for security-relevant meaning, so the app states them, one
// list per directive so a stylesheet CDN is not also trusted for script.
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
	// rules point at lives inside a file via never sees.
	FontOrigins []string
}

// WithHead sets the document shell for every page this app serves —
// title, lang, raw head markup, one inline stylesheet, and the off-origin
// hosts that markup needs the CSP to admit.
//
// Invalid heads panic at startup rather than serving a broken document: a
// Lang that isn't a language tag, an InlineStyle containing "</style", or an
// origin list entry that isn't an absolute http(s) origin.
func WithHead(head Head) Option {
	return func(c *config) { c.head = head }
}

// validate rejects a malformed Head at startup; each of these would otherwise
// surface as a blocked browser request long after Handler returned.
func (hd Head) validate() {
	if hd.Lang != "" && !isLangTag(hd.Lang) {
		panic("via: WithHead: Lang " + quote(hd.Lang) + " is not a language tag (want e.g. \"en\" or \"pt-PT\")")
	}
	if strings.Contains(strings.ToLower(hd.InlineStyle), "</style") {
		panic("via: WithHead: InlineStyle contains \"</style\", which would end the element early")
	}
	validOrigins("ScriptOrigin", hd.ScriptOrigins)
	validOrigins("StyleOrigin", hd.StyleOrigins)
	validOrigins("FontOrigin", hd.FontOrigins)
}

// render writes the head's elements. Raw and InlineStyle are emitted verbatim:
// the app's own markup, and "</style" — the one sequence that could end the
// element early — is rejected at startup.
//
// page is the root composition's own declaration (see [Titler]): a non-empty
// title replaces the router-wide one. It is escaped here and touches no CSP
// slot — which is why a page is allowed to declare it and not the rest of the
// Head.
func (hd Head) render(b *strings.Builder, page string) {
	title := hd.Title
	if page != "" {
		title = page
	}
	if title != "" {
		b.WriteString("<title>" + html.EscapeString(title) + "</title>")
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

// isLangTag is a syntax gate on BCP 47 shape, not a registry lookup: the point
// is to reject anything that could break out of the attribute or carry markup,
// not to police which languages exist.
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

// validOrigins panics if any entry is not an absolute http(s) origin; name
// only labels the field in the message.
func validOrigins(name string, list []string) {
	for _, o := range list {
		if originOf(o) == "" {
			panic("via: WithHead: " + name + " " + quote(o) + " is not an absolute http(s) origin")
		}
	}
}

func quote(s string) string { return `"` + s + `"` }

// Titler lets the ROOT page composition name itself, overriding the
// router-wide [Head].Title — the fix for a multi-page app in which every page
// otherwise shares one title. It is a method rather than a struct field
// because a real title is data-dependent, and it runs AFTER OnInit (and
// OnReload), so the data is already loaded:
//
//	func (p *Ticket) Title() string { return "#" + strconv.Itoa(p.t.ID) + " " + p.t.Subject }
//
// It shapes the DOCUMENT, so it takes effect on a render that writes one: the
// GET, and the full-page response to a native <form> submit. An SSE push
// patches elements inside <body> and never rewrites the head, so a title that
// changes while a tab is connected does not move the tab strip — render the
// changing part in the page instead. Returning "" keeps the router-wide title.
// The value is HTML-escaped.
//
// ONLY THE ROOT'S counts. An embedded child's Title is ignored — a nested unit
// may not rename the page it happens to sit in — and Embed logs one line
// naming the type when it sees one, because the method looks like it works.
//
// There is deliberately no Description sibling: see the duck-typed-method rule
// in AGENTS.md. A future need is one Meta hook subsuming Title, not a fifth
// method.
//
// Duck-typed like [Initer], so pin it: var _ via.Titler = (*Ticket)(nil).
type Titler interface{ Title() string }

// pageTitleOf reads the root's declaration at write time — after OnInit and
// OnReload have loaded the data a title is usually derived from. "" when the
// root declares none, which keeps the router-wide [Head].Title.
func pageTitleOf(root any) string {
	if t, ok := root.(Titler); ok {
		return t.Title()
	}
	return ""
}
