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
// signal/action slots is internal; h exposes only the user vocabulary:
// elements, attributes, and Str.
package h

import (
	"fmt"
	"strings"

	"github.com/go-via/via/internal/hcore"
)

// H is every node of a view: elements, text and attributes alike. It is what
// View returns and what every element constructor accepts and produces, which
// is why a view composes with nothing but function calls:
//
//	h.Div(h.Class("row"), h.Span(h.Str("hi")))
//
// H is sealed. Its only method is unexported, so no package outside via can
// define a new node type: markup reaches the renderer only through the
// constructors in this package, and there is no escape hatch for a raw HTML
// string. If you need markup h has no constructor for, use [El] and [RawAttr];
// both still escape their values.
//
// The zero H is nil, and a nil H renders as nothing. That makes conditional
// children cheap to write, but prefer via.When for a readable one.
type H = hcore.H

// Attr is an H that renders inside the opening tag instead of the element
// body: h.Class, h.Href, h.Data, [RawAttr], via.On and Signal.Bind all return
// one. Because Attr is an H, attributes and children share one variadic
// argument list and may be interleaved in any order; the renderer sorts them.
//
// Attr values are HTML-escaped at render time, and URL-bearing attributes are
// additionally scheme-checked, so a javascript: URL from user data is
// neutralised rather than rendered. Attribute NAMES are not escaped: they are
// validated and an invalid one panics, on the reasoning that a name is written
// by the programmer, never taken from a request.
type Attr = hcore.Attr

// El builds an element with an arbitrary tag, for the handful of tags h has no
// named constructor for (a custom element, an SVG child). The tag is validated
// and an invalid one panics. Prefer the named constructors: they know which
// tags are void and must not emit a closing tag.
func El(tag string, kids ...H) H { return hcore.El(tag, kids...) }

// Stringish is what [Str] accepts: ~string plus every built-in integer and
// float type, including named types whose underlying type is one of those.
// It exists so rendering a number needs no strconv call and no any at the
// call site.
//
// It deliberately does NOT include bool, time.Time, fmt.Stringer or error.
// Rendering those means choosing a format, and h will not choose one for you:
// format the value in Go and pass the string.
type Stringish = hcore.Stringish

// Str is the only way to put text in a view. The value is HTML-escaped at
// render time, so a string taken straight from a request or a database is safe
// here; there is no unescaped counterpart.
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
