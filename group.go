package via

import "strings"

// Group is a route group with scoped middleware and an optional layout.
type Group struct {
	app        *App
	prefix     string
	middleware []Middleware
	layoutFn   func(cmp *Cmp)
	hasLayout  bool
	parent     *Group
}

// Group creates a route group under prefix. Middleware and layout registered
// on the group apply to all pages within it. Use an empty prefix to scope
// middleware and layout without changing the route path.
func (a *App) Group(prefix string) *Group {
	return &Group{app: a, prefix: prefix}
}

// Group creates a nested route group.
func (g *Group) Group(prefix string) *Group {
	return &Group{app: g.app, prefix: joinPath(g.prefix, prefix), parent: g}
}

// Use registers middleware scoped to this group and its children.
func (g *Group) Use(mw ...Middleware) {
	g.middleware = append(g.middleware, mw...)
}

// Layout sets the layout for pages in this group. Replaces the parent layout.
// Pass nil to remove the layout for this group.
func (g *Group) Layout(layoutFn func(cmp *Cmp)) {
	g.layoutFn = layoutFn
	g.hasLayout = true
}

// Page registers a page route within this group.
func (g *Group) Page(route string, initCmpFn func(cmp *Cmp)) {
	chain := g.collectMiddleware()
	layoutFn := g.resolveLayout()
	g.app.pageWithOptions(joinPath(g.prefix, route), initCmpFn, chain, layoutFn)
}

func (g *Group) resolveLayout() func(cmp *Cmp) {
	for curr := g; curr != nil; curr = curr.parent {
		if curr.hasLayout {
			return curr.layoutFn
		}
	}
	return g.app.layoutFn
}

func (g *Group) collectMiddleware() []Middleware {
	var ancestors []*Group
	for curr := g; curr != nil; curr = curr.parent {
		ancestors = append(ancestors, curr)
	}
	var chain []Middleware
	for i := len(ancestors) - 1; i >= 0; i-- {
		chain = append(chain, ancestors[i].middleware...)
	}
	return chain
}

// joinPath concatenates two route segments, collapsing double slashes.
func joinPath(base, segment string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(segment, "/")
}
