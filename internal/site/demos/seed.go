package demos

import (
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Seed never binds or displays its signal: Set is what declares one, so the
// server clock below reaches the browser on first paint from OnInit, and the
// action re-Sets the same wire name. No element here holds the time as text —
// it rides on the region's signals and data-text reads it.
type Seed struct{ Clock via.Signal[string] }

func (s *Seed) OnInit(ctx *via.Ctx) error {
	s.Clock.Set(serverTime())
	return nil
}

func (s *Seed) Refresh(ctx *via.Ctx) { s.Clock.Set(serverTime()) }

func (s *Seed) View() h.H {
	return h.Div(h.Class("row"),
		h.Button(via.On("click", s.Refresh), h.Str("read the clock")),
		h.Output(h.DataText(s.Clock.Ref())),
	)
}

func serverTime() string { return time.Now().UTC().Format("15:04:05") + " UTC" }
