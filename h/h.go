// Package h is a Go-native DSL for HTML composition.
//
// Every element, attribute, and text node is a value implementing [H],
// the single render-to-writer interface. Trees compose as ordinary Go
// values — `h.Div(h.ID("c"), h.H1(h.T("Counter")))` — and render with
// one Write per node, no per-render escaping, and no template engine.
//
// Design properties:
//   - one heap allocation per element (the element pointer + its
//     variadic children slice fold into one object when the compiler
//     can stack-promote the slice, otherwise two);
//   - attributes are pre-escaped at construction so re-renders write
//     their bytes verbatim;
//   - text nodes carry the HTML-escaped payload directly;
//   - rendering walks each child twice — attributes-pass then
//     content-pass — using a concrete type switch rather than an
//     interface-method indirection.
//
// For fragments that don't depend on per-request state, [Static]
// pre-renders to bytes once and writes verbatim on every Render. For
// dynamic-tag escape hatches use [Tag] / [NewTag]. For shared
// composition use [With] to extend an existing element non-destructively.
//
// Plugin authors emitting attribute-shaped output must use [RawAttr]:
// the attribute marker is unexported on purpose so external packages
// cannot inject raw bytes into the opening-tag region. See the on
// package for the canonical pattern.
package h

import "io"

// H is anything that renders itself to an [io.Writer].
type H interface {
	Render(w io.Writer) error
}

// attribute marks nodes that belong inside the opening tag of their
// parent element. Implementations also satisfy [H]. The marker is an
// unexported method so external packages cannot impersonate an
// attribute and inject raw bytes into the open-tag region.
type attribute interface {
	H
	isAttr()
}
