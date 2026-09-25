package demos

import (
	"fmt"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Pulse is the smallest thing that streams: OnInit asks for a tick, so via
// opens this tab's SSE connection and pushes a re-render of this region — and
// only this region — every second.
type Pulse struct {
	Beats  via.State[int]
	Uptime via.State[string]
}

func (p *Pulse) OnInit(ctx *via.Ctx) error {
	p.Uptime.Set(pulseClock(0))
	ctx.Tick(time.Second, p.beat)
	return nil
}

func (p *Pulse) beat(ctx *via.Ctx) {
	n := p.Beats.Get() + 1
	p.Beats.Set(n)
	p.Uptime.Set(pulseClock(n))
}

func pulseClock(secs int) string { return fmt.Sprintf("%02d:%02d", secs/60, secs%60) }

func (p *Pulse) View() h.H {
	return h.Div(
		h.P(h.Class("metric"), p.Uptime.Display()),
		h.P(h.Class("note"), h.Str("beat "), p.Beats.Display(), h.Str(" since this tab connected")),
	)
}
