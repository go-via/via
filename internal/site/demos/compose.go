package demos

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Region is one region: its own State, its own action, its own container. Two
// of them on a page never collide — via keys a child by its position among its
// parent's Child calls, and that key is the container id, the signal prefix
// and the action address alike.
type Region struct {
	Label string
	N     via.State[int]
}

func (r *Region) Bump(ctx *via.Ctx) { r.N.Set(r.N.Get() + 1) }

func (r *Region) View() h.H {
	return h.Div(h.Class("row"),
		h.Strong(h.Str(r.Label)),
		h.Output(r.N.Display()),
		h.Button(via.On("click", r.Bump), h.Str("+")),
	)
}

// Compose holds two of them as plain struct fields. It is itself plain: a live
// unit may not contain another live unit, but a plain one may nest as deep as
// it likes.
type Compose struct{ Left, Right Region }

// OnInit seeds what a parent would normally put in the child's literal;
// Compose is a zero-value field of its page, so there is no literal to seed
// from.
func (c *Compose) OnInit(ctx *via.Ctx) error {
	c.Left.Label, c.Right.Label = "left", "right"
	return nil
}

func (c *Compose) View() h.H {
	return h.Div(h.Class("pair"), via.Child(c.Left), via.Child(c.Right))
}
