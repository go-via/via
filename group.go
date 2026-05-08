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
// in the group's middleware chain.
func (g *Group) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	g.app.mux.Handle("GET "+joinPath(g.prefix, pattern),
		applyMiddleware(g.middleware, http.HandlerFunc(handler)))
}

// Handle registers a non-via http.Handler under the group prefix.
func (g *Group) Handle(pattern string, handler http.Handler) {
	g.app.mux.Handle("GET "+joinPath(g.prefix, pattern),
		applyMiddleware(g.middleware, handler))
}

func joinPath(base, segment string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(segment, "/")
}
