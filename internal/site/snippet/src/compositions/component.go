// Package compositions holds the compile-checked samples of the /compositions
// page.
package compositions

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// snippet:start stepper
type Stepper struct {
	Label string         // prop: whoever embeds or mounts it sets it
	By    int            // prop with a default, filled in by OnInit
	N     via.State[int] // state: on the server, one per connection
}

func (s *Stepper) OnInit(ctx *via.Ctx) error {
	if s.By == 0 {
		s.By = 1
	}
	return nil
}

func (s *Stepper) Add(ctx *via.Ctx) { s.N.Set(s.N.Get() + s.By) }

func (s *Stepper) View() h.H {
	return h.Div(
		h.Strong(h.Str(s.Label)),
		h.Output(s.N.Display()),
		h.Button(on.Click(s.Add), h.Str("+")),
	)
}

// snippet:end

func (a *Avatar) View() h.H {
	return h.Span(h.Str(a.Name), h.Small(h.Str(a.Size), h.Str("px")))
}

func (p *Profile) View() h.H { return h.Div(via.Child(p.Me)) }

func lookup(ctx *via.Ctx) string { return "ana" }

// snippet:start props
type Avatar struct {
	Name string
	Size int
}

type Profile struct{ Me Avatar }

// a constant prop: in the literal
var me = Profile{
	Me: Avatar{Size: 48},
}

// request data: in OnInit
func (p *Profile) OnInit(
	ctx *via.Ctx,
) error {
	p.Me.Name = lookup(ctx)
	return nil
}

// snippet:end
