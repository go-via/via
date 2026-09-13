// Command pulse is a live embed: OnInit registers a tick, so via opens a per-tab
// SSE stream and pushes a re-rendered fragment on every beat.
package main

import (
	"cmp"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Pulse is server-held state pushed to the browser. Beats is a State[int] — it
// lives on the server, is read from the pure View, and via element-patches the
// re-render over SSE on every beat.
type Pulse struct{ Beats via.State[int] }

var _ via.Initer = (*Pulse)(nil)

func (p *Pulse) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Second, p.beat)
	return nil
}

func (p *Pulse) beat(ctx *via.Ctx) { p.Beats.Set(p.Beats.Get() + 1) }

func (p *Pulse) View() h.H {
	return h.Div(
		h.H1(h.Str("Server pulse")),
		h.P(h.Str("beats since you connected: "), p.Beats.Display()),
	)
}

func main() {
	http.Handle("/", via.Handler(Pulse{}))
	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
