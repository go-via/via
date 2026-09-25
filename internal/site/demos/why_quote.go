package demos

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// snippet:start model
type WhyQuote struct {
	Qty   via.Signal[int] `via:"init=1"`
	Total via.State[int]
}

func (q *WhyQuote) Price(ctx *via.Ctx) {
	// Qty came from the browser, so it is input like any form field.
	q.Total.Set(max(q.Qty.Get(), 0) * 12)
}

func (q *WhyQuote) View() h.H {
	return h.Div(h.Class("row"),
		h.Input(h.Type("number"), h.Min(0), q.Qty.Bind()),
		h.Button(on.Click(q.Price), h.Str("Price")),
		h.Output(q.Total.Display()),
	)
}

// snippet:end
