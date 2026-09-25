// Package live holds the compile-checked code the /live page shows.
package live

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// snippet:start closed
type Reveal struct {
	Open   via.Signal[bool]
	Secret via.State[string]
}

func (r *Reveal) Toggle(ctx *via.Ctx) { r.Open.Set(!r.Open.Get()) }

func (r *Reveal) View() h.H {
	return h.Div(
		h.Button(on.Click(r.Toggle), h.Str("reveal")),
		via.When(r.Open.Get(), r.Secret.Display),
	)
}

// snippet:end
