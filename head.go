package via

import (
	"html"
	"net/url"
	"strings"

	"github.com/go-via/via/internal/hcore"
)

// Head describes the document shell — everything between <head> and the body
// via itself owns. It is a struct rather than an h.H because a head is not a
// tree of arbitrary markup: it is a fixed, small vocabulary whose members carry
// security weight. Every external origin named here has to reach the Content
// Security Policy, and a policy can only be derived from values via can see.
//
// The zero Head means "inherit": an empty field is not rendered and does not
// widen the policy, so a partially-filled Head is always valid.
type Head struct {
	// Title is the document title. Empty means no <title> element.
	Title string

	// Lang is the <html lang> value (e.g. "en", "pt-PT"). Empty omits it.
	Lang string

	// Meta are <meta> elements. via always emits <meta charset="utf-8"> itself;
	// a viewport meta is the usual first entry here.
	Meta []HeadMeta

	// Links are <link> elements — stylesheets, icons, preconnects. A stylesheet
	// pointing off-origin widens style-src to that origin automatically.
	Links []HeadLink

	// Scripts are external <script> elements. Inline script is deliberately not
	// expressible: via's CSP admits inline script only by hash, and a hash can
	// only be computed over bytes via controls.
	Scripts []HeadScript

	// InlineStyle is a single inline stylesheet, admitted by its own sha256 —
	// the one inline case a strict policy can carry safely.
	InlineStyle string

	// FontOrigins are extra origins font files may be fetched from. A remote
	// stylesheet's own origin is derived; the origin its @font-face rules point
	// at is inside a file via never sees, so it has to be declared.
	FontOrigins []string
}

// HeadMeta is one <meta> element. Exactly one of Name or Property must be set
// (Property carries the og:/twitter: family, which uses property= not name=).
type HeadMeta struct {
	Name     string
	Property string
	Content  string
}

// HeadLink is one <link> element. Rel and Href are required; the rest are
// emitted only when set.
type HeadLink struct {
	Rel         string
	Href        string
	Type        string
	Sizes       string
	Media       string
	Integrity   string
	CrossOrigin string
}

// HeadScript is one external <script> element. Src is required.
type HeadScript struct {
	Src         string
	Type        string
	Integrity   string
	CrossOrigin string
	Defer       bool
	Async       bool
}

// WithDocumentHead sets the document shell for every page this app serves —
// title, lang, meta, stylesheets, external scripts, one inline stylesheet.
//
// The head is also the app's declaration of which off-origin hosts it needs:
// via derives script-src, style-src and font-src from it, so a stylesheet or
// script listed here works under the strict CSP without any policy tuning, and
// a host NOT listed here stays blocked.
//
// Invalid heads panic at startup rather than serving a broken document: a link
// without Rel or Href, a script without Src, a meta with neither Name nor
// Property (or both), a non-http(s) URL, or a Lang that isn't a language tag.
func WithDocumentHead(head Head) Option {
	return func(c *config) { c.head = head }
}

// validate rejects a malformed Head at startup. Every one of these would
// otherwise surface as a silently missing element or a blocked request in the
// browser, long after Register returned.
func (hd Head) validate() {
	if hd.Lang != "" && !isLangTag(hd.Lang) {
		panic("via: WithDocumentHead: Lang " + quote(hd.Lang) + " is not a language tag (want e.g. \"en\" or \"pt-PT\")")
	}
	for _, m := range hd.Meta {
		switch {
		case m.Name == "" && m.Property == "":
			panic("via: WithDocumentHead: HeadMeta needs a Name or a Property")
		case m.Name != "" && m.Property != "":
			panic("via: WithDocumentHead: HeadMeta " + quote(m.Name) + " sets both Name and Property; pick one")
		}
	}
	for _, l := range hd.Links {
		if l.Rel == "" {
			panic("via: WithDocumentHead: HeadLink to " + quote(l.Href) + " needs a Rel")
		}
		if l.Href == "" {
			panic("via: WithDocumentHead: HeadLink " + quote(l.Rel) + " needs an Href")
		}
		if !hcore.SafeURL(l.Href) {
			panic("via: WithDocumentHead: HeadLink Href " + quote(l.Href) + " is not a safe URL")
		}
	}
	for _, s := range hd.Scripts {
		if s.Src == "" {
			panic("via: WithDocumentHead: HeadScript needs a Src (inline script is not expressible: the CSP admits it only by hash)")
		}
		if !hcore.SafeURL(s.Src) {
			panic("via: WithDocumentHead: HeadScript Src " + quote(s.Src) + " is not a safe URL")
		}
	}
	if strings.Contains(strings.ToLower(hd.InlineStyle), "</style") {
		panic("via: WithDocumentHead: InlineStyle contains \"</style\", which would end the element early")
	}
	for _, o := range hd.FontOrigins {
		if originOf(o) == "" {
			panic("via: WithDocumentHead: FontOrigin " + quote(o) + " is not an absolute http(s) origin")
		}
	}
}

// render writes the head's elements. Everything interpolated is an attribute
// value or text, so everything is escaped — a Head can come from configuration
// a user does not fully control.
func (hd Head) render(b *strings.Builder) {
	if hd.Title != "" {
		b.WriteString("<title>" + html.EscapeString(hd.Title) + "</title>")
	}
	for _, m := range hd.Meta {
		if m.Name != "" {
			b.WriteString(`<meta name="` + html.EscapeString(m.Name) + `"`)
		} else {
			b.WriteString(`<meta property="` + html.EscapeString(m.Property) + `"`)
		}
		b.WriteString(` content="` + html.EscapeString(m.Content) + `">`)
	}
	for _, l := range hd.Links {
		b.WriteString(`<link rel="` + html.EscapeString(l.Rel) + `" href="` + html.EscapeString(l.Href) + `"`)
		attr(b, "type", l.Type)
		attr(b, "sizes", l.Sizes)
		attr(b, "media", l.Media)
		attr(b, "integrity", l.Integrity)
		attr(b, "crossorigin", l.CrossOrigin)
		b.WriteString(">")
	}
	for _, s := range hd.Scripts {
		b.WriteString(`<script src="` + html.EscapeString(s.Src) + `"`)
		attr(b, "type", s.Type)
		attr(b, "integrity", s.Integrity)
		attr(b, "crossorigin", s.CrossOrigin)
		if s.Defer {
			b.WriteString(" defer")
		}
		if s.Async {
			b.WriteString(" async")
		}
		b.WriteString("></script>")
	}
	if hd.InlineStyle != "" {
		// No escaping: a stylesheet is not HTML, and "</style" is the only
		// sequence that could end the element early — rejected at startup.
		b.WriteString("<style>" + hd.InlineStyle + "</style>")
	}
}

func attr(b *strings.Builder, name, val string) {
	if val != "" {
		b.WriteString(" " + name + `="` + html.EscapeString(val) + `"`)
	}
}

// htmlOpen returns the <html> open tag, carrying lang when declared.
func (hd Head) htmlOpen() string {
	if hd.Lang == "" {
		return "<html>"
	}
	return `<html lang="` + html.EscapeString(hd.Lang) + `">`
}

// scriptOrigins, styleOrigins and fontOrigins are the off-origin hosts the head
// commits the policy to. Same-origin references return "" and are dropped:
// 'self' already covers them, and a narrower policy is a better policy.
func (hd Head) scriptOrigins() []string {
	var out []string
	for _, s := range hd.Scripts {
		out = append(out, originOf(s.Src))
	}
	return compactOrigins(out)
}

func (hd Head) styleOrigins() []string {
	var out []string
	for _, l := range hd.Links {
		if strings.Contains(l.Rel, "stylesheet") {
			out = append(out, originOf(l.Href))
		}
	}
	return compactOrigins(out)
}

func (hd Head) fontOrigins() []string {
	out := make([]string, 0, len(hd.FontOrigins))
	for _, o := range hd.FontOrigins {
		out = append(out, originOf(o))
	}
	return compactOrigins(out)
}

// isLangTag accepts the shape of a BCP 47 tag — alphanumeric subtags joined by
// hyphens. It is a syntax gate, not a registry lookup: the point is to reject
// anything that could break out of the attribute or carry markup, not to police
// which languages exist.
func isLangTag(s string) bool {
	if len(s) > 35 {
		return false
	}
	for _, sub := range strings.Split(s, "-") {
		if sub == "" {
			return false
		}
		for i := 0; i < len(sub); i++ {
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

// compactOrigins drops empties and duplicates, preserving declaration order so
// the policy a given config serves is byte-stable across pods and restarts.
func compactOrigins(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, o := range in {
		if o == "" || seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	return out
}

func quote(s string) string { return `"` + s + `"` }
