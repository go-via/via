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
		"redirects, the Content-Security-Policy and its per-document nonce, request limits, and middleware around the Router.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

const originNotice = "via: no WithTrustedOrigin set, so actions accept requests from any origin: any site can fire an action that needs no session, and a page on a sibling subdomain, or this host over http, can fire one as the signed-in user. Set WithTrustedOrigin in production"

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
 script-src 'self' 'nonce-KSfGODYz22EjHLF6Vse7nw' 'sha256-qgomxTCf6EyY9wRYTe+McNJfTHmvsnuHqNwURb6wkX0=' 'sha256-LqIkD2ynqYz8zKSe58JjW3qRObZHPM+TRdfHp4SBKu4=' 'sha256-2TX8Ens+wrX3VPIQX1+1OWZMcSu8dc4f2EwNYTMzJTs='
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
		h.P(API("via.WithTrustedOrigin"), h.Str(" adds the origin check in front of every action, form submit and stream "+
			"connect, in this order: an "),
			Code("Origin"), h.Str(" on the allowlist passes; otherwise a "), Code("Sec-Fetch-Site"), h.Str(" header must be "),
			Code("same-origin"), h.Str(" or "), Code("none"), h.Str("; otherwise the "), Code("Origin"),
			h.Str(" host must equal the request's "), Code("Host"),
			h.Str(", and be https when the request arrived over TLS or the proxy says "), Code("X-Forwarded-Proto: https"),
			h.Str(". A request with neither header fails. A refusal is "), Code("403 forbidden origin"), h.Str(". "+
				"Behind a proxy, list the public origin: the allowlist is checked first, so a rewritten Host does not matter. "+
				"A listed origin is compared as the browser sends it: host case, a default port and a trailing \"/\" do not "+
				"matter, and a value with a path, query, fragment or userinfo panics at startup.")),
		snippet.Text("shell", crossOriginPOST),
		Callout(Warning, "Without it, every origin is accepted",
			h.P(h.Str("A plain action (on a page with nothing live) has no tab id to check, so any site can post to it. "+
				"The session cookie is "), Code("SameSite=Lax"), h.Str(", so the browser leaves it off a cross-site POST and the "+
				"handler runs signed out, but a sibling subdomain counts as the same site. Look for this line at startup:")),
			snippet.Text("", originNotice),
		),

		d.H2("The session cookie"),
		table([]string{"Property", "Value", "Change it with"},
			[]h.H{h.Str("HttpOnly"), h.Str("Always."), h.Str("Nothing.")},
			[]h.H{h.Str("SameSite"), h.Str("Lax."), h.Str("Nothing.")},
			[]h.H{h.Str("Secure"), h.Span(h.Str("When the request arrived over TLS, or the proxy says "),
				Code("X-Forwarded-Proto: https"), h.Str(" or "), Code("Forwarded: proto=https"), h.Str(".")),
				h.Span(API("via.WithSecureCookies"), h.Str(" behind a TLS-terminating proxy that sends neither."))},
			[]h.H{h.Str("Name"), Code("via_session"),
				h.Span(API("via.WithSessionCookieName"), h.Str(": give each app its own when two share a host."))},
			[]h.H{h.Str("Lifetime"), h.Str("24h idle, sliding on the server. The cookie's Max-Age is the TTL, set when the cookie is issued or rotated and not refreshed by use."), API("via.WithSessionTTL")},
			[]h.H{h.Str("Signature"), h.Str("HMAC-SHA256 over the id. A key shorter than 16 bytes panics at startup."),
				h.Span(API("via.WithSessionKey"), h.Str(", or the "), Code("VIA_SESSION_KEY"), h.Str(" environment variable."))},
		),
		h.P(h.Str("With no key set, via generates one per process and warns on the first session: cookies then die on every "+
			"restart and are invalid on a second process. The cookie holds only a signed id, never data.")),
		h.P(h.Str("The proxy header is trusted as sent: a client that forges it only marks its own cookie Secure.")),
		h.P(h.Str("A response that resolved a session or sets the cookie is sent "), Code("Cache-Control: private, no-store"),
			h.Str(", so no shared cache hands one user's page to another and the back button does not show a signed-in "+
				"page after logout. A live page gets the same, session or not, over any Cache-Control a middleware set, "+
				"since its HTML carries the tab id. A plain page with no Cache-Control gets "), Code("no-cache"),
			h.Str("; one the app set is kept. The SSE connect gets at least "), Code("no-cache"),
			h.Str(", over any a middleware set. An action response with no session behind it gets none from via.")),

		d.H2("Redirects"),
		snippet.Region("security/router.go", "redirect", snippet.Mark("ctx.Redirect", "ctx.RedirectExternal")),
		h.P(API("via.Ctx.Redirect"), h.Str(" follows a relative path, or an absolute URL whose host is the request's "),
			Code("Host"), h.Str(" or whose origin is listed with "), API("via.WithTrustedOrigin"),
			h.Str(". Any other target is dropped and logged, like a "), Code("javascript:"),
			h.Str(" one: the action answers with its render, and an OnInit answers 500. The host is read the way a "+
				"browser reads it, so "), Code(`https://app.example@evil.example`), h.Str(" and "),
			Code(`https://evil.example\@app.example`), h.Str(" both name evil.example. "), API("via.Ctx.RedirectExternal"),
			h.Str(" leaves the site, for an OAuth provider or a payment page; it still refuses any scheme but http and "+
				"https. Build its target yourself rather than passing one from the request.")),

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
			[]h.H{API("via.Session.Rotate"), h.Str("New cookie id, same data, old id deleted. Returns the new id. Every other open tab on the session reloads under the new cookie.")},
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
			[]h.H{h.Code(h.Class("csp-has"), h.Str("script-src 'self' 'nonce-")), h.Str("A nonce fresh per document, which Datastar compiles expressions under, plus a sha256 hash for each of via's inline scripts.")},
			[]h.H{h.Code(h.Class("csp-absent"), h.Str("'unsafe-eval'")), h.Span(h.Str("No script on the page may evaluate a string. "),
				API("via.WithUnsafeEval"), h.Str(" adds it, for a library that needs it."))},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("style-src 'self'")), h.Str("via emits no style element and no style attribute.")},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("object-src 'none'")), h.Str("Fixed.")},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("base-uri 'self'")), h.Str("Fixed.")},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("form-action 'self'")), h.Str("Fixed.")},
			[]h.H{h.Code(h.Class("csp-has"), h.Str("frame-ancestors 'self'")), h.Str("Fixed.")},
			[]h.H{h.Code(h.Class("csp-absent"), h.Str("img-src")), h.Str("Added only when a preload names an image on another origin.")},
		),
		h.P(h.Str("The policy is built once per mount from the router's "), API("via.WithHead"), h.Str(" assets plus the page's "),
			APIText("via.Meta", "PageMeta().Assets"), h.Str(": a script or stylesheet origin you declare joins its directive, "+
				"an inline body joins by hash. Its shape never varies per request, which is why Assets must be a constant of the type; "+
				"only the nonce changes, once per document.")),
		h.P(h.Str("Datastar compiles each "), Code("data-*"), h.Str(" expression as a script carrying the nonce, never through "),
			Code("Function"), h.Str(". The nonce holds for the document's life, so expressions that arrive later in a patch "+
				"compile too. A second policy, from middleware or a proxy, that lacks the nonce blocks all of them.")),
		h.P(h.Str("A library that compiles code from strings ("), Code("eval"), h.Str(", "), Code("new Function"), h.Str(", "),
			Code(`setTimeout("…")`), h.Str(") needs "), Code("'unsafe-eval'"), h.Str(". "), API("via.WithUnsafeEval"),
			h.Str(" adds it to every page's "), Code("script-src"), h.Str(" and keeps the nonce and hashes. A string that reaches "+
				"one of those calls then runs as script, so via warns at startup while it is set. Prefer a build of the library "+
				"that does not evaluate strings.")),
		h.P(h.Str("Datastar also stamps that nonce on every "), Code("<script>"), h.Str(" a patch inserts, and on a "),
			Code("text/javascript"), h.Str(" response, so a script that arrives in a live push runs even though the same "+
				"element in the first document is blocked. That is why "), Code(`h.El("script", …)`),
			h.Str(" panics: scripts belong in "), APIText("via.Meta", "Meta.Assets"), h.Str(", where the policy names them.")),
		h.P(h.Str("The nonce is only as fresh as the document. A plain page is sent "), Code("Cache-Control: no-cache"),
			h.Str(" so each view is refetched with its own; an app that sets a longer-lived Cache-Control on a page shares "+
				"that page's nonce with every viewer the cache serves, and an injection that reads it once can reuse it.")),
		Callout(Warning, "A data-* attribute is code",
			h.P(h.Str("Datastar compiles every expression the DOM holds under the page's nonce, so the CSP does not stop an injected "+
				"one: anyone who controls a "),
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
			[]h.H{API("via.WithPinnedDeadline"), h.Str("5s"), h.Span(h.Str("How long an action waits for its tab's goroutine to pick it up, "), Code("503 stream busy"), h.Str("; and for a stream still connecting, at most 2s, "), Code("410"), h.Str("."))},
		),
		h.P(h.Str("The stream cap is router-wide, with no per-client share: one client can hold all of it. Set it to what the "+
			"machine has memory for, since each stream holds a goroutine and a composition tree for the tab's life. A per-client "+
			"limit belongs at the proxy, which sees the client's address; see "),
			h.A(h.Href(d.Href("/deploy")+"#limits-and-dead-peers"), h.Str("Limits and dead peers")), h.Str(".")),

		d.H2("Middleware"),
		snippet.Region("security/middleware.go", "middleware", snippet.Mark("Unwrap()", "accessLog(securityHeaders(app))")),
		h.P(API("via.Router"), h.Str(" is an "), Code("http.Handler"), h.Str(", so logging, rate limits, auth redirects and "+
			"extra headers are ordinary net/http wrappers; via has no middleware API of its own. A wrapper sees page GETs, "+
			"action POSTs under "), Code("/_via/a/"), h.Str(" and stream connects under "), Code("/_via/sse"),
			h.Str(" alike. A wrapper that replaces the ResponseWriter must expose "), Code("Unwrap() http.ResponseWriter"),
			h.Str(", or via cannot flush and every stream connect answers 500.")),
	)
}
