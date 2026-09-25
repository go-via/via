package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
)

// Security is the page on sessions, the origin check, cookies, the CSP,
// request limits and wrapping the Router in middleware.
type Security struct {
	page
	Auth demos.SecurityAuth
}

func NewSecurity(env Env) Security { return Security{page: newPage("/security", env)} }

func (p *Security) PageMeta() via.Meta {
	m := p.meta("Sessions and Rotate, the origin check and CSRF, the session cookie's flags, " +
		"the Content-Security-Policy and its 'unsafe-eval', request limits, and middleware around the Router.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

const originWarning = "via: no WithTrustedOrigin set, so actions accept requests from any origin; set WithTrustedOrigin in production"

const crossOriginPOST = `# without WithTrustedOrigin: the cross-origin sign-in runs
$ curl -s -o /dev/null -w '%{http_code}\n' -H 'Origin: https://evil.example' \
    -F name=mallory -F _viatab= https://example.com/security/_via/a/1/<id>
303
# with WithTrustedOrigin("https://example.com")
$ curl -s -o /dev/null -w '%{http_code}\n' -H 'Origin: https://evil.example' \
    -F name=mallory -F _viatab= https://example.com/security/_via/a/1/<id>
403`

const signInTranscript = `$ curl -si -H 'Origin: https://example.com' \
    -F name=ada -F _viatab= https://example.com/security/_via/a/1/<id>
HTTP/1.1 303 See Other
Location: /security
Set-Cookie: via_session=<signed id>; Path=/; Max-Age=86400; HttpOnly; Secure; SameSite=Lax
Date: Fri, 25 Sep 2026 16:10:27 GMT
Content-Length: 0`

const cspTranscript = `$ curl -sI https://example.com/security \
    | grep -i '^content-security-policy' | tr ';' '\n'
Content-Security-Policy: default-src 'self'
 script-src 'self' 'unsafe-eval' 'sha256-qgomxTCf6EyY9wRYTe+McNJfTHmvsnuHqNwURb6wkX0=' 'sha256-w1BxZv52Hq0b9bKtw6PbfGN6HkmnMl6I3uT+H7Bwjsk=' 'sha256-2TX8Ens+wrX3VPIQX1+1OWZMcSu8dc4f2EwNYTMzJTs='
 style-src 'self'
 object-src 'none'
 base-uri 'self'
 form-action 'self'
 frame-ancestors 'self'`

func (p *Security) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("An action is an HTTP POST that any page on the web can aim at your server, so via treats the origin, "+
			"the session and the tab as three separate checks. By default the tab check and the cookie flags are on and the "+
			"origin check is off, which is right for localhost and wrong for production. Body and stream caps are router-wide "), Code("With*"),
			h.Str(" options, and the CSP is derived from declared assets, never from a request.")),

		d.H2("Origin checks and CSRF"),
		snippet.Region("security/router.go", "router", snippet.Mark("WithTrustedOrigin")),
		h.P(h.Str("Every action POST carries the tab's id in its body, a value same-origin script sets and the browser never "+
			"attaches on its own. It is the CSRF token: an action on a live tab runs only with that tab's id, and only under the "+
			"session the tab connected with. A request carrying another session's cookie answers 403 "), Code("session mismatch"),
			h.Str(", even with the right id.")),
		h.P(API("via.WithTrustedOrigin"), h.Str(" adds the origin check in front of every action, in this order: an "),
			Code("Origin"), h.Str(" on the allowlist passes; otherwise a "), Code("Sec-Fetch-Site"), h.Str(" header must be "),
			Code("same-origin"), h.Str(" or "), Code("none"), h.Str("; otherwise the "), Code("Origin"),
			h.Str(" host must equal the request's "), Code("Host"), h.Str(". A request with neither header fails. "+
				"Behind a proxy, list the public origin: the allowlist is checked first, so a rewritten Host does not matter.")),
		snippet.Text("shell", crossOriginPOST),
		Callout(Warning, "Without it, every origin is accepted",
			h.P(h.Str("A plain action (on a page with nothing live) has no tab id to check, so any site can post to it. "+
				"The session cookie is "), Code("SameSite=Lax"), h.Str(", so the browser leaves it off a cross-site POST and the "+
				"handler runs signed out, but a sibling subdomain counts as the same site. Look for this line at startup:")),
			snippet.Text("", originWarning),
		),

		d.H2("The session cookie"),
		table([]string{"Property", "Value", "Change it with"},
			[]h.H{h.Str("HttpOnly"), h.Str("Always."), h.Str("Nothing.")},
			[]h.H{h.Str("SameSite"), h.Str("Lax."), h.Str("Nothing.")},
			[]h.H{h.Str("Secure"), h.Str("Only when the request arrived over TLS (req.TLS is set)."),
				h.Span(API("via.WithSecureCookies"), h.Str(" behind a TLS-terminating proxy, where req.TLS is always nil."))},
			[]h.H{h.Str("Name"), Code("via_session"),
				h.Span(API("via.WithSessionCookieName"), h.Str(": give each app its own when two share a host."))},
			[]h.H{h.Str("Lifetime"), h.Str("24h idle, sliding on the server. The cookie's Max-Age is the TTL, set when the cookie is issued or rotated and not refreshed by use."), API("via.WithSessionTTL")},
			[]h.H{h.Str("Signature"), h.Str("HMAC-SHA256 over the id. A key shorter than 16 bytes panics at startup."),
				h.Span(API("via.WithSessionKey"), h.Str(", or the "), Code("VIA_SESSION_KEY"), h.Str(" environment variable."))},
		),
		h.P(h.Str("With no key set, via generates one per process and warns on the first session: cookies then die on every "+
			"restart and are invalid on a second process. The cookie holds only a signed id, never data.")),

		d.H2("Sessions and Rotate"),
		demo.Card(d.H3("Sign in, sign out"),
			h.P(API("via.Ctx.Session"), h.Str(" returns the browser's session. "), API("via.Session.Put"),
				h.Str(" stores one JSON value and issues the cookie on first write; "), API("via.Session.Get"),
				h.Str(" reads it back as a type. Both handlers call "), API("via.Session.Rotate"),
				h.Str(", which moves the data to a fresh cookie id and deletes the old one, so an id an attacker planted "+
					"before the login, or copied during it, resolves to nothing afterwards.")),
			via.Child(p.Auth), "security_auth.go", demo.WireOpen(),
			demo.Try(
				h.Str("Sign in, sign out, sign in again: each Rotate lists the id it retired, and Session.ID() never changes."),
				h.Str("Sign out and read the Wire pane: one POST, answered with a patch showing the next id. Its Set-Cookie is hidden from page script; the sign-in response below shows one."),
			)),
		snippet.Text("sign-in response", signInTranscript),
		table([]string{"Call", "Does"},
			[]h.H{API("via.Session.Put"), h.Str("Stores the value, replacing the last one. Does not rotate.")},
			[]h.H{API("via.Session.Get"), h.Str("Reads the value as T; false if none or if it no longer decodes.")},
			[]h.H{API("via.Session.Delete"), h.Str("Clears the value. The id and the cookie stay.")},
			[]h.H{API("via.Session.Rotate"), h.Str("New cookie id, same data, old id deleted. Returns the new id.")},
			[]h.H{API("via.Session.Ensure"), h.Str("Issues the cookie with no value, to key per-user state before there is any.")},
			[]h.H{API("via.Session.ID"), h.Str("A stable id that survives Rotate. Key your own data by it; it grants nothing.")},
		),
		h.P(h.Str("Rotate and the first Put need a response to carry the cookie: call them in "), Code("OnInit"),
			h.Str(" or an action, not in a Tick or Listen handler. Auth is a check in "), Code("OnInit"),
			h.Str(" and a branch in "), Code("View"), h.Str("; composition and the hooks are on "),
			h.A(h.Href(d.Href("/compositions")), h.Str("Compositions")), h.Str(".")),
		Callout(Warning, "Nothing rotates on its own",
			h.P(h.Str("Call Rotate at every auth-state change: login, logout, privilege change. Put does not rotate."))),

		d.H2("Session stores"),
		h.P(h.Str("The default store is a map in the process, so a restart signs everyone out and a second process sees none "+
			"of the first one's sessions. "), API("via.WithSessionStore"), h.Str(" takes a "), API("via.SessionStore"),
			h.Str(": Load, Save and Delete of opaque bytes by id, with a TTL on Save. Pair it with a stable "),
			API("via.WithSessionKey"), h.Str(": the key keeps the cookie valid, the store keeps the data behind it.")),
		h.P(h.Str("Two requests writing one session at once can lose a write against a plain store. Implement "),
			API("via.VersionedSessionStore"), h.Str(" (a conditional SaveIf) and via retries the merge instead. "),
			API("via.NewMemorySessionStore"), h.Str(" is the default and implements both. A store that fails a Load answers 503 "+
				"rather than serving the request signed out; bound its calls with "), API("via.WithSessionStoreTimeout"),
			h.Str(" (default 5s).")),

		d.H2("Content-Security-Policy"),
		snippet.Text("shell", cspTranscript),
		table([]string{"Directive", "Why"},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("default-src 'self'")), h.Str("Nothing loads from another origin unless a directive below widens it.")},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("script-src 'self' 'unsafe-eval'")), h.Str("Plus a sha256 hash for each of via's inline scripts. No nonce, so nothing an injected tag could borrow.")},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("style-src 'self'")), h.Str("via emits no style element and no style attribute.")},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("object-src 'none'")), h.Str("Fixed.")},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("base-uri 'self'")), h.Str("Fixed.")},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("form-action 'self'")), h.Str("Fixed.")},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("frame-ancestors 'self'")), h.Str("Fixed.")},
			[]h.H{h.Code(h.Class("csp-absent"), h.Str("img-src")), h.Str("Added only when a preload names an image on another origin.")},
		),
		h.P(h.Str("The policy is built once per mount from the router's "), API("via.WithHead"), h.Str(" assets plus the page's "),
			APIText("via.Meta", "PageMeta().Assets"), h.Str(": a script or stylesheet origin you declare joins its directive, "+
				"an inline body joins by hash. It never varies per request, which is why Assets must be a constant of the type.")),
		h.P(Code("'unsafe-eval'"), h.Str(" is required: the bundled Datastar client compiles every "), Code("data-*"),
			h.Str(" expression with the Function constructor. Without it every binding is dead in the browser while every "+
				"server-side test passes.")),
		Callout(Warning, "A data-* attribute is code",
			h.P(h.Str("The cost of 'unsafe-eval' is that the CSP does not stop an expression, so anyone who controls a "),
				Code("data-on:*"), h.Str(" or other Datastar attribute value runs script. The defence is the "), Code("h"),
				h.Str(" package: text and attribute values are escaped, and "), API("expr.Val"),
				h.Str(" encodes a Go value as a JavaScript literal. Passing user input to "), API("h.Data"), h.Str(", "),
				API("h.RawAttr"), h.Str(", "), API("expr.Raw"), h.Str(" or the format of "), API("expr.Rawf"),
				h.Str(" is script injection: HTML escaping keeps the attribute intact, not the expression inside it."))),

		d.H2("Request limits"),
		table([]string{"Option", "Default", "Caps, and the answer over it"},
			[]h.H{API("via.WithMaxBody"), h.Str("1 MiB"), h.Span(h.Str("An action POST body, and how much of a form submit stays in memory. "), Code("413 request body too large"), h.Str("."))},
			[]h.H{API("via.WithMaxUpload"), h.Str("8 MiB"), h.Span(h.Str("A PostForm submit's whole multipart body; the part past WithMaxBody spills to a temp file. "), Code("413 request body too large"), h.Str("."))},
			[]h.H{API("via.WithMaxSSEConn"), h.Str("10000"), h.Span(h.Str("Open live streams across the Router. "), Code("503 stream capacity reached"), h.Str("."))},
			[]h.H{API("via.WithPinnedDeadline"), h.Str("5s"), h.Span(h.Str("How long an action waits for its tab's goroutine to pick it up. "), Code("503 stream busy"), h.Str("."))},
		),
		h.P(h.Str("The stream cap is router-wide, with no per-client share: one client can hold all of it. Set it to what the "+
			"machine has memory for, since each stream holds a goroutine and a composition tree for the tab's life. A per-client "+
			"limit is middleware.")),

		d.H2("Middleware"),
		snippet.Region("security/middleware.go", "middleware", snippet.Mark("Unwrap()", "accessLog(securityHeaders(app))")),
		h.P(API("via.Router"), h.Str(" is an "), Code("http.Handler"), h.Str(", so logging, rate limits, auth redirects and "+
			"extra headers are ordinary net/http wrappers; via has no middleware API of its own. A wrapper sees page GETs, "+
			"action POSTs under "), Code("/_via/a/"), h.Str(" and stream connects under "), Code("/_via/sse"),
			h.Str(" alike. A wrapper that replaces the ResponseWriter must expose "), Code("Unwrap() http.ResponseWriter"),
			h.Str(", or via cannot flush and every stream connect answers 500.")),
	)
}
