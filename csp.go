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

type cspNonceKey struct{}

// RequestWithCSPNonce returns r with nonce stored in its context so
// downstream renderPage can find it via [Ctx.CSPNonce]. Use it from
// a custom CSP middleware (or mw.CSP) so the rendered HTML's nonce
// stays in lock-step with whatever value the middleware puts in the
// response header.
func RequestWithCSPNonce(r *http.Request, nonce string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), cspNonceKey{}, nonce))
}
