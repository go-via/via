// Package actions holds the compile-checked samples on /actions.
package actions

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

type Todo struct {
	ID    int
	Title string
}

// snippet:start signatures
type Todos struct {
	Items via.List[Todo]
}

// Argless: bind the method value, no & at the call site.
func (t *Todos) Clear(ctx *via.Ctx) { t.Items.Set(nil) }

// With an argument: the second parameter's type is the arg's type.
func (t *Todos) Delete(ctx *via.Ctx, id int) {
	items := t.Items.Get()
	for i, it := range items {
		if it.ID == id {
			t.Items.Remove(i)
			return
		}
	}
}

func (t *Todos) row(it Todo) h.H {
	return h.Li(
		h.Span(h.Str(it.Title)),
		h.Button(on.Click(on.WithArg(t.Delete, it.ID)), h.Str("×")),
	)
}

func (t *Todos) View() h.H {
	return h.Div(
		h.Ul(t.Items.Each(t.row)),
		h.Button(on.Click(t.Clear), h.Str("Clear")),
	)
}

// snippet:end
