package demos

import (
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// LiveReveal hides a State without leaving it out of the render: Display runs
// on every render, hidden or not, so the unit is live from the GET and the
// reveal has a stream to arrive on.
type LiveReveal struct {
	Open  via.State[bool]
	Since via.State[string]
}

func (r *LiveReveal) OnInit(ctx *via.Ctx) error {
	r.Since.Set("held since " + time.Now().Format("15:04:05"))
	return nil
}

func (r *LiveReveal) Toggle(ctx *via.Ctx) { r.Open.Set(!r.Open.Get()) }

func (r *LiveReveal) View() h.H {
	label := "reveal"
	if r.Open.Get() {
		label = "hide"
	}
	return h.Div(
		h.Button(on.Click(r.Toggle), h.Str(label)),
		h.P(h.Class("metric"), h.Hidden(!r.Open.Get()), r.Since.Display()),
	)
}
