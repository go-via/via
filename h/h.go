// Package h is the via HTML DSL: markup as ordinary Go function calls.
//
// # Three kinds of node
//
// Everything is an [H], a sealed interface, and a view is nothing but nested
// calls:
//
//		h.Div(h.Class("row"), h.Href("/x"), h.Span(h.Str("hi")))
//
//	  - Elements are one exported function per HTML5 tag, named after the tag
//	    with the first letter capitalised: h.Div, h.Ul, h.Textarea, h.Blockquote.
//	    They take children variadically and return H. The table lives in
//	    elements.go. A handful of tags are deliberately absent — html, head,
//	    body, script, style, title, base, meta, link, template, slot, data —
//	    because via owns the document shell and the CSP'd asset tags; declare
//	    those through via.Head / via.Meta instead. Anything else exotic goes
//	    through [El].
//	  - Attributes are one exported function per attribute, named the same way:
//	    h.Class, h.Name, h.Type, h.Value, h.Placeholder, h.Href, h.Disabled. The
//	    table lives in attrs.go (URL-valued ones in url.go). They return [Attr],
//	    which is itself an H, so attributes and children share one argument list
//	    and may be interleaved in any order — the renderer sorts them into the
//	    opening tag. Boolean attributes (h.Disabled, h.Required, h.Checked) take
//	    a bool and render as a bare name when true and as nothing when false,
//	    because disabled="false" still disables a control. [RawAttr] spells an
//	    attribute h has no helper for.
//	  - Text is [Str], which accepts a string or any built-in numeric type, so
//	    h.Str(count) needs no strconv call.
//
// A nil H renders as nothing, so a conditional child costs nothing; prefer
// via.When and via.Each for a readable one.
//
// # Escaping
//
// Text and attribute values are HTML-escaped at render time, always, with no
// opt-out: there is no raw-HTML constructor anywhere in this package, and H is
// sealed so no other package can add one. URL-bearing attributes (h.Href,
// h.Src, h.Action, and a via Redirect target) are additionally scheme-checked,
// so a javascript: or data: URL arriving from user data is dropped rather than
// rendered. Attribute names are not escaped — they are validated against an
// allowlist and an invalid one panics, on the reasoning that a name is written
// by the programmer and never taken from a request.
//
// # The Datastar escape hatch
//
// via generates the data-* attributes that make a page live (package on,
// Signal.Bind, State.Display). The DataShow/DataClass/DataOn family spells the
// rest of Datastar's vocabulary:
//
//	h.Div(h.DataShow(p.Open.Ref()), ...)   // renders data-show="$open"
//
// [Data] covers anything they miss. It validates the key the same
// way RawAttr does and escapes the expression.
// Datastar splits a key on ":", so data-attr-value must be written as
// h.Data("attr:value", …) — and no Go test can catch the difference.
//
// # Guarantees
//
// No user-facing identifier strings beyond the tag/attr names the caller
// passes, no reflection, no any in the element/child signatures, no closures
// required at a call site, stdlib only. The renderer/binder plumbing via uses
// to drive dynamic signal and action slots is internal; h exposes only the
// vocabulary: elements, attributes, and Str.
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
// body: h.Class, h.Href, h.Data, [RawAttr], on.Click and Signal.Bind all return
// one. Because Attr is an H, attributes and children share one variadic
// argument list and may be interleaved in any order; the renderer sorts them.
//
// Attr values are HTML-escaped at render time, and URL-bearing attributes are
// additionally scheme-checked, so a javascript: URL from user data is
// neutralised rather than rendered. Attribute names are not escaped: they are
// validated and an invalid one panics, on the reasoning that a name is written
// by the programmer, never taken from a request.
type Attr = hcore.Attr

// El builds an element with an arbitrary tag, for the handful of tags h has no
// named constructor for (a custom element, an SVG child). The tag is validated
// and an invalid one panics, as does script in any case: Datastar gives a
// <script> that arrives in a live patch the page's nonce, so it would run.
// Declare scripts in via.Meta.Assets. Prefer the named constructors: they
// know which tags are void and must not emit a closing tag.
func El(tag string, kids ...H) H { return hcore.El(tag, kids...) }

// Stringish is what [Str] accepts: ~string plus every built-in integer and
// float type, including named types whose underlying type is one of those.
// It exists so rendering a number needs no strconv call and no any at the
// call site.
//
// It deliberately does not include bool, time.Time, fmt.Stringer or error.
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
// through the same policy as Href/Src/Action (mailto: and tel: pass on href
// only), so h.RawAttr("formaction", …) cannot smuggle a javascript: scheme
// past the typed constructors. srcdoc is
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
// first colon, so data-attr-value names a plugin "attr-value" that does not
// exist and is silently ignored — no console error, no attribute applied.
func Data(name, val string) Attr { return hcore.Data(name, val) }
