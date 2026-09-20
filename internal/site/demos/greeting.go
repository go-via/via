package demos

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Greeting is two views of one signal: Bind() and Display() resolve to the
// same wire name, so the sentence follows the input with no request at all.
type Greeting struct{ Name via.Signal[string] }

func (g *Greeting) View() h.H {
	return h.Div(
		h.Label(h.Str("Your name"),
			h.Input(g.Name.Bind(), h.Placeholder("type here"), h.AutoComplete("off")),
		),
		h.P(h.DataShow(g.Name.Ref().Ne("")), h.Str("Hello, "), g.Name.Display(), h.Str("!")),
		h.P(h.DataShow(g.Name.Ref().Eq("")), h.Small(h.Str("The greeting appears once you type."))),
	)
}
