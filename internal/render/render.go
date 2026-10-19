// Package render is the render core shared by h, on and via. It is internal
// so those packages can reach the renderer/binder plumbing while their public
// surface stays user vocabulary only (elements, attributes, events, Str).
//
// The tree is built from a single sealed interface, H. Two flavours implement
// it: element/text nodes (rendered in the element body) and attributes
// (rendered inside the opening tag). The Attr marker partitions the two.
//
// Hard guarantees of this package: no user-facing identifier strings beyond the
// tag/attr names the caller passes, no reflection, no any in the element/child
// signatures, no closures required at user call sites. stdlib only.
package render

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

// Binder is the via Ctx a render pass runs under. render sits below via, so a
// Renderer can hold the Ctx only as this interface. A rendered signal declares
// its slot and registers its hydrator through it, and package on binds its
// actions through it. via names the slots itself (a
// signal's field path, an action's handler-name hash), so the Binder allocates
// nothing. The via package supplies the implementation; h and on depend only
// on this interface.
type Binder interface {
	// DeclareSignal records that slot participates in this render with the given
	// initial value, for the page-level data-signals declaration. Idempotent
	// within a render.
	DeclareSignal(slot string, initial any)
	// Hydrator records slot's updater, kept across renders so a live action
	// can update the underlying value in place without a re-render.
	Hydrator(slot string, h Hydrator)
	// Action binds fn, a func(*via.Ctx), to event on the element r is writing.
	Action(r *Renderer, event string, fn any)
	// ArgAction binds fn, a func(*via.Ctx, T), with arg to event. call
	// decodes a dispatched arg and calls fn with it.
	ArgAction(r *Renderer, event string, fn, arg any, call ArgCall)
}

// Hydrator writes a posted signal value into the handle it was bound from.
type Hydrator interface {
	// Hydrate reports whether raw decoded; a value that did not is not written.
	Hydrate(raw json.RawMessage) bool
}

// ArgCall is on.Bind's typed half of an arg action: via checks the raw arg
// against what the render bound between Decode and Call.
type ArgCall interface {
	// Decode turns a dispatched arg into the handler's argument type.
	Decode(raw []byte) (any, error)
	// Call runs the handler on ctx, a *via.Ctx, with a value Decode returned.
	Call(ctx, v any)
}

// Renderer accumulates output bytes and exposes the Binder so dynamic nodes
// (defined in via) can reach the render pass's Ctx.
type Renderer struct {
	buf *bytes.Buffer
	ctx Binder
}

// NewRenderer builds a Renderer bound to b. via injects its *Ctx as the Binder.
func NewRenderer(b Binder) *Renderer {
	return &Renderer{buf: &bytes.Buffer{}, ctx: b}
}

// Binder returns the Binder this render pass was built with; nil outside a via
// render.
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
// attribute value can break out of its context.
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
			// A raw NUL never reaches the wire: body text would drop it
			// silently, so it renders as a visible U+FFFD instead.
			buf.WriteString("&#65533;")
		default:
			buf.WriteByte(s[i])
		}
	}
}

var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true,
	"track": true, "wbr": true,
}

var newlineEaters = map[string]bool{"pre": true, "textarea": true, "listing": true}

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
	if voidElements[strings.ToLower(e.tag)] {
		r.WriteString(">")
		return
	}
	r.WriteString(">")
	body := r.buf.Len()
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
	if newlineEaters[strings.ToLower(e.tag)] && r.buf.Len() > body && r.buf.Bytes()[body] == '\n' {
		// The HTML parser drops one newline right after these opening tags,
		// so a leading newline of the content needs a second one to survive.
		tail := bytes.Clone(r.buf.Bytes()[body:])
		r.buf.Truncate(body)
		r.buf.WriteByte('\n')
		r.buf.Write(tail)
	}
	r.WriteString("</")
	r.WriteString(e.tag)
	r.WriteString(">")
}

// Dynamic is markup that reads the Binder during the render pass: signals,
// children, bound actions. via and on implement it on unexported types and
// wrap them with Dyn or DynAttr, which keeps H sealed and render unexported.
type Dynamic interface{ RenderTo(*Renderer) }

type dynNode struct{ d Dynamic }

func (n dynNode) render(r *Renderer) { n.d.RenderTo(r) }

// Dyn wraps d as a dynamic body node.
func Dyn(d Dynamic) H { return dynNode{d: d} }

type dynAttr struct{ d Dynamic }

func (a dynAttr) render(r *Renderer) { a.d.RenderTo(r) }
func (a dynAttr) isAttr()            {}

// DynAttr wraps d as a dynamic attribute: event bindings (package on) and
// Signal.Bind, which register with the render pass's Ctx.
func DynAttr(d Dynamic) Attr { return dynAttr{d: d} }

// El builds a generic element with the given tag and children. tag must match
// [A-Za-z][A-Za-z0-9-]* — an invalid tag panics, since it is a
// programming-time construction and an unvalidated one (e.g. "div
// onclick=alert(1)") would graft a live attribute into the opening tag.
//
// script panics in any case. Datastar v1.0.4 stamps the document nonce on
// every <script> a patch inserts, so one rendered in a live push would run
// even though the same element in the first document is blocked.
func El(tag string, kids ...H) H {
	if !plainName(tag) {
		panic(fmt.Sprintf("h: invalid tag name %q (must match [A-Za-z][A-Za-z0-9-]*)", tag))
	}
	if strings.EqualFold(tag, "script") {
		panic(Miswired(`h: El("` + tag + `") is refused: Datastar gives a patched-in <script> the page's nonce, ` +
			"so it would run; declare scripts in via.Meta.Assets"))
	}
	return element{tag: tag, kids: kids}
}

// plainName is the strict ASCII allowlist for a tag and a non-data attribute
// name: a leading letter, then letters, digits or hyphens. No spaces, so a
// name cannot smuggle a second attribute or an inline event handler into the
// opening tag.
func plainName(name string) bool {
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
	return plainName(name)
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
	mustAttrName(name)
	return rawAttr{name: name, val: val}
}

func mustAttrName(name string) {
	if !validAttrName(name) {
		panic(fmt.Sprintf("h: invalid attribute name %q (must match [A-Za-z][A-Za-z0-9-]*; data-* names may also use : _ . ; inline on* handlers are rejected)", name))
	}
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
	mustAttrName(name)
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
