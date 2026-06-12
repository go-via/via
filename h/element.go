package h

import "io"

// element is the structural node. Tag is a literal lowercase string
// stored as the tag bytes (no per-render conversion). children may
// contain attributes interspersed with element content; Render emits
// every attribute first, then every non-attribute child.
type element struct {
	tag      string
	children []H
	void     bool
}

// stringWriter lets Render skip the io.Writer→[]byte conversion when
// the underlying writer is e.g. *bytes.Buffer / *strings.Builder. The
// type assertion is performed once per element Render and cached for
// every Write within it.
type stringWriter interface {
	io.Writer
	WriteString(s string) (int, error)
}

func writeString(w io.Writer, sw stringWriter, s string) error {
	if sw != nil {
		_, err := sw.WriteString(s)
		return err
	}
	_, err := w.Write([]byte(s))
	return err
}

func (e *element) Render(w io.Writer) error {
	sw, _ := w.(stringWriter)
	if err := writeString(w, sw, "<"); err != nil {
		return err
	}
	if err := writeString(w, sw, e.tag); err != nil {
		return err
	}
	if err := renderAttrs(w, e.children); err != nil {
		return err
	}
	if err := writeString(w, sw, ">"); err != nil {
		return err
	}
	if e.void {
		return nil
	}
	if err := renderContent(w, e.children); err != nil {
		return err
	}
	if err := writeString(w, sw, "</"); err != nil {
		return err
	}
	if err := writeString(w, sw, e.tag); err != nil {
		return err
	}
	return writeString(w, sw, ">")
}

// renderAttrs walks the children once and emits every attribute node.
// Groups are descended recursively so attributes nested inside Each /
// With-built groups still surface in the opening tag (Fragment rejects
// attribute arguments at construction).
func renderAttrs(w io.Writer, children []H) error {
	for _, c := range children {
		if c == nil {
			continue
		}
		switch v := c.(type) {
		case attribute:
			if err := v.Render(w); err != nil {
				return err
			}
		case group:
			if err := renderAttrs(w, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderContent walks the children once and emits every non-attribute
// node. Attributes encountered are skipped (already emitted in
// renderAttrs).
func renderContent(w io.Writer, children []H) error {
	for _, c := range children {
		if c == nil {
			continue
		}
		switch v := c.(type) {
		case attribute:
		case group:
			if err := renderContent(w, v); err != nil {
				return err
			}
		default:
			if err := v.Render(w); err != nil {
				return err
			}
		}
	}
	return nil
}

// el builds a non-void element with the given lowercase tag.
func el(tag string, children []H) H {
	return &element{tag: tag, children: children}
}

// elVoid builds a void element (no closing tag, content children
// ignored at render time).
func elVoid(tag string, children []H) H {
	return &element{tag: tag, children: children, void: true}
}
