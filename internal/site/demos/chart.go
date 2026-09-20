package demos

import (
	"math/rand/v2"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
)

// Chart drives the same island function as Sparkline, from a click instead of
// a clock. It holds no State and never ticks, so this demo does not stream:
// the plain action's element patch carries the one signal the handler wrote,
// and the effect re-runs on it.
type Chart struct {
	Series via.Signal[[]int] `via:"init=[]"`
}

func (c *Chart) OnInit(ctx *via.Ctx) error {
	c.Series.Set(samples())
	return nil
}

func (c *Chart) Fresh(ctx *via.Ctx) { c.Series.Set(samples()) }

func (c *Chart) View() h.H {
	return h.Div(
		h.Div(h.Class("row"), h.Button(via.On("click", c.Fresh), h.Str("New data"))),
		h.Canvas(h.Class("chart"), h.DataIgnoreMorph(),
			h.Width(420), h.Height(90),
			h.DataEffect(expr.Call("viaChart", expr.El, c.Series.Ref()))),
	)
}

func samples() []int {
	s := make([]int, 16)
	for i := range s {
		s[i] = 10 + rand.IntN(90)
	}
	return s
}
