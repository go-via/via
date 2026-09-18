// Command dashboard shows live-child multiplexing: one page, one SSE stream,
// several independent regions that re-render and patch only themselves. It
// also feeds a JS-owned canvas from Go with a Signal the View never binds, and
// keeps a details toggle on the client with a SignalCS the server never sees,
// seeded open by a struct tag.
package main

import (
	"cmp"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
)

// Header is a plain child: no State, no Tick, so it never streams. The parent
// literal seeds its text.
type Header struct{ site string }

func (hd *Header) View() h.H {
	return h.Div(h.H2(h.Str(hd.site)), h.P(h.Str("status board")))
}

// Uptime ticks its own State once a second and pushes only its own region.
// Load is a Signal nothing renders: Set declares it, so each tick's patch
// carries the series to the canvas island below without a Bind or Display. The
// tag starts it at an empty array, so the island's effect never sees the null
// a nil slice would marshal to.
type Uptime struct {
	Secs via.State[int]
	Load via.Signal[[]int] `via:"init=[]"`
	load []int
}

var _ via.Initer = (*Uptime)(nil)

func (u *Uptime) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Second, u.beat)
	return nil
}

func (u *Uptime) beat(ctx *via.Ctx) {
	u.Secs.Set(u.Secs.Get() + 1)
	u.load = append(u.load, rand.IntN(100))
	if len(u.load) > 30 {
		u.load = u.load[1:]
	}
	u.Load.Set(u.load)
}

func (u *Uptime) View() h.H {
	return h.Div(
		h.H2(h.Str("uptime")),
		h.P(u.Secs.Display(), h.Str("s")),
		// DataIgnoreMorph keeps this node across live patches, so the bitmap the
		// effect drew survives; the effect re-runs when $load changes.
		h.Canvas(h.DataIgnoreMorph(), h.Width(120), h.Height(30), h.DataEffect(sparkline(u.Load.Ref()))),
	)
}

// sparkline draws series on the canvas the effect runs on. expr has no loop
// or arithmetic past Add, so the drawing is Rawf text; the signal and the
// element are spliced in checked rather than spelled by hand.
func sparkline(series expr.Expr) expr.Expr {
	return expr.Rawf(`const s=%s,c=%s.getContext('2d'),w=c.canvas.width,ht=c.canvas.height;`+
		`c.clearRect(0,0,w,ht);c.beginPath();`+
		`s.forEach((v,i)=>c.lineTo(i*w/Math.max(s.length-1,1),ht-v*ht/100));c.stroke()`,
		series, expr.El)
}

// Queue is a live child with actions and no hook at all: rendering its State is
// what earns it a connection. A click routes to this child on this connection,
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
// be live itself for its embedded children to be. Details is client-only:
// toggling it is no request, Datastar never posts it, and the tag seeds it
// open at first paint.
type Dashboard struct {
	Details via.SignalCS[bool] `via:"init=true"`
	Header  Header
	Uptime  Uptime
	Queue   Queue
}

func (d *Dashboard) View() h.H {
	return h.Div(
		h.H1(h.Str("dashboard")),
		h.Button(h.DataOn("click", d.Details.Ref().Toggle()), h.Str("details: "), d.Details.Display()),
		h.Div(h.DataShow(d.Details.Ref()), via.Child(d.Header)),
		via.Child(d.Uptime),
		via.Child(d.Queue),
	)
}

func main() {
	dash := Dashboard{Header: Header{site: "north-1"}}
	http.Handle("/", via.Handler(dash))
	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
