// Command dashboard shows live-embed multiplexing: one page, one SSE stream,
// several independent regions that re-render and patch only themselves.
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

// Header is a plain child: no State, no Tick, so it never streams. The parent
// literal seeds its text.
type Header struct{ site string }

func (hd *Header) View() h.H {
	return h.Div(h.H2(h.Str(hd.site)), h.P(h.Str("status board")))
}

// Uptime ticks its own State once a second and pushes only its own region.
type Uptime struct{ Secs via.State[int] }

var _ via.Initer = (*Uptime)(nil)

func (u *Uptime) OnInit(ctx *via.Ctx) error { ctx.Tick(time.Second, u.beat); return nil }
func (u *Uptime) beat(ctx *via.Ctx)         { u.Secs.Set(u.Secs.Get() + 1) }
func (u *Uptime) View() h.H {
	return h.Div(h.H2(h.Str("uptime")), h.P(u.Secs.Display(), h.Str("s")))
}

// Queue is a live embed with actions and no hook at all: rendering its State is
// what earns it a connection. A click routes to this embed on this connection,
// mutates its State, and patches only this region.
type Queue struct{ Pending via.State[int] }

func (q *Queue) Enqueue(ctx *via.Ctx) { q.Pending.Set(q.Pending.Get() + 1) }
func (q *Queue) Run(ctx *via.Ctx) {
	if n := q.Pending.Get(); n > 0 {
		q.Pending.Set(n - 1)
	}
}
func (q *Queue) View() h.H {
	return h.Div(
		h.H2(h.Str("build queue")),
		h.P(q.Pending.Display(), h.Str(" pending")),
		h.Button(via.On("click", q.Enqueue), h.Str("enqueue")),
		h.Button(via.On("click", q.Run), h.Str("run one")),
	)
}

// Dashboard is the shell. It holds no State and never ticks — a shell need not
// be live itself for its embedded children to be.
type Dashboard struct {
	Header Header
	Uptime Uptime
	Queue  Queue
}

func (d *Dashboard) View() h.H {
	return h.Div(
		h.H1(h.Str("dashboard")),
		via.Embed(d.Header),
		via.Embed(d.Uptime),
		via.Embed(d.Queue),
	)
}

func main() {
	dash := Dashboard{Header: Header{site: "north-1"}}
	http.Handle("/", via.Handler(dash))
	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
