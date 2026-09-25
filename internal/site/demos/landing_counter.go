package demos

import (
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// LandingCounter is the front page's counter as it runs there. Nothing in its
// View is live, so a click is one POST whose response is the patch.
type LandingCounter struct{ n *atomic.Int64 }

var landingCount atomic.Int64

func NewLandingCounter() LandingCounter { return LandingCounter{n: &landingCount} }

func resetLanding() { landingCount.Store(0) }

func (c *LandingCounter) Inc(ctx *via.Ctx) { c.n.Add(1) }
func (c *LandingCounter) Dec(ctx *via.Ctx) { c.n.Add(-1) }

func (c *LandingCounter) View() h.H {
	return h.Div(h.Class("row"),
		h.Button(on.Click(c.Dec), h.Str("−")),
		h.Output(h.Str(c.n.Load())),
		h.Button(on.Click(c.Inc), h.Str("+")),
	)
}
