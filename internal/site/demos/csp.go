package demos

import "github.com/go-via/via/h"

// CSPNotes describes the header rather than reading it: via exports no
// accessor for a mount's policy, and Ctx has no response writer.
type CSPNotes struct{}

type cspRow struct{ directive, from string }

// Each directive is spelled exactly as in the header: site/csp_test.go scrapes
// them from the rendered /platform page and checks them against its real
// response header.
var cspRows = []cspRow{
	{"default-src 'self'", "the floor — nothing on the page may load from another origin unless a directive below widens it"},
	{"script-src 'self' 'unsafe-eval'", "three 'sha256-…' sources follow these in the header: via's own inline scripts (client events, reconnect, redirect), hashed into every mount's policy even where the page ships only some of them — the reconnect manager is served on live pages only. 'unsafe-eval' is required because the bundled Datastar client compiles every data-* expression with the Function constructor"},
	{"style-src 'self'", "via emits no <style> element and no style attribute of its own"},
	{"object-src 'none'", "fixed"},
	{"base-uri 'self'", "fixed"},
	{"form-action 'self'", "fixed"},
	{"frame-ancestors 'self'", "fixed"},
}

var cspAbsent = []cspRow{
	{"font-src", "absent unless an Assets.Preload with As \"font\" names another origin, or an Assets.FontOrigins is declared, each a bare scheme://host[:port] — this site's font preloads are relative, so 'self' already covers them"},
	{"img-src", "absent unless an Assets.Preload with As \"image\" names another origin"},
}

var cspWidens = []cspRow{
	{"Assets.Scripts{Src: \"https://cdn…\"}", "that origin joins script-src; a relative Src is already covered by 'self', and a protocol-relative //cdn… one panics at startup"},
	{"Assets.Scripts{Inline: …}", "the sha256 of the body as the browser parses it (CRLF and CR as LF, NUL as U+FFFD) joins script-src"},
	{"Assets.Styles{Href / Inline}", "the same two, on style-src"},
	{"Assets.Preload{As: \"script\" | \"style\" | \"font\" | \"image\"}", "an absolute Href's origin joins the matching directive — script-src, style-src, font-src or img-src; a relative one is already 'self'"},
	{"Head.Raw", "refuses a <script> or <style> outright — via never parses Raw, so the policy could not admit it"},
}

func cspList(label, class string, list []cspRow) h.H {
	items := []h.H{h.Class("reflist")}
	for _, r := range list {
		items = append(items, h.Li(h.Code(h.Class(class), h.Str(r.directive)), h.Str(" — "), h.Str(r.from)))
	}
	return h.Div(h.P(h.Str(label)), h.Ul(items...))
}

func (c *CSPNotes) View() h.H {
	return h.Div(
		cspList("This page's policy, directive by directive:", "csp-has", cspRows),
		cspList("Not in the header at all:", "csp-absent", cspAbsent),
		cspList("What widens it, from the router's WithHead assets and the mount's PageMeta().Assets:", "csp-widens", cspWidens),
		h.P(h.Class("note"), h.Str("To read the real header: open the Network tab, pick the document request, "),
			h.Code(h.Str("Response Headers → Content-Security-Policy")), h.Str(" — or run:")),
		h.Pre(h.Code(h.Str("curl -sI http://localhost:8080/platform | grep -i content-security-policy"))),
	)
}
