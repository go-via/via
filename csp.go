package via

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// genCSPNonce returns a 16-byte (128-bit, the OWASP recommendation) URL-safe
// base64 nonce for a strict-CSP script-src. rand.Read failing is an
// unrecoverable infrastructure fault, so it panics.
func genCSPNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("via: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// cspPolicy is the strict Content-Security-Policy for a per-render nonce.
// 'unsafe-eval' is required, not optional: the bundled Datastar client compiles
// every data-* expression (e.g. @post(...) on a click) with the Function
// constructor, which CSP gates behind 'unsafe-eval'. Drop it and every action
// binding is silently dead in the browser while every server-side test passes.
func cspPolicy(nonce string) string {
	return "default-src 'self'; script-src 'self' 'nonce-" + nonce + "' 'unsafe-eval'; " +
		"style-src 'self' 'nonce-" + nonce + "'; " +
		"object-src 'none'; base-uri 'self'; frame-ancestors 'self'"
}

// writeSecurityHeaders sets the HTML content type and the default hardening
// headers (nosniff + the strict CSP for nonce) on an HTML response.
func writeSecurityHeaders(w http.ResponseWriter, nonce string) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", cspPolicy(nonce))
}
