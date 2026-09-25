package demos

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Counter is the smallest live unit: rendering a State is what earns this
// child its own SSE stream, and a click patches only this region.
type Counter struct{ N via.State[int] }

func (c *Counter) Inc(ctx *via.Ctx) { c.N.Set(c.N.Get() + 1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.N.Set(c.N.Get() - 1) }

func (c *Counter) View() h.H {
	return h.Div(h.Class("row"),
		h.Button(via.On("click", c.Dec), h.Str("−")),
		h.Output(c.N.Display()),
		h.Button(via.On("click", c.Inc), h.Str("+")),
	)
}
