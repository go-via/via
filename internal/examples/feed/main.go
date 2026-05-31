// Feed demo for the append-only Signal[[]T] surface: a bounded ring
// buffer streams random values to the browser five times per second,
// keeping the most recent 50. The view binds to the entire array via
// Datastar's reactive expressions — no client JS, no DOM bookkeeping.
//
//	go run ./internal/examples/feed
//	open http://localhost:3000
package main

import (
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

const windowSize = 50

type Feed struct {
	Points  via.SignalSlice[float64] `via:"points"`
	Running via.SignalBool           `via:"running,init=true"`
}

func (p *Feed) Toggle(ctx *via.Ctx) {
	p.Running.Op(ctx).Toggle()
}

func (p *Feed) Clear(ctx *via.Ctx) {
	p.Points.Write(ctx, nil)
}

func (p *Feed) OnConnect(ctx *via.Ctx) error {
	via.Stream(ctx, 200*time.Millisecond, func(ctx *via.Ctx, _ time.Time) {
		if !p.Running.Read(ctx) {
			return
		}
		_ = p.Points.Update(ctx, func(s []float64) ([]float64, error) {
			s = append(s, rand.Float64()*100)
			if len(s) > windowSize {
				copy(s, s[len(s)-windowSize:])
				s = s[:windowSize]
			}
			return s, nil
		})
	})
	return nil
}

func (p *Feed) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.H1(h.Text("Live feed")),
		h.P(h.Text("Latest "), h.Strong(h.Text("50")),
			h.Text(" values pushed from the server, 5×/sec. Pure server push, zero hand-written JS.")),
		h.Div(
			h.Button(h.Text("Toggle"), on.Click(p.Toggle)),
			h.Button(h.Text("Clear"), on.Click(p.Clear)),
		),
		h.Pre(
			h.Style("padding:1rem;background:#111;color:#0f0;font-family:monospace;white-space:pre-wrap;line-height:1.4"),
			h.Data("text", "$points.map(v => v.toFixed(1)).join(' ')"),
		),
	)
}

func main() {
	app := via.New(via.WithTitle("Feed"))
	via.Mount[Feed](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
