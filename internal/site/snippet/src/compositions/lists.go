package compositions

import (
	"strconv"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// snippet:start list
type Todo struct {
	ID    int
	Title string
}

type Todos struct {
	Items via.List[Todo]
}

func (t *Todos) Remove(ctx *via.Ctx, id int) {
	for i, x := range t.Items.Get() {
		if x.ID == id {
			t.Items.Remove(i)
			return
		}
	}
}

// snippet:start row
func (t *Todos) row(x Todo) h.H {
	id := "todo-" + strconv.Itoa(x.ID)
	del := on.WithArg(t.Remove, x.ID)
	return h.Li(h.ID(id),
		h.Str(x.Title),
		h.Button(on.Click(del),
			h.Str("×")))
}

// snippet:end

func (t *Todos) View() h.H {
	return h.Div(
		h.Ul(t.Items.Each(t.row)),
		via.When(len(t.Items.Get()) == 0, t.empty),
	)
}

func (t *Todos) empty() h.H { return h.P(h.Str("Nothing to do.")) }

// snippet:end
