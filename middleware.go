package via

import (
	"context"
	"net/http"
)

// Middleware is the request-wrapping function shape used by [App.Use].
// Each middleware receives the next handler in the chain and decides
// whether to invoke it, short-circuit (e.g. with a 401), or wrap the
// response writer before passing through. Registration order is
// outer-first: the first middleware passed to Use runs first per
// request.
//
// Pre-built middleware lives in via/mw — RequestID, AccessLog,
// Recover, CSP, HSTS, RedirectHTTPS.
type Middleware func(w http.ResponseWriter, r *http.Request, next http.Handler)

func applyMiddleware(chain []Middleware, final http.Handler) http.Handler {
	// Wrap from the inside out so chain[0] ends up as the outermost
	// middleware and runs first per request — the canonical Go pattern.
	for i := len(chain) - 1; i >= 0; i-- {
		mw, next := chain[i], final
		final = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mw(w, r, next)
		})
	}
	return final
}

type requestIDKey struct{}

// RequestIDFrom pulls the request id out of r.Context. Returns "" if
// no RequestID middleware (mw.RequestID) has run for this request.
// [Log] uses this to stamp the rid on every record emitted from an
// action / handler whose request is tagged.
func RequestIDFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	v, _ := r.Context().Value(requestIDKey{}).(string)
	return v
}

// RequestWithID returns r with id planted on its context so downstream
// handlers can read it via [RequestIDFrom]. Used by mw.RequestID
// (and by custom RequestID-shaped middleware) to keep the rid lookup
// path consistent across packages.
func RequestWithID(r *http.Request, id string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
}

type routeKey struct{}

// RouteFrom returns the resolved logical route of the composition serving this
// request — the mounted pattern (e.g. "/users/{id}"), the SAME value on the
// page GET, the action POST, and the SSE handshake. Group middleware can use
// it to tell which page it is guarding, since on the action/SSE paths
// r.URL.Path is the shared "/_action/{id}" or "/_sse", not the page route.
// Returns "" outside a via composition request (e.g. a plain HandleFunc route).
func RouteFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	v, _ := r.Context().Value(routeKey{}).(string)
	return v
}

// requestWithRoute plants the resolved route on r's context so group
// middleware on any entry point can read it via [RouteFrom].
func requestWithRoute(r *http.Request, route string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), routeKey{}, route))
}
