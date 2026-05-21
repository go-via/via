// Countercomp shows nested compositions: two independent counter cards
// inside one page. Each child renders its own state; actions live on the
// parent and forward to the child instance the user clicked.
//
//	go run ./internal/examples/countercomp
package main

import (
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

type CounterCard struct {
	Count via.StateTab[int]
	Step  via.Signal[int] `via:"step,init=1"`
}

func (c *CounterCard) Inc(ctx *via.Ctx) {
	c.Count.Update(ctx, func(n int) int { return n + c.Step.Read(ctx) })
}

// View takes the click attribute as a parameter so the parent can decide
// which action drives this card.
func (c *CounterCard) View(ctx *via.CtxR, onClick h.H) h.H {
	return h.Div(
		h.P(h.Textf("Count: %d", c.Count.Read(ctx))),
		h.P(h.Text("Step: "), c.Step.Text()),
		h.Label(
			h.Text("Update Step: "),
			h.Input(h.Type("number"), c.Step.Bind()),
		),
		h.Button(h.Text("Increment"), onClick),
	)
}

type Page struct {
	A CounterCard
	B CounterCard
}

func (p *Page) IncA(ctx *via.Ctx) { p.A.Inc(ctx) }
func (p *Page) IncB(ctx *via.Ctx) { p.B.Inc(ctx) }

func (p *Page) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.H1(h.Text("Counter 1")),
		p.A.View(ctx, on.Click(p.IncA)),
		h.H1(h.Text("Counter 2")),
		p.B.View(ctx, on.Click(p.IncB)),
	)
}

func main() {
	app := via.New(via.WithTitle("Counter Components"))
	via.Mount[Page](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
