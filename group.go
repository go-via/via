package via

import (
	"net/http"
	"strings"
)

type Group struct {
	app       *App
	prefix    string
	middleware []Middleware
}

func (a *App) Group(prefix string) *Group {
	return &Group{app: a, prefix: prefix}
}

func (g *Group) Use(mw ...Middleware) {
	g.middleware = append(g.middleware, mw...)
}

func (g *Group) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	g.app.mux.HandleFunc("GET "+joinPath(g.prefix, pattern), handler)
}

func joinPath(base, segment string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(segment, "/")
}
