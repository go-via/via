package demos

import "github.com/go-via/via/h"

// CSPNotes is documentation, not a reading: via builds a mount's
// Content-Security-Policy once at Mount and exports no accessor for it, and
// Ctx has no response writer to read a header back from. The header itself is
// one Network-tab row away, so this unit says what produced it instead.
type CSPNotes struct{}

type cspRow struct{ directive, from string }

var cspRows = []cspRow{
	{"default-src 'self'", "the floor — nothing on the page may load from another origin unless a directive below widens it"},
	{"script-src 'self' 'unsafe-eval' 'sha256-…' 'sha256-…'", "the two hashes are via's own inline scripts (reconnect, redirect); 'unsafe-eval' is required because the bundled Datastar client compiles every data-* expression with the Function constructor"},
	{"style-src 'self'", "via emits no <style> element and no style attribute of its own"},
	{"font-src, img-src", "absent unless an Assets.Preload with As \"font\" or \"image\", or an Assets.FontOrigins, widens them"},
	{"object-src 'none'; base-uri 'self'; frame-ancestors 'self'", "fixed"},
}

var cspWidens = []cspRow{
	{"Assets.Scripts{Src: \"https://cdn…\"}", "that origin joins script-src; a relative Src is already covered by 'self'"},
	{"Assets.Scripts{Inline: …}", "the sha256 of those exact bytes joins script-src"},
	{"Assets.Styles{Href / Inline}", "the same two, on style-src"},
	{"Assets.Preload{As: \"font\" | \"image\"}", "that origin joins font-src / img-src"},
	{"Head.Raw", "refuses a <script> or <style> outright — via never parses Raw, so the policy could not admit it"},
}

func cspList(label string, list []cspRow) h.H {
	items := []h.H{h.Class("csp")}
	for _, r := range list {
		items = append(items, h.Li(h.Code(h.Str(r.directive)), h.Str(" — "), h.Str(r.from)))
	}
	return h.Div(h.P(h.Str(label)), h.Ul(items...))
}

func (c *CSPNotes) View() h.H {
	return h.Div(
		cspList("This page's policy, directive by directive:", cspRows),
		cspList("What widens it, from the router's WithHead assets and the mount's PageMeta().Assets:", cspWidens),
		h.P(h.Class("note"), h.Str("To read the real header: open the Network tab, pick the document request, "),
			h.Code(h.Str("Response Headers → Content-Security-Policy")), h.Str(" — or run:")),
		h.Pre(h.Code(h.Str("curl -sI http://localhost:8080/platform | grep -i content-security-policy"))),
	)
}
