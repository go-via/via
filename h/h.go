// Package h provides a Go-native DSL for HTML composition.
// Every element, attribute, and text node is constructed as a function that returns a [h.H] DOM node.
//
// Example:
//
//	h.Div(
//		h.H1(h.Text("Hello, Via")),
//		h.P(h.Text("Pure Go. No tmplates.")),
//	)
package h

import (
	"io"

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

// Rawf creates a text DOM [Node] that just Renders the interpolated and
// unescaped string format.
func Rawf(format string, a ...any) H {
	return g.Rawf(format, a...)
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

// Each renders a list of values into a Group of nodes, one per element.
//
//	h.Ul(h.Each(items, func(it Item) h.H { return h.Li(h.Text(it.Name)) }))
func Each[T any](items []T, fn func(T) H) H {
	if len(items) == 0 {
		return nil
	}
	out := make([]H, 0, len(items))
	for _, it := range items {
		if n := fn(it); n != nil {
			out = append(out, n)
		}
	}
	return Group(out)
}

// EachIndexed is Each with the element's index passed alongside the value.
func EachIndexed[T any](items []T, fn func(i int, v T) H) H {
	if len(items) == 0 {
		return nil
	}
	out := make([]H, 0, len(items))
	for i, it := range items {
		if n := fn(i, it); n != nil {
			out = append(out, n)
		}
	}
	return Group(out)
}

// Group bundles a slice of nodes into a single H so callers can return
// many nodes from a function that has to return one.
func Group(items []H) H {
	gnodes := retype(items)
	out := make(group, len(gnodes))
	for i, n := range gnodes {
		out[i] = n
	}
	return out
}

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

// HTML5Props defines properties for HTML5 pages. Title is set always set, Description
// and Language elements only if the strings are non-empty.
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

// JoinAttrs with the given name only on the first level of the given nodes. This means that
// attributes on non-direct descendants are ignored. Attribute values are joined by spaces.
//
// Note that this renders all first-level attributes to check whether they should be processed.
func JoinAttrs(name string, children ...H) H {
	return gc.JoinAttrs(name, retype(children)...)
}
