package content

import (
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/shell"
)

// liveLimit is one bucket per visitor for the whole page: every demo below
// lets an anonymous visitor publish to state other people are looking at.
var liveLimit = demo.NewLimiter(20)

func init() {
	demo.Reset(15*time.Minute, demos.ResetShared)
}

// Live is the page on what makes a page stream, and what it streams over.
type Live struct {
	Pulse  demos.Pulse
	Feed   demos.Feed
	Shared demos.Shared
	Ping   demos.Ping
}

func (p *Live) PageMeta() via.Meta {
	return shell.Meta("Live", "Per-tab SSE: Tick, Listen, State.Track and per-user fan-out keyed by the session id.")
}

func (p *Live) OnInit(ctx *via.Ctx) error {
	shell.EnsureSession(ctx)
	p.Feed.Lim, p.Shared.Lim, p.Ping.Lim = liveLimit, liveLimit, liveLimit
	return nil
}

func (p *Live) View() h.H {
	return shell.Page("Live", shell.Nav[3],
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
			h.P(h.Str("A topic.Topic is a plain Go value with a Publish method. ctx.Listen subscribes this unit "+
				"to one, runs the handler on the unit's own goroutine for every value, and unsubscribes when the "+
				"tab goes away. Posts appear in every tab anyone has open on this page, including yours; the list "+
				"keeps the newest 50 and posting is capped at 20 a minute per visitor.")),
			via.Child(p.Feed), "feed.go"),

		demo.Card("Shared counter",
			h.P(h.Str("State is per connection, so a number every visitor must agree on lives in a store you own "+
				"— here an atomic.Int64. The topic announces that it moved, and State.Track keeps this tab's "+
				"State equal to it: seed at init, seed again once the stream has subscribed, then follow every "+
				"publish. It resets to zero every 15 minutes, and the reset is published through the topic so "+
				"open tabs redraw rather than going stale.")),
			via.Child(p.Shared), "shared.go"),

		demo.Card("Ping me",
			h.P(h.Str("One topic carries every visitor's pings; each event names the session it is for and the "+
				"Listen handler drops the rest. ctx.Session().ID() is the routing key — it is stable, it is not "+
				"the cookie, and it grants nothing. Open this page in a second browser and ping from one: only "+
				"that one lights up, while two tabs of the same browser both do, because the session is the unit "+
				"of fan-out and not the tab.")),
			via.Child(p.Ping), "ping.go"),
	)
}
