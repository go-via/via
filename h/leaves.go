package h

import (
	"fmt"
	"io"
)

// textNode carries an HTML-escaped element-content payload as a string.
// The pointer receiver lets [H] interface storage skip the
// non-pointer-into-interface boxing alloc that would hit if we used
// `type text []byte`.
type textNode struct{ s string }

func (t *textNode) Render(w io.Writer) error {
	if sw, ok := w.(stringWriter); ok {
		_, err := sw.WriteString(t.s)
		return err
	}
	_, err := w.Write([]byte(t.s))
	return err
}

// rawNode carries trusted (already-safe) element-content markup.
type rawNode struct{ s string }

func (r *rawNode) Render(w io.Writer) error {
	if sw, ok := w.(stringWriter); ok {
		_, err := sw.WriteString(r.s)
		return err
	}
	_, err := w.Write([]byte(r.s))
	return err
}

// Text creates an HTML-escaped text node. When the input contains no
// characters that need escaping, the node carries the input string
// verbatim — no byte copy.
func Text(s string) H {
	if s == "" {
		return nil
	}
	if needsEscape(s) < 0 {
		return &textNode{s: s}
	}
	return &textNode{s: htmlEscape(s)}
}

// T is a brevity alias for [Text]. The short name matters at call
// sites where many small text nodes nest inside one-letter element
// constructors — `h.H1(h.T("Counter"))` reads cleaner than
// `h.H1(h.Text("Counter"))` without sacrificing static typing.
func T(s string) H { return Text(s) }

// Textf formats and escapes once at construction.
func Textf(format string, a ...any) H {
	return Text(fmt.Sprintf(format, a...))
}

// Raw emits s verbatim — the caller is responsible for HTML safety.
func Raw(s string) H {
	if s == "" {
		return nil
	}
	return &rawNode{s: s}
}

// RawAttr is a pre-rendered attribute fragment (leading space, name,
// optional `="escaped-value"`). It implements [H] as an attribute so
// the element renderer emits it inside the opening tag, and writes its
// bytes verbatim on every Render — no per-render escape.
//
// The bytes are owned by the caller; do not mutate them after passing
// the value to [H] consumers.
type RawAttr []byte

func (a RawAttr) Render(w io.Writer) error { _, err := w.Write(a); return err }
func (RawAttr) isAttr()                    {}
