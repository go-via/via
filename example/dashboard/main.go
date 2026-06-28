// Command dashboard shows live-island multiplexing: one page, one SSE stream,
// several independent regions. The Dashboard is a static shell that embeds child
// compositions with via.Child[C]; each child re-renders and patches only itself.
// A child without OnConnect (Greeting) is a plain in-place component; a child
// with OnConnect (Clock, Counter) is a live island pushed over the shared
// stream. via.NewChild seeds a child's data (Greeting's name). Zero '&', no
// identifier strings, no closures at any call site.
package main

import (
	"net/http"
	"time"

	"github.com/go-via/via/v2"
	"github.com/go-via/via/v2/h"
)

// Greeting is a PLAIN child — no OnConnect, so it just renders structure. It is
// seeded with a name via via.NewChild; nothing about it streams.
type Greeting struct{ name string }

func (g *Greeting) View() h.H {
	return h.Div(h.H2(h.Str("welcome")), h.P(h.Str("hello, "), h.Str(g.name)))
}

// Clock is a LIVE island: it ticks its own State once a second and pushes only
// its own region — the counter and greeting are untouched by its updates.
type Clock struct{ secs via.State[int] }

func (c *Clock) OnConnect(ctx *via.Ctx) error { ctx.Tick(time.Second, c.beat); return nil }
func (c *Clock) beat(ctx *via.Ctx)            { c.secs.Set(ctx, c.secs.Get()+1) }
func (c *Clock) View() h.H {
	return h.Div(h.H2(h.Str("uptime")), h.P(c.secs.Display(), h.Str("s")))
}

// Counter is a LIVE island with an action: its + button routes to this island
// on this connection (the via_tab handshake), mutates its State, and the result
// rides back over the shared stream — patching only the counter's region.
type Counter struct{ n via.State[int] }

func (c *Counter) OnConnect(ctx *via.Ctx) error { return nil }
func (c *Counter) Inc(ctx *via.Ctx)             { c.n.Set(ctx, c.n.Get()+1) }
func (c *Counter) View() h.H {
	return h.Div(
		h.H2(h.Str("clicks")),
		h.P(c.n.Display()),
		h.Button(via.OnClick(c.Inc), h.Str("+")),
	)
}

// Dashboard is the shell. It does NOT implement OnConnect — it isn't a live
// composition itself; its embedded children are. They all share this page's one
// SSE stream.
type Dashboard struct {
	Greeting via.Child[Greeting]
	Clock    via.Child[Clock]
	Counter  via.Child[Counter]
}

func (d *Dashboard) View() h.H {
	return h.Div(
		h.H1(h.Str("dashboard")),
		d.Greeting.Embed(), // plain — rendered in place, never streams
		d.Clock.Embed(),    // live — ticks, pushes its own region
		d.Counter.Embed(),  // live — its + button patches only its own region
	)
}

func main() {
	// One Register, one route, one stream. NewChild seeds the greeting's name;
	// Clock and Counter need no data, so their zero Child is fine.
	dash := Dashboard{Greeting: via.NewChild(Greeting{name: "alice"})}
	http.Handle("/", via.Register(dash, via.WithTheme()))
	http.ListenAndServe(":8080", nil)
}
