// Package h provides a Go-native DSL for HTML composition.
// Every element, attribute, and text node is constructed as a function that returns a [h.H] DOM node.
//
// Example:
//
//	h.Div(
//		h.H1(h.Text("Hello, Via")),
//		h.P(h.Text("Pure Go. No templates.")),
//	)
package h

import (
	"io"
	"iter"

	g "maragu.dev/gomponents"
	gc "maragu.dev/gomponents/components"
)

// gNode is a local alias for gomponents.Node so Group can hold them
// without forcing every caller to import gomponents directly.
type gNode = g.Node

// H represents a DOM node.
type H interface {
	Render(w io.Writer) error
}

// Text creates a text DOM node that Renders the escaped string t.
func Text(t string) H {
	return g.Text(t)
}

// Textf creates a text DOM node that Renders the interpolated and escaped string format.
func Textf(format string, a ...any) H {
	return g.Textf(format, a...)
}

// Raw creates a text DOM [Node] that just Renders the unescaped string t.
func Raw(s string) H {
	return g.Raw(s)
}

// Attr creates an attribute DOM [Node] with a name and optional value.
// If only a name is passed, it's a name-only (boolean) attribute (like "required").
// If a name and value are passed, it's a name-value attribute (like `class="header"`).
// More than one value make [Attr] panic.
// Use this if no convenience creator exists in the h package.
func Attr(name string, value ...string) H {
	return g.Attr(name, value...)
}

func If(condition bool, n H) H {
	if condition {
		return n
	}
	return nil
}

// IfElse picks between two pre-built branches. Both are constructed
// eagerly — use [WhenElse] if construction is expensive or has side
// effects.
//
//	h.IfElse(loggedIn,
//	    h.A(h.Href("/profile"), h.Text("Profile")),
//	    h.A(h.Href("/login"),   h.Text("Sign in")),
//	)
func IfElse(condition bool, then, els H) H {
	if condition {
		return then
	}
	return els
}

// When is If with a builder so the node is constructed lazily — useful
// when the construction has side effects or is expensive:
//
//	h.When(loaded, func() h.H { return h.P(h.Text(slow.Render())) })
func When(condition bool, build func() H) H {
	if condition && build != nil {
		return build()
	}
	return nil
}

// WhenElse is IfElse with lazy builders — only the branch that wins
// runs. Either builder may be nil; a nil builder for the winning branch
// renders nothing.
//
//	h.WhenElse(loaded,
//	    func() h.H { return h.P(h.Text(slow.Render())) },
//	    func() h.H { return h.Aside(h.Text("loading…")) },
//	)
func WhenElse(condition bool, then, els func() H) H {
	if condition {
		if then != nil {
			return then()
		}
		return nil
	}
	if els != nil {
		return els()
	}
	return nil
}

// Each renders a list of values into a Group of nodes, one per element.
//
//	h.Ul(h.Each(items, func(it Item) h.H { return h.Li(h.Text(it.Name)) }))
func Each[T any](items []T, fn func(T) H) H {
	if len(items) == 0 {
		return nil
	}
	out := make(group, 0, len(items))
	for _, it := range items {
		if gn, ok := fn(it).(gNode); ok && gn != nil {
			out = append(out, gn)
		}
	}
	return out
}

// EachIndexed is Each with the element's index passed alongside the value.
func EachIndexed[T any](items []T, fn func(i int, v T) H) H {
	if len(items) == 0 {
		return nil
	}
	out := make(group, 0, len(items))
	for i, it := range items {
		if gn, ok := fn(i, it).(gNode); ok && gn != nil {
			out = append(out, gn)
		}
	}
	return out
}

// EachSeq renders one node per value drawn from a Go 1.23 iter.Seq.
// Pairs with stdlib iterators (slices.Values, maps.Values, ...) and any
// custom iterator that yields T values:
//
//	h.Ul(h.EachSeq(maps.Values(byID), func(it Item) h.H {
//	    return h.Li(h.Text(it.Name))
//	}))
//
// Length is unknown up front, so the underlying group grows as needed.
func EachSeq[T any](seq iter.Seq[T], fn func(T) H) H {
	if seq == nil {
		return nil
	}
	var out group
	for v := range seq {
		if gn, ok := fn(v).(gNode); ok && gn != nil {
			out = append(out, gn)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EachSeq2 renders one node per (K, V) pair drawn from a Go 1.23
// iter.Seq2. Pairs with maps.All / slices.All / custom 2-iterators:
//
//	h.Ul(h.EachSeq2(maps.All(byID), func(id string, it Item) h.H {
//	    return h.Li(h.Textf("%s — %s", id, it.Name))
//	}))
func EachSeq2[K, V any](seq iter.Seq2[K, V], fn func(K, V) H) H {
	if seq == nil {
		return nil
	}
	var out group
	for k, v := range seq {
		if gn, ok := fn(k, v).(gNode); ok && gn != nil {
			out = append(out, gn)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SwitchCase pairs a key with the node to render when Switch's value
// matches the key. Build with Case / Default.
type SwitchCase struct {
	key       any // empty for Default
	node      H
	isDefault bool
}

// Case returns a SwitchCase that fires when Switch's value equals key.
func Case(key any, node H) SwitchCase { return SwitchCase{key: key, node: node} }

// Default returns a SwitchCase that fires when no other case matches.
// At most one Default per Switch is honoured (the first one wins).
func Default(node H) SwitchCase { return SwitchCase{node: node, isDefault: true} }

// Switch renders the first matching SwitchCase and nothing else.
// Falls back to Default if no Case matches; renders nothing if no
// Default is provided.
//
//	h.Switch(p.Active.Get(ctx),
//	    h.Case("overview", overviewView(ctx)),
//	    h.Case("settings", settingsView(ctx)),
//	    h.Default(h.P(h.Text("not found"))),
//	)
func Switch(value any, cases ...SwitchCase) H {
	var fallback H
	for _, c := range cases {
		if c.isDefault {
			if fallback == nil {
				fallback = c.node
			}
			continue
		}
		if c.key == value {
			return c.node
		}
	}
	return fallback
}

// retype converts a slice of via's H interface values into the
// gomponents.Node slice that gomponents element constructors expect.
// Empty input returns nil — variadic call sites accept that identically
// to a zero-length slice.
func retype(nodes []H) []g.Node {
	if len(nodes) == 0 {
		return nil
	}
	list := make([]g.Node, len(nodes))
	for i, node := range nodes {
		// (g.Node)(nil) on a nil interface yields (nil, false) — safe to
		// drop the explicit nil guard, the zero slice value covers it.
		list[i], _ = node.(g.Node)
	}
	return list
}

// Fragment bundles many nodes into one H so a function whose signature
// returns a single H can yield several nodes. With a known list,
// pass them directly; with a slice, spread:
//
//	return h.Fragment(h.H2(h.Text(title)), h.Hr())
//	return h.Fragment(items...)
func Fragment(items ...H) H { return group(retype(items)) }

type group []gNode

func (g group) Render(w io.Writer) error {
	for _, n := range g {
		if n == nil {
			continue
		}
		if err := n.Render(w); err != nil {
			return err
		}
	}
	return nil
}

// HTML5Props defines properties for HTML5 pages. Title is always set;
// Description and Language elements are emitted only if the strings are
// non-empty.
type HTML5Props struct {
	Title       string
	Description string
	Language    string
	Head        []H
	Body        []H
	HTMLAttrs   []H
}

// HTML5 document template.
func HTML5(p HTML5Props) H {
	gp := gc.HTML5Props{
		Title:       p.Title,
		Description: p.Description,
		Language:    p.Language,
		Head:        retype(p.Head),
		Body:        retype(p.Body),
		HTMLAttrs:   retype(p.HTMLAttrs),
	}
	gp.Head = append(gp.Head, Script(Type("module"), Src("/_datastar.js")))
	return gc.HTML5(gp)
}
