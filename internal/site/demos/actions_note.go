package demos

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// Note submits in place: on.Submit posts the tab's signals as JSON, so the
// field is a bound Signal, not a form value, and the answer patches this
// region instead of loading a new page.
type Note struct {
	Draft via.Signal[string]
	Err   via.Signal[string]
	Notes via.List[string]
}

func (n *Note) Add(ctx *via.Ctx) {
	text := strings.TrimSpace(n.Draft.Get())
	if text == "" {
		n.Err.Set("Write something first.")
		return
	}
	if len([]rune(text)) > 80 {
		n.Err.Set("80 characters at most.")
		return
	}
	n.Notes.Append(text)
	trim(&n.Notes, 5)
	n.Draft.Set("")
	n.Err.Set("")
}

func (n *Note) View() h.H {
	return h.Div(
		h.Form(h.Class("row"), on.Submit(n.Add),
			h.Input(n.Draft.Bind(), h.Placeholder("a note"), h.AutoComplete("off")),
			h.Button(h.Type("submit"), h.Str("Add")),
		),
		h.Small(h.Class("err"), n.Err.Display()),
		h.Ul(n.Notes.Each(func(s string) h.H { return h.Li(h.Str(s)) })),
	)
}
