// Command dashboard shows live-embed multiplexing: one page, one SSE stream,
// several independent regions. The Dashboard is a static shell that embeds child
// compositions — plain struct fields rendered with via.Embed; each child
// re-renders and patches only itself. A child with neither server State nor a
// Tick (Greeting) is a plain in-place component; a child that holds State or
// ticks (Clock, Counter) is a live embed pushed over the shared stream. The parent literal seeds a child's data
// (Greeting's name). Zero '&', no identifier strings, no closures at any call site.
package main

import (
	"net/http"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Greeting is a PLAIN child — no State, no Tick, so it just renders structure. It is
// seeded with a name at the parent's literal; nothing about it streams.
type Greeting struct{ name string }

func (g *Greeting) View() h.H {
	return h.Div(h.H2(h.Str("welcome")), h.P(h.Str("hello, "), h.Str(g.name)))
}

// Clock is a LIVE embed: it ticks its own State once a second and pushes only
// its own region — the counter and greeting are untouched by its updates.
type Clock struct{ secs via.State[int] }

func (c *Clock) OnInit(ctx *via.Ctx) error { ctx.Tick(time.Second, c.beat); return nil }
func (c *Clock) beat(ctx *via.Ctx)         { c.secs.Set(c.secs.Get() + 1) }
func (c *Clock) View() h.H {
	return h.Div(h.H2(h.Str("uptime")), h.P(c.secs.Display(), h.Str("s")))
}

// Counter is a LIVE embed with an action, and it needs no hook at all to be
// one: rendering its State is what earns it a connection. The + button routes
// to this embed on this connection (the tab handshake), mutates its State, and
// the result rides back over the shared stream — patching only its region.
type Counter struct{ n via.State[int] }

func (c *Counter) Inc(ctx *via.Ctx) { c.n.Set(c.n.Get() + 1) }
func (c *Counter) View() h.H {
	return h.Div(
		h.H2(h.Str("clicks")),
		h.P(c.n.Display()),
		h.Button(via.On("click", c.Inc), h.Str("+")),
	)
}

// Dashboard is the shell. It holds no State and never ticks — a shell need
// not be live itself for its embedded children to be; they all share this
// page's one SSE stream.
type Dashboard struct {
	Greeting Greeting
	Clock    Clock
	Counter  Counter
}

func (d *Dashboard) View() h.H {
	return h.Div(
		h.H1(h.Str("dashboard")),
		via.Embed(d.Greeting), // plain — rendered in place, never streams
		via.Embed(d.Clock),    // live — ticks, pushes its own region
		via.Embed(d.Counter),  // live — its + button patches only its own region
	)
}

func main() {
	// One Register, one route, one stream. The parent literal seeds the
	// greeting's name; Clock and Counter need no data, so their zero value is fine.
	dash := Dashboard{Greeting: Greeting{name: "alice"}}
	http.Handle("/", via.Register(dash))
	http.ListenAndServe(":8080", nil)
}
