// Command pulse is a live island. Pulse implements OnConnect, so via opens a
// per-tab SSE stream and pushes a re-rendered fragment on every server-side
// tick — the browser updates with no client code, no WebSocket, no build step.
// The View is pure and ctx-free; there is no '&' and no closure at any call site.
package main

import (
	"cmp"
	"net/http"
	"os"
	"time"

	"github.com/go-via/via/v2"
	"github.com/go-via/via/v2/h"
)

// Pulse is server-held state pushed to the browser. Beats is a State[int] — it
// lives on the server, is read from the pure View, and via element-patches the
// re-render over SSE on every beat.
type Pulse struct{ Beats via.State[int] }

func (p *Pulse) OnConnect(ctx *via.Ctx) error {
	ctx.Tick(time.Second, p.beat)
	return nil
}

func (p *Pulse) beat(ctx *via.Ctx) { p.Beats.Set(ctx, p.Beats.Get()+1) }

func (p *Pulse) View() h.H {
	return h.Div(
		h.H1(h.Str("Server pulse")),
		h.P(h.Str("beats since you connected: "), p.Beats.Display()),
	)
}

func main() {
	http.Handle("/", via.Register(Pulse{}))
	http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil)
}
