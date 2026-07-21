// Package h is the via HTML DSL and renderer.
//
// The tree is built from a single sealed interface, H. Two flavours implement
// it: element/text nodes (rendered in the element body) and attributes
// (rendered inside the opening tag). The Attr marker partitions the two.
//
// Hard guarantees of this package: no user-facing identifier strings beyond the
// tag/attr names the caller passes, no reflection, no any in the element/child
// signatures, no closures required at user call sites. stdlib only.
//
// The renderer/binder plumbing that lets the via package drive dynamic
// signal/action slots lives in internal/hcore; h re-exports only the user
// vocabulary — elements, attributes, and Str.
package h

import "github.com/go-via/via/internal/hcore"

// H is the single sealed tree type. The render method is unexported, so only
// types defined in this module can satisfy H — the tree is closed.
type H = hcore.H

// Attr marks an H that renders inside the opening tag rather than the body.
type Attr = hcore.Attr

// El builds a generic element with the given tag and children.
func El(tag string, kids ...H) H { return hcore.El(tag, kids...) }

// Stringish constrains the value types Str accepts, avoiding any.
type Stringish = hcore.Stringish

// Str builds an escaped static text node from any Stringish value. No any.
func Str[T Stringish](v T) H { return hcore.Str(v) }

// RawAttr builds a name="val" attribute; val is HTML-escaped at render. name
// must match [A-Za-z][A-Za-z0-9-]* — an invalid name panics, since a name is a
// programming-time construction and an injectable one defeats the safe-HTML
// guarantee.
func RawAttr(name, val string) Attr { return hcore.RawAttr(name, val) }

// Data builds a data-<name>="val" attribute; val is HTML-escaped at render. The
// suffix is held to the same allowlist as RawAttr ("data-" is a fixed prefix).
func Data(name, val string) Attr { return hcore.Data(name, val) }
