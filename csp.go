package via

import (
	"context"
	"net/http"
	"strings"
)

// CSPNonce returns a per-request cryptographically-random base64
// nonce suitable for use with strict Content-Security-Policy headers.
// The same value is returned on every call within one request, so
// plugins and the page render share one nonce.
//
// For strict CSP enforcement, install StrictCSP — or write your own
// middleware that pre-generates the nonce, sets the Content-Security-
// Policy header, and threads it through the request via
// RequestWithCSPNonce. Without that, CSPNonce returns a random
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
// downstream renderPage can find it via Ctx.CSPNonce(). Use it from a
// custom CSP middleware that wants to keep the rendered HTML's nonce
// in lock-step with whatever value it puts in the response header.
func RequestWithCSPNonce(r *http.Request, nonce string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), cspNonceKey{}, nonce))
}

// StrictCSP returns a Middleware that generates a fresh nonce per
// request, sets a strict Content-Security-Policy header that allows
// only same-origin scripts plus that nonce, and threads the nonce
// through r.Context so Ctx.CSPNonce returns the matching value.
//
// Wire it up as the very first middleware so the header lands on
// every response, including 404s and SSE handshakes:
//
//	app.Use(via.StrictCSP())
//
// Pass extra directives if defaults aren't enough:
//
//	app.Use(via.StrictCSP("img-src 'self' data:"))
//
// The default policy is `default-src 'self'; script-src 'self'
// 'nonce-XYZ'; object-src 'none'; base-uri 'self'`.
func StrictCSP(extra ...string) Middleware {
	tail := ""
	if len(extra) > 0 {
		tail = "; " + strings.Join(extra, "; ")
	}
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		n := genCSPNonce()
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'nonce-"+n+"'; "+
				"object-src 'none'; base-uri 'self'"+tail)
		next.ServeHTTP(w, RequestWithCSPNonce(r, n))
	}
}
