package via

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

// randomToken returns a 16-byte (128-bit, the OWASP recommendation) URL-safe
// base64 token. It backs session ids and per-connection tab ids — anywhere via
// needs an unguessable value. rand.Read failing is an unrecoverable
// infrastructure fault, so it panics.
func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("via: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// sha256Source returns the CSP source expression that admits exactly src as an
// inline script or stylesheet. CSP hash sources are STANDARD base64 (padded),
// not the URL-safe alphabet used elsewhere in via — the browser rejects a
// mismatch silently.
func sha256Source(src string) string {
	sum := sha256.Sum256([]byte(src))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// cspHeader is the strict Content-Security-Policy every HTML response carries.
//
// via admits its own inline scripts BY HASH rather than by nonce. Every inline
// script via emits is library-controlled and enumerable, which makes a hash
// strictly better on all three axes that matter here:
//
//   - Publishing it costs nothing. A hash authorises exactly the bytes via
//     already ships; knowing it lets an attacker execute nothing else. A nonce
//     is a bearer token — anything that learns it can run arbitrary script — so
//     it is only worth having if it is fresh per response and unguessable.
//   - It is identical across pods and restarts by construction, with no shared
//     signing key. That is what an action's injected redirect script needs: the
//     document that must admit it may have been served by another pod.
//   - It leaves no token for an injected <script nonce="…"> to borrow. A stable
//     nonce is readable with one curl, so an injection point anywhere becomes
//     script execution everywhere; a hash closes that off with nothing to steal.
//
// 'unsafe-eval' is required, not optional: the bundled Datastar client compiles
// every data-* expression (e.g. @post(...) on a click) with the Function
// constructor, which CSP gates behind 'unsafe-eval'. Drop it and every action
// binding is silently dead in the browser while every server-side test passes.
//
// style-src carries no inline allowance by default: via itself emits no <style>
// element and no style attribute (the reconnect banner sets .style via the
// CSSOM, which CSP does not gate). A Head's InlineStyle adds its own hash.
var cspHeader = buildCSP(Head{})

// buildCSP returns the policy for an app serving head. Everything off-origin the
// document needs is derived from the Head, so the policy is exactly as wide as
// what the app declared and no wider — and it stays a pure function of the
// config, so every pod serving that config serves byte-identical bytes.
func buildCSP(head Head) string {
	script := "script-src 'self' 'unsafe-eval' " + sha256Source(reconnectInit) + " " + sha256Source(redirectInit)
	for _, o := range head.scriptOrigins() {
		script += " " + o
	}
	style := "style-src 'self'"
	for _, o := range head.styleOrigins() {
		style += " " + o
	}
	if head.InlineStyle != "" {
		style += " " + sha256Source(head.InlineStyle)
	}
	csp := "default-src 'self'; " + script + "; " + style + "; "
	if fonts := head.fontOrigins(); len(fonts) > 0 {
		csp += "font-src 'self' " + strings.Join(fonts, " ") + "; "
	}
	return csp + "object-src 'none'; base-uri 'self'; frame-ancestors 'self'"
}

// writeSecurityHeaders sets the HTML content type and the default hardening
// headers (nosniff + the strict CSP) on an HTML response. Element-patch
// fragments use the default policy: a fragment carries no document of its own,
// so its header is inert — only the page's policy governs what runs.
func writeSecurityHeaders(w http.ResponseWriter) { writeHeadersWithCSP(w, cspHeader) }

func writeHeadersWithCSP(w http.ResponseWriter, csp string) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", csp)
}
