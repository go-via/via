package demos

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// Toggle holds a panel open or closed with a SignalCS, whose wire name is
// _-prefixed — Datastar's fetch filter drops those, so the flag never leaves
// the browser. The tag decides what it starts as at first paint.
type Toggle struct {
	Open via.SignalCS[bool] `via:"init=false"`
}

func (t *Toggle) View() h.H {
	return h.Div(
		h.Button(on.ClickCS(t.Open.Ref().Toggle()), h.Str("open: "), t.Open.Display()),
		h.Div(h.Class("panel"), h.DataShow(t.Open.Ref()),
			h.P(h.Str("Shown by data-show, not by a re-render. The server has no field "+
				"for this and no handler ran.")),
		),
	)
}
