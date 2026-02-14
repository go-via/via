package via

import (
	"net/http"
)

// Group represents a route group with a common prefix and middleware.
type Group struct {
	v           *V
	prefix      string
	middlewares []Middleware
}

// Group creates a new route group with the given prefix.
// The callback receives the Group for registering routes.
func (v *V) Group(prefix string, fn func(*Group)) {
	g := &Group{
		v:           v,
		prefix:      prefix,
		middlewares: []Middleware{},
	}
	fn(g)
}

// Use adds middleware to this group only.
func (g *Group) Use(middleware ...Middleware) {
	g.middlewares = append(g.middlewares, middleware...)
}

// Group creates a nested route group within this group.
func (g *Group) Group(prefix string, fn func(*Group)) {
	child := &Group{
		v:           g.v,
		prefix:      g.prefix + prefix,
		middlewares: append([]Middleware{}, g.middlewares...),
	}
	fn(child)
}

// Page registers a page route within the group.
func (g *Group) Page(route string, fn func(*Composition)) {
	fullRoute := g.prefix + route
	c := &Composition{
		id:           genRandID(),
		route:        fullRoute,
		actions:      make(map[string]func(*Context)),
		actionOwners: make(map[string]compOwner),
	}
	fn(c)
	if c.viewFn == nil {
		panic("page " + fullRoute + " has no view")
	}

	g.v.compositionRegistryMutex.Lock()
	g.v.compositionRegistry[c.id] = c
	g.v.compositionRegistryMutex.Unlock()

	// Apply middleware: global outermost, then group (last registered runs closest to handler)
	var handler http.Handler = g.v.newPageHTTPHandler(fullRoute, c.id, c)
	// Apply group middleware first (closer to handler)
	for i := len(g.middlewares) - 1; i >= 0; i-- {
		handler = g.middlewares[i](handler)
	}
	// Then global middleware (outer)
	for i := len(g.v.middlewares) - 1; i >= 0; i-- {
		handler = g.v.middlewares[i](handler)
	}

	g.v.mux.Handle("GET "+fullRoute, handler)
}
