// Package h is the via HTML DSL and renderer.
//
// The tree is built from a single sealed interface, H. Two flavours implement
// it: element/text nodes (rendered in the element body) and attributes
// (rendered inside the opening tag). The Attr marker partitions the two.
//
// Hard guarantees of this package: no user-facing identifier strings beyond the
// tag/attr names the caller passes, no reflection, no any in the element/child
// signatures, no closures required at user call sites. stdlib only.
package h

import (
	"bytes"
	"fmt"
)

// H is the single sealed tree type. The render method is unexported, so only
// types defined in this module can satisfy H — the tree is closed.
type H interface{ render(*Renderer) }

// Attr marks an H that renders inside the opening tag rather than the body.
type Attr interface {
	H
	isAttr()
}

// Binder bridges h into the via package without an import cycle. Dynamic slots
// (signals, actions) claim positional ids and read hydrated values through it
// during a render pass. The via package supplies the implementation; h only
// depends on this interface.
type Binder interface {
	// SignalName allocates the next first-use signal name ("s0","s1",…). A
	// handle calls it once, then caches and reuses the name across renders, so a
	// signal's identity is the handle, not its render position — a signal bound
	// to an input and displayed elsewhere share one name.
	SignalName() string
	// DeclareSignal records that slot participates in this render with the given
	// initial value, for the page-level data-signals declaration. Idempotent
	// within a render.
	DeclareSignal(slot string, initial any)
	// SignalInit returns the hydrated value for a slot, if the request carried one.
	SignalInit(slot string) (any, bool)
	// ActionSlot registers a handler and returns its positional id "0","1",….
	ActionSlot(fn func()) string
}

// Renderer accumulates output bytes and exposes the Binder so dynamic nodes
// (defined in via) can claim slots and read hydrated values.
type Renderer struct {
	buf *bytes.Buffer
	ctx Binder
}

// NewRenderer builds a Renderer bound to b. via injects its *Ctx as the Binder.
func NewRenderer(b Binder) *Renderer {
	return &Renderer{buf: &bytes.Buffer{}, ctx: b}
}

// Binder returns the bound Binder for this render pass.
func (r *Renderer) Binder() Binder { return r.ctx }

// Render renders a single node into the buffer.
func (r *Renderer) Render(node H) { node.render(r) }

// String returns the accumulated output.
func (r *Renderer) String() string { return r.buf.String() }

// Bytes returns the accumulated output without copying.
func (r *Renderer) Bytes() []byte { return r.buf.Bytes() }

// WriteString writes raw bytes. The caller is responsible for pre-escaping.
func (r *Renderer) WriteString(s string) { r.buf.WriteString(s) }

// WriteEscaped HTML-escapes text/attribute values, then writes them.
func (r *Renderer) WriteEscaped(s string) { writeEscaped(r.buf, s) }

// writeEscaped escapes the five HTML-significant characters. It mirrors the
// stdlib html template escaping for text and quoted-attribute contexts: <, >,
// &, ", ' are all neutralised so neither body text nor a double-quoted
// attribute value can break out of its context.
func writeEscaped(buf *bytes.Buffer, s string) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			buf.WriteString("&lt;")
		case '>':
			buf.WriteString("&gt;")
		case '&':
			buf.WriteString("&amp;")
		case '"':
			buf.WriteString("&#34;")
		case '\'':
			buf.WriteString("&#39;")
		case '\r':
			// A bare CR is an SSE line terminator: left raw it would split a
			// datastar-patch-elements frame mid-payload. Neutralise it.
			buf.WriteString("&#13;")
		default:
			buf.WriteByte(s[i])
		}
	}
}

// voidElements are HTML void elements: they self-close and carry no body.
var voidElements = map[string]bool{
	"input": true,
	"br":    true,
	"hr":    true,
	"img":   true,
	"meta":  true,
	"link":  true,
}

// element is the concrete node type for all tags.
type element struct {
	tag  string
	kids []H
}

func (e element) render(r *Renderer) {
	// TODO(static-skeleton): slice 1 walks the tree on every render. A later
	// slice caches the static skeleton and only re-renders dynamic slots.
	r.WriteString("<")
	r.WriteString(e.tag)
	// Attributes render inside the opening tag, in source order.
	for _, k := range e.kids {
		if a, ok := k.(Attr); ok {
			a.render(r)
		}
	}
	if voidElements[e.tag] {
		r.WriteString(">")
		return
	}
	r.WriteString(">")
	// Non-attribute children render in the body, in source order.
	for _, k := range e.kids {
		if _, ok := k.(Attr); !ok {
			k.render(r)
		}
	}
	r.WriteString("</")
	r.WriteString(e.tag)
	r.WriteString(">")
}

// dynNode wraps a render function into a sealed H. It is the bridge the via
// package uses to define dynamic body nodes (signals) that claim positional
// ids from the Binder during the render pass, without breaking the seal or
// exposing render itself. Users never construct one directly.
type dynNode struct{ fn func(*Renderer) }

func (d dynNode) render(r *Renderer) { d.fn(r) }

// Dyn wraps fn as a dynamic body node. via uses this for signal handles that
// must read the Binder at render time. The fn is an internal detail of via,
// never a user-supplied closure at a public call site.
func Dyn(fn func(*Renderer)) H { return dynNode{fn: fn} }

// dynAttr wraps a render function into a sealed Attr.
type dynAttr struct{ fn func(*Renderer) }

func (d dynAttr) render(r *Renderer) { d.fn(r) }
func (d dynAttr) isAttr()            {}

// DynAttr wraps fn as a dynamic attribute. via uses this for event bindings
// (OnClick) that must claim an action id from the Binder at render time.
func DynAttr(fn func(*Renderer)) Attr { return dynAttr{fn: fn} }

// El builds a generic element with the given tag and children.
func El(tag string, kids ...H) H { return element{tag: tag, kids: kids} }

// Div builds a <div>.
func Div(kids ...H) H { return element{tag: "div", kids: kids} }

// Span builds a <span>.
func Span(kids ...H) H { return element{tag: "span", kids: kids} }

// H1 builds an <h1>.
func H1(kids ...H) H { return element{tag: "h1", kids: kids} }

// Button builds a <button>.
func Button(kids ...H) H { return element{tag: "button", kids: kids} }

// Input builds an <input> (a void element).
func Input(kids ...H) H { return element{tag: "input", kids: kids} }

// Body builds a <body>.
func Body(kids ...H) H { return element{tag: "body", kids: kids} }

// P builds a <p>.
func P(kids ...H) H { return element{tag: "p", kids: kids} }

// H2 builds an <h2>.
func H2(kids ...H) H { return element{tag: "h2", kids: kids} }

// Label builds a <label>.
func Label(kids ...H) H { return element{tag: "label", kids: kids} }

// Form builds a <form>.
func Form(kids ...H) H { return element{tag: "form", kids: kids} }

// Ul builds a <ul>.
func Ul(kids ...H) H { return element{tag: "ul", kids: kids} }

// Ol builds an <ol>.
func Ol(kids ...H) H { return element{tag: "ol", kids: kids} }

// Li builds a <li>.
func Li(kids ...H) H { return element{tag: "li", kids: kids} }

// B builds a <b>.
func B(kids ...H) H { return element{tag: "b", kids: kids} }

// Stringish constrains the value types Str accepts, avoiding any.
type Stringish interface {
	~string | ~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

// textNode is an escaped static text node.
type textNode struct{ s string }

func (t textNode) render(r *Renderer) { r.WriteEscaped(t.s) }

// Str builds an escaped static text node from any Stringish value. No any.
func Str[T Stringish](v T) H { return textNode{s: fmt.Sprint(v)} }

// rawAttr is the concrete attribute node. The value is escaped at render time.
type rawAttr struct{ name, val string }

func (a rawAttr) render(r *Renderer) {
	r.WriteString(" ")
	r.WriteString(a.name)
	r.WriteString(`="`)
	r.WriteEscaped(a.val)
	r.WriteString(`"`)
}

func (a rawAttr) isAttr() {}

// validAttrName reports whether name is a safe HTML attribute name: a leading
// ASCII letter, then ASCII letters, digits, or hyphens. Only attribute values
// are escaped at render — an unvalidated name composed from caller data could
// graft a second attribute or close the tag, so the name is allowlisted here.
func validAttrName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// RawAttr builds a name="val" attribute; val is HTML-escaped at render. name
// must match [A-Za-z][A-Za-z0-9-]* — an invalid name panics, since a name is a
// programming-time construction and an injectable one defeats the safe-HTML
// guarantee.
func RawAttr(name, val string) Attr {
	if !validAttrName(name) {
		panic(fmt.Sprintf("h: invalid attribute name %q (must match [A-Za-z][A-Za-z0-9-]*)", name))
	}
	return rawAttr{name: name, val: val}
}

// Data builds a data-<name>="val" attribute; val is HTML-escaped at render. The
// suffix is held to the same allowlist as RawAttr ("data-" is a fixed prefix).
func Data(name, val string) Attr {
	if !validAttrName(name) {
		panic(fmt.Sprintf("h: invalid data-* attribute suffix %q (must match [A-Za-z][A-Za-z0-9-]*)", name))
	}
	return rawAttr{name: "data-" + name, val: val}
}
