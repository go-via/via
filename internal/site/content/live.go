package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/shell"
)

// Live is the page on what makes a page stream, and what it streams over.
type Live struct {
	page
	Pulse  demos.Pulse
	Feed   demos.Feed
	Shared demos.Shared
	Ping   demos.Ping
}

// NewLive gives each demo its own limiter, so a visitor spending the feed's
// budget still has pings and shared clicks left.
func NewLive(origin string) Live {
	return Live{
		page:   newPage("/live", origin),
		Feed:   demos.NewFeed(demo.NewLimiter(20)),
		Shared: demos.NewShared(demo.NewLimiter(20)),
		Ping:   demos.NewPing(demo.NewLimiter(20)),
	}
}

func (p *Live) PageMeta() via.Meta {
	return p.meta("Per-tab SSE: Tick, Listen, StateTrack and per-user fan-out keyed by the session id.")
}

func (p *Live) View() h.H {
	return shell.Page(p.nav,
		h.P(h.Str("A page is a request and a response until a unit on it acts live: its OnInit registered a "+
			"ctx.Tick or a ctx.Listen, or its View displayed a State. Then via opens one SSE connection for that "+
			"tab and pushes element patches down it. Actions do not change that — a click still POSTs, and on a "+
			"streaming page the answer comes back as a frame on the connection instead of as the POST's own body. "+
			"Liveness is the verdict of the render that served the page, so a State behind a branch that was "+
			"closed at GET wires the page plain: render it unconditionally, or register the Tick in OnInit.")),

		demo.Card("Pulse",
			h.P(h.Str("ctx.Tick(time.Second, p.beat) is the whole subscription. Every beat runs the handler, "+
				"re-renders this unit and patches it; the header, the sidebar and the demos below it are not "+
				"touched. One stream per tab, not per unit — every live unit on this page shares this one "+
				"connection.")),
			via.Child(p.Pulse), "pulse.go"),

		demo.Card("Broadcast feed",
			h.P(h.Str("A topic.Topic is built with topic.New[T]() and held for the life of the process; the zero "+
				"Topic is not usable. ctx.Listen subscribes this unit to one, runs the handler "+
				"on the unit's own goroutine for every value, and unsubscribes when the tab goes away. Posts "+
				"appear in every tab anyone has open on this page, including yours. The list is this tab's own, "+
				"keeps the newest 50, and posting is capped at 20 a minute per client IP.")),
			via.Child(p.Feed), "feed.go"),

		demo.Card("Shared counter",
			h.Div(
				h.P(h.Str("State is per connection, so a number every visitor must agree on lives in a store you "+
					"own — here an atomic.Int64. The topic only announces that the store moved. "+
					"via.StateTrack(topic, load) keeps this tab's State equal to it: seed at init, seed again "+
					"once the stream has subscribed, then follow every publish.")),
				h.P(h.Str("The source decides which of the two forms to use. The StateTrack literal is for a "+
					"source fixed at mount, as it is here — the demo needs no OnInit to be live. State.Track in "+
					"OnInit is for a source that depends on the request, such as the room named by a path param.")),
				h.P(h.Str("The counter resets to zero every 15 minutes, and the reset is published through the "+
					"topic so open tabs redraw rather than going stale.")),
			),
			via.Child(p.Shared), "shared.go"),

		demo.Card("Ping me",
			h.P(h.Str("One topic carries every visitor's pings. Each event names the session it is for, and the "+
				"Listen handler drops the rest. ctx.Session().ID() is the routing key: it is stable, it is not "+
				"the cookie, and it grants nothing. Open this page in a second browser and ping from one — only "+
				"that browser lights up. Two tabs of the same browser both light up, because the session is the "+
				"unit of fan-out and not the tab.")),
			via.Child(p.Ping), "ping.go"),

		h.P(h.A(h.Href("/islands"), h.Str("Next: handing a subtree to a JS library"))),
	)
}
