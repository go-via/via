package via

import (
	"html"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/go-via/via/internal/render"
)

// Head describes the router-wide document shell: the <html lang>, the
// attributes on <html> and <body>, raw head markup, and the assets every page
// of the app carries. Per-page slots — the title and the rest of [Meta] —
// belong to the mounted page, not here.
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

	// HTMLAttrs and BodyAttrs are written on every page's <html> and <body>,
	// error pages included, after via's own. See [Meta] for how a page's are
	// laid over them.
	HTMLAttrs []Attr
	BodyAttrs []Attr

	// Raw is head markup emitted verbatim after <meta charset> — icons,
	// viewport meta, whatever the app needs. Scripts and styles are refused:
	// declare them in Assets.
	Raw string

	// Assets are the scripts, styles and preloads every page carries. They
	// widen the CSP of every mount.
	Assets Assets
}

// Meta is what a mounted page declares about its own document, via the
// PageMeta() Meta hook on the mounted root. The head slots are inert: escaped
// text written into the head, free to depend on data OnInit loaded, though a
// data-* attribute on <html> or <body> is live Datastar.
//
// Assets is not inert — it decides the page's Content-Security-Policy, which is
// built once at Mount. It must therefore be a constant of the type: via reads
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
	// <meta property="og:title">. Rendered in sorted key order. A non-nil map
	// defaults "title", "description" and "url" from Title, Description and
	// Canonical where it does not set them itself; a nil map renders nothing.
	OG map[string]string

	// Twitter are Twitter card names without their prefix — "card" becomes
	// <meta name="twitter:card">. Rendered in sorted key order.
	Twitter map[string]string

	// Assets are this page's own scripts, styles and preloads. Must be a
	// constant of the type: see the type doc.
	Assets Assets

	// HTMLAttrs and BodyAttrs are laid over [Head]'s by name, replacing in
	// place, with the rest appended; class values are joined instead. A
	// refused one panics at Mount, or is dropped and logged if only request
	// data produces it.
	HTMLAttrs []Attr
	BodyAttrs []Attr
}

// Attr is one attribute via writes on <html> or <body>, as Name="Value" with
// the value escaped. A name is a letter then [a-zA-Z0-9:._-]*, compared
// case-insensitively, at most once per list. via refuses the names it writes
// itself (lang, data-nonce, data-signals, data-init) and the ones the CSP
// blocks (style, on*).
type Attr struct{ Name, Value string }

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

// pageMetaer lets the root page composition describe its own document: title,
// description, social cards, and the assets the page needs. It is a method
// rather than a struct field because real metadata is data-dependent, and it
// is read after OnInit, so the data is already loaded.
//
// Assets is the exception and is checked as one: it is read at Mount from the
// literal you mounted, before any request, because the CSP is built there — one
// string per mount, none per request. See [Meta].
//
// Only the root's counts. An embedded child's PageMeta is ignored — a nested
// unit may not rename the page it happens to sit in — and Child logs one line
// naming the type when it sees one, because the method looks like it works.
//
// Duck-typed and unexported like initer; the contract callers read is in the
// package doc.
type pageMetaer interface{ PageMeta() Meta }

// pageMetaOf reads the root's declaration. At write time that is after OnInit
// has loaded the data metadata is usually derived from; at Mount
// it is the literal you mounted, which is what makes the Assets constancy check
// meaningful.
func pageMetaOf(root any) Meta {
	if m, ok := root.(pageMetaer); ok {
		return m.PageMeta()
	}
	return Meta{}
}

// WithHead sets the router-wide document shell: lang, raw head markup, the
// <html> and <body> attributes, and the assets every page carries.
//
// Invalid heads panic at startup rather than serving a broken document: a Lang
// that isn't a language tag, a Raw carrying a script or style, an attribute
// [Attr] refuses, or an asset that fails [Assets] validation.
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
	validateAttrs("via: WithHead: HTMLAttrs", hd.HTMLAttrs)
	validateAttrs("via: WithHead: BodyAttrs", hd.BodyAttrs)
}

// validateAttrs panics on the first attribute checkAttrs refuses.
func validateAttrs(where string, attrs []Attr) {
	if _, refused := checkAttrs(attrs); len(refused) > 0 {
		panic(where + ": " + refused[0])
	}
}

// checkAttrs splits attrs into the ones via writes and a reason for each it
// refuses.
func checkAttrs(attrs []Attr) (kept []Attr, refused []string) {
	seen := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		low := strings.ToLower(a.Name)
		why := ""
		switch {
		case !isAttrName(a.Name):
			why = "is not an attribute name (want a letter, then letters, digits, ':', '.', '_' or '-')"
		case low == "lang":
			why = "is written by via; set Head.Lang instead"
		case low == "data-nonce" || low == "data-signals" || low == "data-init":
			why = "is written by via"
		case strings.HasPrefix(low, "on"):
			why = "is an inline event handler, which the CSP blocks; use a data-on: attribute"
		case low == "style":
			why = "is an inline style, which the CSP blocks; use a class"
		case seen[low]:
			why = "is set twice"
		}
		seen[low] = true
		if why != "" {
			refused = append(refused, quote(a.Name)+" "+why)
			continue
		}
		kept = append(kept, a)
	}
	return kept, refused
}

func isAttrName(s string) bool {
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z') {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(":._-", c) >= 0) {
			return false
		}
	}
	return true
}

// mergeAttrs lays a page's attributes over the router's: see [Meta].
func mergeAttrs(router, page []Attr) []Attr {
	out := slices.Clone(router)
	for _, p := range page {
		i := slices.IndexFunc(out, func(a Attr) bool { return strings.EqualFold(a.Name, p.Name) })
		switch {
		case i < 0:
			out = append(out, p)
		case !strings.EqualFold(p.Name, "class"):
			out[i].Value = p.Value
		case out[i].Value == "":
			out[i].Value = p.Value
		case p.Value != "":
			out[i].Value += " " + p.Value
		}
	}
	return out
}

func attrString(attrs []Attr) string {
	var b strings.Builder
	for _, a := range attrs {
		b.WriteString(" " + a.Name + `="` + html.EscapeString(a.Value) + `"`)
	}
	return b.String()
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
		if !isBareOrigin(o) {
			panic(where + ": FontOrigin " + quote(o) + " is not an absolute http(s) origin (scheme://host[:port], no path)")
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
	// A protocol-relative URL has no scheme to read an origin off, so the CSP
	// would admit it only as 'self' and the browser would block the load.
	if u.Scheme == "" && !render.SafeURL(raw) {
		panic(where + ": " + field + " " + quote(raw) + " is protocol-relative; write it as https://… so its origin can join the CSP")
	}
}

// isBareOrigin reports whether o is exactly an http(s) origin, optionally with
// a trailing "/".
func isBareOrigin(o string) bool {
	if originOf(o) == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && u.User == nil && (u.Path == "" || u.Path == "/") &&
		!u.ForceQuery && u.RawQuery == "" && u.Fragment == ""
}

// fingerprint is the Assets identity the constancy check compares. It is a
// string and not reflect.DeepEqual because the check runs on every document
// render, where via's reflect use is limited to memo-key reads (see
// TestCore_reflectUseMatchesTheAllowlist), and because a string is comparable
// in one op against the one stored at Mount.
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
	og := m.openGraph()
	for _, k := range slices.Sorted(maps.Keys(og)) {
		metaTag(b, "property", "og:"+k, og[k])
	}
	for _, k := range slices.Sorted(maps.Keys(m.Twitter)) {
		metaTag(b, "name", "twitter:"+k, m.Twitter[k])
	}
}

// openGraph fills title/description/url from Meta on a copy of m.OG, leaving
// m.OG itself untouched for callers that inspect it.
func (m Meta) openGraph() map[string]string {
	if m.OG == nil {
		return nil
	}
	og := maps.Clone(m.OG)
	for k, v := range map[string]string{"title": m.Title, "description": m.Description, "url": m.Canonical} {
		if _, ok := og[k]; !ok && v != "" {
			og[k] = v
		}
	}
	return og
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

// htmlOpen renders <html>, carrying nonce for Datastar to compile expressions
// under when the document runs Datastar ("" for one that does not), then attrs.
func (hd Head) htmlOpen(nonce string, attrs []Attr) string {
	open := "<html"
	if hd.Lang != "" {
		open += ` lang="` + html.EscapeString(hd.Lang) + `"`
	}
	if nonce != "" {
		open += ` data-nonce="` + nonce + `"`
	}
	return open + attrString(attrs) + ">"
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
	// ';' would end the CSP directive and ',' start another policy; url.Parse
	// admits both in a host.
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(u.Host, ";,") {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func quote(s string) string { return `"` + s + `"` }
