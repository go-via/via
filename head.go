package via

import (
	"html"
	"maps"
	"net/url"
	"slices"
	"strings"
)

// Head describes the ROUTER-WIDE document shell: the <html lang>, raw head
// markup, and the assets every page of the app carries. Per-page slots — the
// title and the rest of [Meta] — belong to the mounted page, not here.
//
// Raw is emitted verbatim and via never parses it, so it may not carry a
// <script> or <style>: those are CSP-governed and must be declared in Assets,
// where buildCSP can see them.
//
// The zero Head means "inherit": an empty field is not rendered and does not
// widen the policy, so a partially-filled Head is always valid.
type Head struct {
	// Lang is the <html lang> value (e.g. "en", "pt-PT"). Empty omits it.
	Lang string

	// Raw is head markup emitted verbatim after <meta charset> — icons,
	// viewport meta, whatever the app needs. Scripts and styles are refused:
	// declare them in Assets.
	Raw string

	// Assets are the scripts, styles and preloads every page carries. They
	// widen the CSP of every mount.
	Assets Assets
}

// Meta is what a mounted page declares about its own document, via the
// [PageMetaer] hook. Everything but Assets is INERT: escaped text written into
// the head, free to depend on data OnInit loaded.
//
// Assets is not inert — it decides the page's Content-Security-Policy, which is
// built once at Mount. It must therefore be a CONSTANT of the type: via reads
// it at Mount from the mounted literal, again at Mount from a probe copy whose
// zero fields are filled in, and again on every document render. It panics if
// any two disagree — at Mount for a page the probe can see through, on the
// first render for one it cannot.
type Meta struct {
	// Title is the document title. Empty means no <title> element.
	Title string

	// Description is the <meta name="description"> content. Empty omits it.
	Description string

	// Canonical is the <link rel="canonical"> href. Empty omits it.
	Canonical string

	// Robots is the <meta name="robots"> content ("noindex", …). Empty omits it.
	Robots string

	// OG are Open Graph properties without their prefix — "title" becomes
	// <meta property="og:title">. Rendered in sorted key order.
	OG map[string]string

	// Twitter are Twitter card names without their prefix — "card" becomes
	// <meta name="twitter:card">. Rendered in sorted key order.
	Twitter map[string]string

	// Assets are this page's own scripts, styles and preloads. MUST be a
	// constant of the type: see the type doc.
	Assets Assets
}

// Assets is the CSP-governed half of the document head: everything that loads
// or executes. Declaring it here is what lets via derive a policy that admits
// it — markup smuggled through [Head].Raw would be blocked by the browser with
// no via-side signal, which is why Raw refuses scripts and styles.
type Assets struct {
	Scripts []Script
	Styles  []Style
	Preload []Preload

	// FontOrigins are origins font files may be fetched from. A remote
	// stylesheet's own origin does not cover this: the origin its @font-face
	// rules point at lives inside a file via never sees.
	FontOrigins []string
}

// Script is one <script>. Exactly one of Src and Inline is set: Src is a URL
// (relative, or an absolute http(s) one whose origin joins script-src), Inline
// is source admitted by the sha256 of its exact bytes.
type Script struct {
	Src    string
	Inline string
	Module bool
	Defer  bool
}

// Style is one stylesheet — Href for a <link rel="stylesheet">, Inline for a
// <style> admitted by the sha256 of its exact bytes. Exactly one is set.
type Style struct {
	Href   string
	Inline string
}

// Preload is one <link rel="preload">. As is the destination — "script",
// "style", "font" or "image" — and names the directive the href widens.
type Preload struct {
	Href string
	As   string
}

// PageMetaer lets the ROOT page composition describe its own document: title,
// description, social cards, and the assets the page needs.
//
//	func (p *Ticket) PageMeta() via.Meta { return via.Meta{Title: "#" + p.id} }
//
// It is a method rather than a struct field because real metadata is
// data-dependent, and it runs AFTER OnInit (and OnReload), so the data is
// already loaded. The inert fields are HTML-escaped.
//
// Assets is the exception and is checked as one: it is read at Mount from the
// literal you mounted, before any request, because the CSP is built there — one
// string per mount, none per request. A PageMeta whose Assets vary with the
// page's data panics at Mount; one that varies on something the boot probe
// cannot reach (a slice OnInit fills) panics on the first GET instead.
//
// It shapes the DOCUMENT, so it takes effect on a render that writes one: the
// GET, and the full-page response to a native <form> submit. An SSE push
// patches elements inside <body> and never rewrites the head.
//
// ONLY THE ROOT'S counts. An embedded child's PageMeta is ignored — a nested
// unit may not rename the page it happens to sit in — and Child logs one line
// naming the type when it sees one, because the method looks like it works.
//
// Duck-typed like [Initer], so pin it: var _ via.PageMetaer = (*Ticket)(nil).
type PageMetaer interface{ PageMeta() Meta }

// pageMetaOf reads the root's declaration. At write time that is after OnInit
// and OnReload have loaded the data metadata is usually derived from; at Mount
// it is the literal you mounted, which is what makes the Assets constancy check
// meaningful.
func pageMetaOf(root any) Meta {
	if m, ok := root.(PageMetaer); ok {
		return m.PageMeta()
	}
	return Meta{}
}

// WithHead sets the router-wide document shell: lang, raw head markup, and the
// assets every page carries.
//
// Invalid heads panic at startup rather than serving a broken document: a Lang
// that isn't a language tag, a Raw carrying a script or style, or an asset that
// fails [Assets] validation.
func WithHead(head Head) Option {
	return func(c *config) { c.head = head }
}

// validate rejects a malformed Head at startup; each of these would otherwise
// surface as a blocked browser request long after Handler returned.
func (hd Head) validate() {
	if hd.Lang != "" && !isLangTag(hd.Lang) {
		panic("via: WithHead: Lang " + quote(hd.Lang) + " is not a language tag (want e.g. \"en\" or \"pt-PT\")")
	}
	low := strings.ToLower(hd.Raw)
	for _, bad := range []string{"<script", "<style"} {
		if strings.Contains(low, bad) {
			panic("via: WithHead: Raw contains " + quote(bad) + " — via never parses Raw, so the CSP " +
				"cannot admit it and the browser would block it silently; declare it in Head.Assets instead")
		}
	}
	hd.Assets.validate("via: WithHead: Assets")
}

// validate rejects assets that cannot be served safely. where labels the
// declaration site (the option, or the page type whose PageMeta returned them).
func (a Assets) validate(where string) {
	for _, s := range a.Scripts {
		if (s.Src == "") == (s.Inline == "") {
			panic(where + ": Script must set exactly one of Src and Inline")
		}
		if strings.Contains(strings.ToLower(s.Inline), "</script") {
			panic(where + ": Script.Inline contains \"</script\", which would end the element early")
		}
		validAssetURL(where, "Script.Src", s.Src)
	}
	for _, s := range a.Styles {
		if (s.Href == "") == (s.Inline == "") {
			panic(where + ": Style must set exactly one of Href and Inline")
		}
		if strings.Contains(strings.ToLower(s.Inline), "</style") {
			panic(where + ": Style.Inline contains \"</style\", which would end the element early")
		}
		validAssetURL(where, "Style.Href", s.Href)
	}
	for _, p := range a.Preload {
		if !slices.Contains(preloadAs, p.As) {
			panic(where + ": Preload.As " + quote(p.As) + " is not one of " + strings.Join(preloadAs, ", "))
		}
		validAssetURL(where, "Preload.Href", p.Href)
	}
	for _, o := range a.FontOrigins {
		if originOf(o) == "" {
			panic(where + ": FontOrigin " + quote(o) + " is not an absolute http(s) origin")
		}
	}
}

var preloadAs = []string{"script", "style", "font", "image"}

// validAssetURL admits a relative URL (same-origin, covered by 'self') and an
// absolute http(s) one (its origin joins the directive). Anything else — a
// javascript: or data: URL above all — would be a source via cannot express in
// the policy, so it fails at startup.
func validAssetURL(where, field, raw string) {
	if raw == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		panic(where + ": " + field + " " + quote(raw) + " is not a URL")
	}
	if u.Scheme != "" && originOf(raw) == "" {
		panic(where + ": " + field + " " + quote(raw) + " is not a relative URL or an absolute http(s) one")
	}
}

// fingerprint is the Assets identity the constancy check compares. It is a
// string and not reflect.DeepEqual because via's core stays reflection-free
// off the memoized type-setup path (see TestCore_importsNoReflectPackage), and
// because a string is comparable in one op against the one stored at Mount.
func (a Assets) fingerprint() string {
	var b strings.Builder
	for _, s := range a.Scripts {
		b.WriteString("S\x00" + s.Src + "\x00" + s.Inline + "\x00")
		if s.Module {
			b.WriteString("m")
		}
		if s.Defer {
			b.WriteString("d")
		}
		b.WriteString("\x00")
	}
	for _, s := range a.Styles {
		b.WriteString("L\x00" + s.Href + "\x00" + s.Inline + "\x00")
	}
	for _, p := range a.Preload {
		b.WriteString("P\x00" + p.Href + "\x00" + p.As + "\x00")
	}
	for _, o := range a.FontOrigins {
		b.WriteString("F\x00" + o + "\x00")
	}
	return b.String()
}

// render writes the inert head slots. Every value is escaped: Meta is data, not
// markup, and none of it may close its own element.
func (m Meta) render(b *strings.Builder) {
	if m.Title != "" {
		b.WriteString("<title>" + html.EscapeString(m.Title) + "</title>")
	}
	if m.Description != "" {
		metaTag(b, "name", "description", m.Description)
	}
	if m.Robots != "" {
		metaTag(b, "name", "robots", m.Robots)
	}
	if m.Canonical != "" {
		b.WriteString(`<link rel="canonical" href="` + html.EscapeString(m.Canonical) + `">`)
	}
	for _, k := range slices.Sorted(maps.Keys(m.OG)) {
		metaTag(b, "property", "og:"+k, m.OG[k])
	}
	for _, k := range slices.Sorted(maps.Keys(m.Twitter)) {
		metaTag(b, "name", "twitter:"+k, m.Twitter[k])
	}
}

func metaTag(b *strings.Builder, key, name, content string) {
	b.WriteString(`<meta ` + key + `="` + html.EscapeString(name) + `" content="` + html.EscapeString(content) + `">`)
}

// render writes the asset elements. Inline bodies are emitted verbatim — they
// are the app's own source, admitted by hash, and the one sequence that could
// end the element early is rejected at startup.
func (a Assets) render(b *strings.Builder) {
	for _, p := range a.Preload {
		b.WriteString(`<link rel="preload" href="` + html.EscapeString(p.Href) + `" as="` + p.As + `"`)
		if p.As == "font" {
			// A font preload without crossorigin is fetched twice: the CSS
			// fetch is anonymous-CORS and does not match the preload.
			b.WriteString(` crossorigin`)
		}
		b.WriteString(">")
	}
	for _, s := range a.Styles {
		if s.Href != "" {
			b.WriteString(`<link rel="stylesheet" href="` + html.EscapeString(s.Href) + `">`)
			continue
		}
		b.WriteString("<style>" + s.Inline + "</style>")
	}
	for _, s := range a.Scripts {
		attrs := ""
		if s.Module {
			attrs += ` type="module"`
		}
		if s.Defer {
			attrs += " defer"
		}
		if s.Src != "" {
			b.WriteString("<script" + attrs + ` src="` + html.EscapeString(s.Src) + `"></script>`)
			continue
		}
		b.WriteString("<script" + attrs + ">" + s.Inline + "</script>")
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

func quote(s string) string { return `"` + s + `"` }
