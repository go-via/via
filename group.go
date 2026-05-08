package via

import (
	"net/http"
	"strings"
)

// Group bundles routes under a shared path prefix and (optionally) a shared
// middleware chain. Middleware registered with g.Use wraps every handler
// registered via g.HandleFunc / g.Handle / via.MountOn[C](g, ...).
type Group struct {
	app        *App
	prefix     string
	middleware []Middleware
}

// Group creates a new route group under prefix.
func (a *App) Group(prefix string) *Group {
	return &Group{app: a, prefix: prefix}
}

// Use installs middleware that wraps handlers registered through this group.
func (g *Group) Use(mw ...Middleware) {
	g.middleware = append(g.middleware, mw...)
}

// HandleFunc registers a non-via handler under the group prefix, wrapped
// in the group's middleware chain. The pattern follows the same shape as
// http.ServeMux — `"/users"` is GET-only by convention,
// `"POST /users"` registers POST. Without a method token, GET is assumed.
func (g *Group) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	full := groupPattern(g.prefix, pattern)
	g.app.claimRoute(full, "Group("+g.prefix+").HandleFunc")
	g.app.mux.Handle(full,
		applyMiddleware(g.middleware, http.HandlerFunc(handler)))
}

// Handle registers a non-via http.Handler under the group prefix. Same
// pattern shape as HandleFunc.
func (g *Group) Handle(pattern string, handler http.Handler) {
	full := groupPattern(g.prefix, pattern)
	g.app.claimRoute(full, "Group("+g.prefix+").Handle")
	g.app.mux.Handle(full,
		applyMiddleware(g.middleware, handler))
}

// groupPattern joins a group's prefix with a per-handler pattern, keeping
// any leading method token (GET, POST, ...) intact and defaulting to GET
// when the caller didn't specify a method.
func groupPattern(prefix, pattern string) string {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || !isHTTPMethodToken(method) {
		method = "GET"
		path = pattern
	}
	return method + " " + joinPath(prefix, path)
}

// isHTTPMethodToken matches the standard methods Go's http.ServeMux
// recognises as a route prefix. Excludes obscure verbs (TRACE, CONNECT) —
// callers using those must register at the App level directly.
func isHTTPMethodToken(s string) bool {
	switch s {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	}
	return false
}

func joinPath(base, segment string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(segment, "/")
}
