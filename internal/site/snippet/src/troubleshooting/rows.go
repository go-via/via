// Package troubleshooting holds the compile-checked code the /troubleshooting
// page shows, and the tests that reproduce each error it quotes.
package troubleshooting

import (
	"strconv"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

type Todo struct {
	ID    int
	Title string
}

type Todos struct{ items []Todo }

func (l *Todos) Delete(ctx *via.Ctx, id int) {
	for i, t := range l.items {
		if t.ID == id {
			l.items = append(l.items[:i], l.items[i+1:]...)
			return
		}
	}
}

// snippet:start rows
func (l *Todos) row(t Todo) h.H {
	return h.Li(h.ID("todo-"+strconv.Itoa(t.ID)),
		h.Input(h.Type("checkbox")),
		h.Str(t.Title),
		h.Button(on.Click(on.WithArg(l.Delete, t.ID)), h.Str("delete")),
	)
}

func (l *Todos) View() h.H { return h.Ul(via.Each(l.items, l.row)) }

// snippet:end
