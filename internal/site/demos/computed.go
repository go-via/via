package demos

import (
	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
)

// Computed derives from two signals in the browser. The sum and the class on
// it are expressions over the same two refs, re-evaluated on every keystroke.
type Computed struct {
	A via.Signal[int] `via:"init=2"`
	B via.Signal[int] `via:"init=3"`
}

// expr has no arithmetic past Add, so the addition is Rawf text — with the two
// refs spliced in checked rather than spelled by hand.
func (c *Computed) sum() expr.Expr {
	return expr.Rawf("(%s + %s)", c.A.Ref(), c.B.Ref())
}

func (c *Computed) View() h.H {
	return h.Div(
		h.Label(h.Str("A"), h.Input(h.Type("number"), c.A.Bind())),
		h.Label(h.Str("B"), h.Input(h.Type("number"), c.B.Bind())),
		h.P(h.Str("A + B = "),
			h.Span(h.Class("total"), h.DataText(c.sum()), h.DataClass("over", c.sum().Gt(10))),
			h.Small(h.Str(" (turns amber above 10)")),
		),
	)
}
