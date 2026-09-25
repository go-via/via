package demos

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
)

var relayChoices = []string{"Go", "Rust", "Zig"}

// Relay hands both children one topic.
type Relay struct {
	Picker  Picker
	Summary Summary
}

// Picker publishes without knowing who listens.
type Picker struct {
	Picks *topic.Topic[string]
	Sent  via.State[int]
}

// Summary listens, and its re-render is pushed after each value.
type Summary struct {
	Picks *topic.Topic[string]
	Last  via.State[string]
	Count via.State[int]
}

// snippet:start relay
// A new topic per render: each tab's
// stream gets its own.
func (r *Relay) OnInit(
	ctx *via.Ctx,
) error {
	t := topic.New[string]()
	r.Picker.Picks = t
	r.Summary.Picks = t
	return nil
}

func (p *Picker) Pick(
	ctx *via.Ctx, choice string,
) {
	p.Sent.Set(p.Sent.Get() + 1)
	p.Picks.Publish(choice)
}

func (s *Summary) OnInit(
	ctx *via.Ctx,
) error {
	ctx.Listen(s.Picks, s.picked)
	return nil
}

// snippet:end

func (s *Summary) picked(ctx *via.Ctx, choice string) {
	s.Last.Set(choice)
	s.Count.Set(s.Count.Get() + 1)
}

func (r *Relay) View() h.H {
	return h.Div(h.Class("pair"), via.Child(r.Picker), via.Child(r.Summary))
}

func (p *Picker) View() h.H {
	return h.Div(
		h.Div(h.Class("row"), via.Each(relayChoices, p.button)),
		h.P(h.Class("note"), h.Str("sent: "), p.Sent.Display()),
	)
}

func (p *Picker) button(choice string) h.H {
	return h.Button(on.Click(on.WithArg(p.Pick, choice)), h.Str(choice))
}

func (s *Summary) View() h.H {
	return h.Div(
		h.P(h.Str("last pick: "), h.Strong(s.Last.Display())),
		h.P(h.Class("note"), h.Str("received: "), s.Count.Display()),
	)
}
