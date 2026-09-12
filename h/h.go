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

import (
	"fmt"
	"strings"

	"github.com/go-via/via/internal/hcore"
)

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
// must match [A-Za-z][A-Za-z0-9-]*, with ':', '_' and '.' additionally allowed
// in data-* names for Datastar's plugin/modifier syntax. A literal on* name
// (onclick, onerror, ... anything that is not data-on*) is rejected. An
// invalid name panics, since a name is a programming-time construction and an
// injectable one defeats the safe-HTML guarantee.
//
// This only stops a *literal* on* attribute name; it is not a general inline
// event-handler ban. h.Data("attr:onclick", "$x") legitimately re-creates
// onclick client-side through Datastar's attr plugin — that is Datastar's own
// vocabulary, not an injection, and this gate does not and cannot police it.
//
// A URL-bearing name (formaction, action, href, src, xlink:href, poster, the
// <object> data attribute, cite, background, ping, manifest, srcset) is run
// through the same policy as Href/Src/Action, so h.RawAttr("formaction", …)
// cannot smuggle a javascript: scheme past the typed constructors. srcdoc is
// rejected outright: a browser entity-decodes it and parses the result as a
// same-origin document, so single-escaping it is not a safe render.
func RawAttr(name, val string) Attr {
	if strings.EqualFold(name, "srcdoc") {
		panic(fmt.Sprintf("h: %q is not permitted via RawAttr — an inline document can carry same-origin script", name))
	}
	if isURLBearingAttr(name) {
		val = safeURL(val, name)
	}
	return hcore.RawAttr(name, val)
}

// Data builds a data-<name>="val" attribute; val is HTML-escaped at render. It
// is the escape hatch to Datastar's full vocabulary: the suffix may carry the
// plugin syntax, as in h.Data("on:click", "@post(\'/x\')"),
// h.Data("on:keydown__debounce.300ms", ...), h.Data("class:active", "$on"),
// h.Data("attr:disabled", "$busy") or h.Data("show", "$open").
//
// Spell the separator as a colon, never a hyphen: Datastar splits a key on the
// FIRST colon, so data-attr-value names a plugin "attr-value" that does not
// exist and is silently ignored — no console error, no attribute applied.
func Data(name, val string) Attr { return hcore.Data(name, val) }
