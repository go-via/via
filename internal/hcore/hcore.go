// Package hcore is the h DSL's render core. It is internal so the via package
// can reach the renderer/binder plumbing while h's public surface stays
// user-vocabulary only (elements, attributes, Str).
//
// The tree is built from a single sealed interface, H. Two flavours implement
// it: element/text nodes (rendered in the element body) and attributes
// (rendered inside the opening tag). The Attr marker partitions the two.
//
// Hard guarantees of this package: no user-facing identifier strings beyond the
// tag/attr names the caller passes, no reflection, no any in the element/child
// signatures, no closures required at user call sites. stdlib only.
package hcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Miswired is the panic value of a check that fails on how a tree is wired
// (its types, fields and bindings) rather than on its data. via's boot render
// at Mount re-raises only this type, so a View that panics on its zero value
// for any other reason still mounts.
type Miswired string

func (m Miswired) Error() string { return string(m) }

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
	// DeclareSignal records that slot participates in this render with the given
	// initial value, for the page-level data-signals declaration. Idempotent
	// within a render.
	DeclareSignal(slot string, initial any)
	// Hydrator records slot's update function, kept across renders so a live
	// action can update the underlying value in place without a re-render. fn
	// reports whether the value decoded.
	Hydrator(slot string, fn func(json.RawMessage) bool)
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

// Binder returns the Binder that names this render pass's signal and action
// slots; nil outside a via render.
func (r *Renderer) Binder() Binder { return r.ctx }

// Render appends node's markup to the buffer. A nil node renders as nothing.
func (r *Renderer) Render(node H) {
	if node == nil {
		return
	}
	node.render(r)
}

// Bytes returns the accumulated output without copying.
func (r *Renderer) Bytes() []byte { return r.buf.Bytes() }

// WriteString writes raw bytes. The caller is responsible for pre-escaping.
func (r *Renderer) WriteString(s string) { r.buf.WriteString(s) }

// WriteEscaped HTML-escapes text/attribute values, then writes them.
func (r *Renderer) WriteEscaped(s string) { writeEscaped(r.buf, s) }

// writeEscaped escapes the HTML-significant characters. It mirrors the
// stdlib html template escaping for text and quoted-attribute contexts: <, >,
// &, ", ' are all neutralised so neither body text nor a double-quoted
// attribute value can break out of its context. NUL is neutralised too, so
// hostile input can't forge via's internal digest-placeholder token.
func writeEscaped(buf *bytes.Buffer, s string) {
	for i := range len(s) {
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
		case '\x00':
			// HTML5 parsing replaces a literal NUL with U+FFFD; doing it
			// here keeps the escaped bytes and the parsed DOM identical.
			buf.WriteString("&#65533;")
		default:
			buf.WriteByte(s[i])
		}
	}
}

var voidElements = map[string]bool{
	"area": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true,
	"track": true, "wbr": true,
}

type element struct {
	tag  string
	kids []H
}

func (e element) render(r *Renderer) {
	r.WriteString("<")
	r.WriteString(e.tag)
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
	for _, k := range e.kids {
		// A nil child is the documented "renders as nothing" (h.H's godoc), and
		// a helper returning nil on an empty case is the idiom that produces
		// one — rendering it would nil-deref instead.
		if k == nil {
			continue
		}
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

type dynAttr struct{ fn func(*Renderer) }

func (d dynAttr) render(r *Renderer) { d.fn(r) }
func (d dynAttr) isAttr()            {}

// DynAttr wraps fn as a dynamic attribute. via uses this for event bindings
// (On) that must claim an action id from the Binder at render time.
func DynAttr(fn func(*Renderer)) Attr { return dynAttr{fn: fn} }

// El builds a generic element with the given tag and children. tag must match
// [A-Za-z][A-Za-z0-9-]* — an invalid tag panics, since it is a
// programming-time construction and an unvalidated one (e.g. "div
// onclick=alert(1)") would graft a live attribute into the opening tag.
//
// script panics in any case. Datastar v1.0.4 stamps the document nonce on
// every <script> a patch inserts, so one rendered in a live push would run
// even though the same element in the first document is blocked.
func El(tag string, kids ...H) H {
	if !validTagName(tag) {
		panic(fmt.Sprintf("h: invalid tag name %q (must match [A-Za-z][A-Za-z0-9-]*)", tag))
	}
	if strings.EqualFold(tag, "script") {
		panic(Miswired(`h: El("` + tag + `") is refused: Datastar gives a patched-in <script> the page's nonce, ` +
			"so it would run; declare scripts in via.Meta.Assets"))
	}
	return element{tag: tag, kids: kids}
}

// validTagName holds tag to the same strict ASCII allowlist as a non-data
// attribute name: a leading letter, then letters, digits or hyphens. No
// spaces, so a tag string cannot smuggle a second attribute or an inline
// event handler into the opening tag.
func validTagName(tag string) bool {
	if tag == "" {
		return false
	}
	for i := range len(tag) {
		c := tag[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// Stringish constrains the value types Str accepts, avoiding any.
type Stringish interface {
	~string | ~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

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

// validAttrName allowlists attribute names: only values are escaped at render,
// so an unvalidated name composed from caller data could graft a second
// attribute or close the tag. data-* names additionally admit ':', '_' and '.'
// — Datastar's plugin syntax, without which its whole client-side vocabulary
// would be out of reach (see h.Data for the colon-vs-hyphen trap).
//
// Inline DOM event handlers are rejected outright: any on* name that is not
// data-on* executes caller-adjacent strings as script, which is the actual XSS
// vector a name allowlist exists to stop.
func validAttrName(name string) bool {
	if rest, ok := cutDataPrefix(name); ok {
		return validDataSuffix(rest)
	}
	if len(name) >= 2 && (name[0] == 'o' || name[0] == 'O') && (name[1] == 'n' || name[1] == 'N') {
		return false
	}
	if name == "" {
		return false
	}
	for i := range len(name) {
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

func cutDataPrefix(name string) (string, bool) {
	if len(name) < len("data-") || !strings.EqualFold(name[:len("data-")], "data-") {
		return "", false
	}
	return name[len("data-"):], true
}

// validDataSuffix allows Datastar's plugin punctuation after a leading letter.
func validDataSuffix(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '-' || c == ':' || c == '_' || c == '.'):
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
		panic(fmt.Sprintf("h: invalid attribute name %q (must match [A-Za-z][A-Za-z0-9-]*; data-* names may also use : _ . ; inline on* handlers are rejected)", name))
	}
	return rawAttr{name: name, val: val}
}

// Data builds a data-<name>="val" attribute; val is HTML-escaped at render. The
// suffix is held to the same allowlist as RawAttr ("data-" is a fixed prefix).
func Data(name, val string) Attr {
	if !validAttrName("data-" + name) {
		panic(fmt.Sprintf("h: invalid data-* attribute suffix %q (must match [A-Za-z][A-Za-z0-9-]* with : _ . permitted)", name))
	}
	return rawAttr{name: "data-" + name, val: val}
}

// noAttr is an Attr that renders nothing. It exists so a boolean attribute can
// express absence: `disabled="false"` is a *disabled* control in every browser,
// so the off state must emit no attribute at all, and rawAttr always writes
// name="val".
type noAttr struct{}

func (noAttr) render(*Renderer) {}
func (noAttr) isAttr()          {}

// BoolAttr builds a bare HTML boolean attribute — `disabled` when on, nothing
// at all when off. The name is held to the same allowlist as RawAttr.
func BoolAttr(name string, on bool) Attr {
	if !validAttrName(name) {
		panic(fmt.Sprintf("h: invalid attribute name %q (must match [A-Za-z][A-Za-z0-9-]*; data-* names may also use : _ . ; inline on* handlers are rejected)", name))
	}
	if !on {
		return noAttr{}
	}
	return bareAttr{name: name}
}

// bareAttr is a valueless attribute: ` disabled`, not ` disabled=""`.
type bareAttr struct{ name string }

func (a bareAttr) render(r *Renderer) {
	r.WriteString(" ")
	r.WriteString(a.name)
}

func (a bareAttr) isAttr() {}

// NoAttr is an attribute that renders nothing, for a caller that has decided an
// attribute should be absent rather than empty.
func NoAttr() Attr { return noAttr{} }

// EscapeString is writeEscaped as a string function, for the few places that
// build an attribute value by concatenation instead of through a Renderer.
func EscapeString(s string) string {
	var b bytes.Buffer
	writeEscaped(&b, s)
	return b.String()
}
