package h

import "io"

// HTML5Props defines properties for HTML5 pages. Title is always set;
// Description and Language are emitted only when their strings are
// non-empty.
type HTML5Props struct {
	Title       string
	Description string
	Language    string
	Head        []H
	Body        []H
	HTMLAttrs   []H
}

// doctype is a stateless sentinel that prefixes its sibling with the
// HTML5 doctype declaration.
type doctype struct{ sibling H }

var doctypeBytes = []byte("<!doctype html>")

func (d doctype) Render(w io.Writer) error {
	if _, err := w.Write(doctypeBytes); err != nil {
		return err
	}
	return d.sibling.Render(w)
}

// HTML5 returns a fully formed HTML5 document. The injected datastar
// script tag matches the [h.HTML5] surface so the runtime can serve
// the same fragment regardless of which package built it.
func HTML5(p HTML5Props) H {
	head := make([]H, 0, 5+len(p.Head))
	head = append(head, Meta(Charset("utf-8")))
	head = append(head, Meta(Name("viewport"), Content("width=device-width, initial-scale=1")))
	head = append(head, Title(p.Title))
	if p.Description != "" {
		head = append(head, Meta(Name("description"), Content(p.Description)))
	}
	for _, n := range p.Head {
		if n != nil {
			head = append(head, n)
		}
	}
	head = append(head, Script(Type("module"), Src("/_datastar.js")))

	body := make([]H, 0, len(p.Body))
	for _, n := range p.Body {
		if n != nil {
			body = append(body, n)
		}
	}

	htmlChildren := make([]H, 0, 2+len(p.HTMLAttrs))
	if p.Language != "" {
		htmlChildren = append(htmlChildren, Lang(p.Language))
	}
	for _, a := range p.HTMLAttrs {
		if a != nil {
			htmlChildren = append(htmlChildren, a)
		}
	}
	htmlChildren = append(htmlChildren,
		&element{tag: "head", children: head},
		&element{tag: "body", children: body},
	)

	return doctype{sibling: &element{tag: "html", children: htmlChildren}}
}
