package via

import (
	"context"
	"net/http"
)

// CSPNonce returns a per-request cryptographically-random base64
// nonce suitable for use with strict Content-Security-Policy headers.
// The same value is returned on every call within one request, so
// plugins and the page render share one nonce.
//
// For strict CSP enforcement, install mw.CSP — or write your own
// middleware that pre-generates the nonce, sets the Content-Security-
// Policy header, and threads it through the request via
// [RequestWithCSPNonce]. Without that, CSPNonce returns a random
// per-request value the browser will not honor.
func (ctx *Ctx) CSPNonce() string {
	if ctx == nil {
		return ""
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.cspNonce != "" {
		return ctx.cspNonce
	}
	if ctx.r != nil {
		if v, ok := ctx.r.Context().Value(cspNonceKey{}).(string); ok && v != "" {
			ctx.cspNonce = v
			return v
		}
	}
	ctx.cspNonce = genCSPNonce()
	return ctx.cspNonce
}

// captureCSPNonce records the request's strict-CSP nonce on ctx so the
// SSE push path can nonce server-injected <script> elements with the same
// value the page document was served under. It is read-only — it never
// generates a nonce — and writes a dedicated field (not the lazy CSPNonce
// cache), so a page served without a CSP middleware leaves docNonce empty
// even if a View calls CSPNonce(), and pushed scripts stay attribute-free.
// Called once at page render; the first non-empty value wins, so a later
// action/SSE request's (distinct, per-request) nonce can't clobber it.
func (ctx *Ctx) captureCSPNonce(r *http.Request) {
	if ctx == nil || r == nil {
		return
	}
	v, ok := r.Context().Value(cspNonceKey{}).(string)
	if !ok || v == "" {
		return
	}
	ctx.mu.Lock()
	if ctx.docNonce == "" {
		ctx.docNonce = v
	}
	ctx.mu.Unlock()
}

// documentCSPNonce returns the nonce the page document was served under, or
// "" if no CSP middleware threaded one. Unlike CSPNonce it never generates
// a value — the push path must use the document's real nonce or nothing,
// never a freshly minted one the browser's policy wouldn't recognise.
func (ctx *Ctx) documentCSPNonce() string {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.docNonce
}

type cspNonceKey struct{}

// RequestWithCSPNonce returns r with nonce stored in its context so
// downstream renderPage can find it via [Ctx.CSPNonce]. Use it from
// a custom CSP middleware (or mw.CSP) so the rendered HTML's nonce
// stays in lock-step with whatever value the middleware puts in the
// response header.
func RequestWithCSPNonce(r *http.Request, nonce string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), cspNonceKey{}, nonce))
}
