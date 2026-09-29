// Package troubleshooting holds the compile-checked code the /troubleshooting
// page shows, and the tests that reproduce each error it quotes.
package troubleshooting

import (
	"slices"
	"strconv"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

type Todo struct {
	ID    int
	Title string
}

type Todos struct{ Items []Todo }

func (l *Todos) Delete(ctx *via.Ctx, id int) {
	for i, t := range l.Items {
		if t.ID == id {
			// Items shares its backing array with the literal passed to
			// Mount, so deleting in place would edit every later request's rows.
			l.Items = slices.Concat(l.Items[:i], l.Items[i+1:])
			return
		}
	}
}

// snippet:start rows
func (l *Todos) row(t Todo) h.H {
	return h.Li(h.ID("todo-"+strconv.Itoa(t.ID)),
		h.Input(h.Type("checkbox")),
		h.Str(t.Title),
		h.Button(on.Click(on.Bind(l.Delete, t.ID)), h.Str("delete")),
	)
}

func (l *Todos) View() h.H { return h.Ul(via.Each(l.Items, l.row)) }

// snippet:end
