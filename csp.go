package via

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

// randomToken returns a 128-bit (the OWASP recommendation) URL-safe token,
// backing session ids and tab ids alike. rand.Read failing is an unrecoverable
// infrastructure fault, so it panics.
func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("via: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// sha256Source returns the CSP source expression admitting exactly src. CSP
// hash sources are STANDARD base64 (padded), not the URL-safe alphabet used
// elsewhere in via — the browser rejects a mismatch silently.
func sha256Source(src string) string {
	sum := sha256.Sum256([]byte(src))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// cspHeader is the strict Content-Security-Policy every HTML response carries.
//
// via admits its own inline scripts BY HASH, not by nonce. A hash authorises
// exactly the bytes via ships, so publishing it costs nothing and leaves no
// token for an injected <script nonce="…"> to borrow; it is also identical
// across pods and restarts with no shared signing key, which is what an
// action's injected redirect script needs when another pod served the document.
//
// 'unsafe-eval' is required, not optional: the bundled Datastar client compiles
// every data-* expression with the Function constructor, which CSP gates behind
// it. Drop it and every action binding is silently dead in the browser while
// every server-side test passes.
//
// style-src carries no inline allowance: via emits no <style> element and no
// style attribute (the reconnect banner sets .style via the CSSOM, which CSP
// does not gate). A Head's InlineStyle adds its own hash.
var cspHeader = buildCSP(Head{})

// buildCSP derives the policy from the Head alone, so it is exactly as wide as
// the app declared and stays a pure function of the config — every pod serving
// that config serves byte-identical bytes.
func buildCSP(head Head) string {
	var script strings.Builder
	script.WriteString("script-src 'self' 'unsafe-eval' " + sha256Source(reconnectInit) + " " + sha256Source(redirectInit))
	for _, o := range head.ScriptOrigins {
		script.WriteString(" " + o)
	}
	var style strings.Builder
	style.WriteString("style-src 'self'")
	for _, o := range head.StyleOrigins {
		style.WriteString(" " + o)
	}
	if head.InlineStyle != "" {
		style.WriteString(" " + sha256Source(head.InlineStyle))
	}
	csp := "default-src 'self'; " + script.String() + "; " + style.String() + "; "
	if len(head.FontOrigins) > 0 {
		csp += "font-src 'self' " + strings.Join(head.FontOrigins, " ") + "; "
	}
	return csp + "object-src 'none'; base-uri 'self'; frame-ancestors 'self'"
}

// writeSecurityHeaders sets the HTML content type plus nosniff and the strict
// CSP. Element-patch fragments take the default policy: a fragment carries no
// document, so its header is inert — only the page's policy governs what runs.
func writeSecurityHeaders(w http.ResponseWriter) { writeHeadersWithCSP(w, cspHeader) }

func writeHeadersWithCSP(w http.ResponseWriter, csp string) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", csp)
}
