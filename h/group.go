package h

import (
	"bytes"
	"fmt"
	"io"
	"iter"
	"strings"
)

// group is the multi-node container. It implements [H] so it can stand
// in for a single node, and is transparent to [element] (attributes
// inside a group still surface in the parent's open tag).
type group []H

func (g group) Render(w io.Writer) error {
	for _, c := range g {
		if c == nil {
			continue
		}
		// At the top level, attribute fragments would produce invalid
		// HTML if written verbatim into element content. Skip them.
		if _, ok := c.(attribute); ok {
			continue
		}
		if err := c.Render(w); err != nil {
			return err
		}
	}
	return nil
}

// Fragment bundles many nodes into one [H]. Use it when a function
// whose signature returns a single [H] needs to yield several:
//
//	return Fragment(H2(T("title")), Hr())
//	return Fragment(items...)
//
// Attribute arguments (ID, Class, on.*, …) panic at construction: a
// fragment has no opening tag to put them in, so they were previously
// dropped without a trace — a programming mistake better caught where
// the bad call is written.
//
// The returned node aliases items — there is no defensive copy.
// Callers must not mutate items after handing it to Fragment.
func Fragment(items ...H) H {
	if len(items) == 0 {
		return nil
	}
	for i, it := range items {
		a, ok := it.(attribute)
		if !ok {
			continue
		}
		// Render the offending attribute into the panic so the message
		// names it — the argument index alone is weak triage for
		// Fragment(items...) call sites.
		var buf bytes.Buffer
		_ = a.Render(&buf)
		panic(fmt.Sprintf(
			"h.Fragment: argument %d is an attribute (%s) — a fragment has "+
				"no opening tag to receive it, so it would be silently "+
				"dropped; attach the attribute to the enclosing element "+
				"instead", i+1, strings.TrimSpace(buf.String())))
	}
	return group(items)
}

// If returns n when condition is true, otherwise nil — which renders as
// nothing. Both branches are evaluated eagerly; use [When] if
// constructing n is expensive or has side effects you only want when
// condition holds.
func If(condition bool, n H) H {
	if condition {
		return n
	}
	return nil
}

// IfElse picks between two pre-built branches. Both are constructed
// eagerly — use [WhenElse] if construction is expensive.
func IfElse(condition bool, then, els H) H {
	if condition {
		return then
	}
	return els
}

// When is [If] with a builder so the node is constructed lazily.
func When(condition bool, build func() H) H {
	if condition && build != nil {
		return build()
	}
	return nil
}

// WhenElse is [IfElse] with lazy builders — only the winning branch
// runs. Either builder may be nil; a nil builder for the winning branch
// renders nothing.
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

// Maybe renders fn(v) only when v differs from its zero value, so
// optional fields and pointer-style data render cleanly without an
// explicit guard at every call site. T must be [comparable] because
// the zero check is `v == zero` — uncomparable types (slices, maps,
// funcs) fail at compile time rather than via a generics error at
// instantiation.
//
//	h.Maybe(user.Email, func(s string) h.H {
//	    return h.P("email: ", s)
//	})
func Maybe[T comparable](v T, fn func(T) H) H {
	var zero T
	if v == zero || fn == nil {
		return nil
	}
	return fn(v)
}

// Each renders one node per element of items.
func Each[T any](items []T, fn func(T) H) H {
	if len(items) == 0 {
		return nil
	}
	out := make(group, 0, len(items))
	for _, it := range items {
		if n := fn(it); n != nil {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EachIndexed is [Each] with the element index passed alongside the
// value.
func EachIndexed[T any](items []T, fn func(i int, v T) H) H {
	if len(items) == 0 {
		return nil
	}
	out := make(group, 0, len(items))
	for i, it := range items {
		if n := fn(i, it); n != nil {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EachSeq renders one node per value drawn from a Go 1.23 [iter.Seq].
func EachSeq[T any](seq iter.Seq[T], fn func(T) H) H {
	if seq == nil {
		return nil
	}
	var out group
	for v := range seq {
		if n := fn(v); n != nil {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EachSeq2 renders one node per (K, V) pair drawn from a Go 1.23
// [iter.Seq2].
func EachSeq2[K, V any](seq iter.Seq2[K, V], fn func(K, V) H) H {
	if seq == nil {
		return nil
	}
	var out group
	for k, v := range seq {
		if n := fn(k, v); n != nil {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SwitchCase pairs a key with the node to render when [Switch]'s value
// matches the key. Build with [Case] / [Default].
type SwitchCase[K comparable] struct {
	key       K
	node      H
	isDefault bool
}

// Case returns a [SwitchCase] that fires when [Switch]'s value equals
// key.
func Case[K comparable](key K, node H) SwitchCase[K] {
	return SwitchCase[K]{key: key, node: node}
}

// Default returns a [SwitchCase] that fires when no other case matches.
// At most one Default per Switch is honoured (the first one wins).
//
// K cannot be inferred from the argument, so it is spelled explicitly
// at the call site: h.Default[Status](unknownView).
func Default[K comparable](node H) SwitchCase[K] {
	return SwitchCase[K]{node: node, isDefault: true}
}

// Switch renders the first matching [SwitchCase] and nothing else.
//
// value and every [Case] key share the comparable type K, so a
// mismatched case is a compile error. Note Go's comparable admits
// interface and comparable-struct types whose comparison can still
// panic at runtime if a value carries a non-comparable dynamic type —
// for tab-style branching on such a value, project it to a simple key
// first (e.g. a tag string or enum) and Switch on that.
func Switch[K comparable](value K, cases ...SwitchCase[K]) H {
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

// staticNode is an [H] whose render output is the captured byte slice
// — built once, written verbatim forever.
type staticNode struct{ b []byte }

func (s *staticNode) Render(w io.Writer) error { _, err := w.Write(s.b); return err }

// Static pre-renders n into a byte slice and returns an [H] that writes
// those bytes on every Render. Use it for fragments that don't depend
// on per-request state — site headers, navigation, layout chrome — so
// they stop allocating across reloads.
//
// Capturing a subtree that embeds a [RawAttr] (or any other node
// derived from per-request data) is almost certainly a bug: the bytes
// are frozen at construction and will keep emitting the original
// values regardless of later state. Reserve Static for truly static
// content built at package-init time.
//
// Panics if n.Render returns an error during pre-render; a Static
// node is built at package-init time where the only realistic failure
// is a misconfigured writer.
func Static(n H) H {
	if n == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := n.Render(&buf); err != nil {
		panic("h.Static: " + err.Error())
	}
	return &staticNode{b: buf.Bytes()}
}

// With returns a copy of base extended with additional children. It
// makes component composition compose without forcing every component
// signature to take a variadic — e.g. a Card(body) constructor can
// still gain an extra class or click handler at the call site:
//
//	h.With(Card(myBody), on.Click(open))
//
// When base is not an *element (text, group, attribute, raw
// fragment, …) With falls back to a plain group so the result still
// renders the base followed by more. In that fallback path, attribute
// children bubble to the wrapping element via group semantics (the
// renderer skips attributes at the group top level and the parent
// element consumes them); they do not attach to base itself.
func With(base H, more ...H) H {
	if len(more) == 0 {
		return base
	}
	if e, ok := base.(*element); ok && e != nil {
		// Copy-on-write so we never mutate a tree the caller might
		// hold onto. The combined slice is reallocated at the new
		// length so subsequent appends don't reach back into the
		// source element's backing array.
		merged := make([]H, len(e.children)+len(more))
		copy(merged, e.children)
		copy(merged[len(e.children):], more)
		return &element{tag: e.tag, children: merged, void: e.void}
	}
	out := make([]H, 0, 1+len(more))
	if base != nil {
		out = append(out, base)
	}
	out = append(out, more...)
	return group(out)
}
