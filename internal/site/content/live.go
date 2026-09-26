package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
)

// Live is the page on what makes a page stream, and what it streams over.
type Live struct {
	page
	Reveal   demos.LiveReveal
	Pulse    demos.Pulse
	Feed     demos.Feed
	Shared   demos.Shared
	Ping     demos.Ping
	Presence demos.LivePresence
}

// NewLive gives each demo its own limiter, so a visitor spending the feed's
// budget still has pings and shared clicks left.
func NewLive(env Env) Live {
	return Live{
		page:     newPage("/live", env),
		Feed:     demos.NewFeed(demo.NewLimiter(20)),
		Shared:   demos.NewShared(demo.NewLimiter(20)),
		Ping:     demos.NewPing(demo.NewLimiter(20)),
		Presence: demos.NewLivePresence(),
	}
}

func (p *Live) PageMeta() via.Meta {
	m := p.meta("Per-tab SSE: Tick, Listen, StateTrack and per-user fan-out keyed by the session id.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

func (p *Live) View() h.H {
	d := p.doc()
	secondTab := func(anchor string) h.H {
		return h.A(h.Href(d.Href("/live")+"#"+anchor), h.Target("_blank"), h.Rel("noopener"),
			h.Str("Open in a second tab ↗"))
	}
	return d.Page(
		h.P(h.Str("The server pushes to a tab when a unit on the page asks for it.")),

		d.H2("A page is plain until a unit goes live"),
		h.P(h.Str("Any one of these makes its unit live, and the page streams:")),
		table([]string{"Trigger", "Where", "Effect"},
			[]h.H{APIText("via.Ctx.Tick", "ctx.Tick(d, fn)"), h.Str("OnInit"), h.Str("Runs fn every d, then re-renders the unit and pushes the patch.")},
			[]h.H{APIText("via.Ctx.Listen", "ctx.Listen(t, fn)"), h.Str("OnInit"), h.Str("Runs fn for every value published on t, then re-renders and pushes. Unsubscribes when the tab goes away.")},
			[]h.H{h.Span(APIText("via.State.Display", "State.Display()"), h.Str(", "), APIText("via.List.Each", "List.Each(row)")), h.Str("View"), h.Str("Renders server state; a change pushes a patch on the tab's stream.")},
			[]h.H{APIText("via.StateTrack", "via.StateTrack(t, load)"), h.Str("field literal"), h.Str("Seeds from load, seeds again once the stream has subscribed, then follows t. For a source fixed at mount.")},
			[]h.H{APIText("via.State.Track", "State.Track(ctx, t, load)"), h.Str("OnInit"), h.Str("The same, for a source that depends on the request, such as a path param.")},
		),

		h.P(h.Str("Without a trigger, a page is a request and a response. With one, via opens one SSE connection "+
			"for the tab and pushes element patches down it. One stream per tab, not per unit: every "),
			h.A(h.Href(d.Href("/glossary#live-unit")), h.Str("live unit")), h.Str(" on the page shares one connection. "+
				"A tab is that one connection, so \"per tab\" in these docs means per connection: a reload is a new tab.")),
		h.P(h.Str("Actions do not change transport. A click still POSTs; with a live unit on the page the answer arrives "+
			"as a frame on the stream instead of as the POST's own body.")),
		h.P(h.Str("Liveness is decided once, by the render that served the page. An action cannot turn a plain "+
			"page into a live one.")),

		d.H2("Server state"),
		demo.Card(d.H3("Reveal"),
			h.P(API("via.State"), h.Str(" is server-held and per tab. "), API("via.State.Display"),
				h.Str(" renders it and marks its unit live; "), API("via.State.Get"),
				h.Str(" does not. Here the State is rendered on every request and only hidden, so this unit is "+
					"live from the GET, and the reveal comes back as a frame on the stream.")),
			via.Child(p.Reveal), "live_reveal.go",
			demo.WireOpen(),
			demo.Try(h.Str("Click reveal: the Wire pane shows the POST, then the patch arriving on the SSE stream."),
				h.Str("Reload and reveal again: the time moves, because each connection runs OnInit afresh."))),
		Callout(Warning, "State behind a closed branch never goes live",
			snippet.Region("live/closed.go", "closed", snippet.Title(""), snippet.Mark("via.When")),
			h.P(h.Str("At GET the branch is closed, "), API("via.State.Display"), h.Str(" never runs, and the "+
				"page is served plain. The click that opens the branch would need a stream the tab never opened, "+
				"so via fails that action with a 500 and logs the reason, instead of leaving a tab where every "+
				"later action answers 410. Render the State unconditionally and hide it, as the demo does, or "+
				"register a "), API("via.Ctx.Tick"), h.Str(" or "), API("via.Ctx.Listen"), h.Str(" in OnInit."))),

		d.H2("Timers"),
		demo.Card(d.H3("Pulse"),
			h.P(APIText("via.Ctx.Tick", "ctx.Tick(time.Second, p.beat)"), h.Str(" is the whole subscription. "+
				"Every beat runs the handler on the unit's goroutine, re-renders this unit and patches it; the "+
				"header, the sidebar and the other demos are not touched. Tick is valid only inside OnInit; a "+
				"later call registers nothing and logs.")),
			via.Child(p.Pulse), "pulse.go",
			demo.Try(h.Str("Open Wire: one patch a second, carrying only this card's markup."))),

		d.H2("Topics"),
		demo.Card(d.H3("Broadcast feed"),
			h.P(h.Str("A "), API("topic.Topic"), h.Str(" is built with "), API("topic.New"),
				h.Str(" and held for the life of the process; the zero Topic is not usable. "), API("via.Ctx.Listen"),
				h.Str(" subscribes this unit, runs the handler on the unit's own goroutine for every value, and "+
					"unsubscribes when the tab goes away. The list is this tab's own and keeps the newest 50; "+
					"posting is capped at 20 a minute per client IP.")),
			via.Child(p.Feed), "feed.go",
			demo.Try(secondTab("broadcast-feed"),
				h.Str("Post from one tab: the event lands in both."))),
		Callout(Caveat, "Slow subscribers",
			h.P(API("topic.Topic.Publish"), h.Str(" never blocks. Each subscriber has its own queue; a Listen "+
				"handler drains the whole backlog in one batch, then re-renders once and sends one frame, so a "+
				"burst costs frames at the rate the tab drains, not the rate you publish.")),
			h.P(h.Str("A queue holds at most "), API("topic.DefaultLimit"), h.Str(" (100,000) undelivered values. "+
				"Past that, Publish drops the new value for that subscriber only, and logs once per subscription. "+
				"Listen always subscribes with the default; "), API("topic.Topic.SubscribeLimit"),
				h.Str(" and "), API("topic.Sub.Dropped"), h.Str(" are for a subscription you own."))),

		d.H2("Shared state across tabs"),
		demo.Card(d.H3("Shared counter"),
			h.Div(
				h.P(h.Str("State is per tab, so a number every visitor must agree on lives in a store you "+
					"own, here an int64 behind a mutex held across the change and the publish, so tabs never "+
					"settle on an older value. The topic only announces that the store moved. "),
					API("via.StateTrack"), h.Str(" keeps this tab's State equal to it: seed at init, seed again "+
						"once the stream has subscribed, then follow every publish.")),
				h.P(h.Str("The literal form is for a source fixed at mount, as here, and needs no OnInit. "),
					API("via.State.Track"), h.Str(" in OnInit is for a source that depends on the request, such "+
						"as the room named by a path param. The counter resets to zero every 15 minutes, through "+
						"the topic, so open tabs redraw.")),
			),
			via.Child(p.Shared), "shared.go",
			demo.Try(secondTab("shared-counter"),
				h.Str("Click +1 in one tab and watch the other follow."))),
		h.P(APIText("via.State.Track", "Track"), h.Str(" works on a "), API("via.List"),
			h.Str(" too. The store publishes a copy of the whole slice on every change, and each tab's list follows it:")),
		snippet.Region("live/track.go", "track", snippet.Title("wall.go"), snippet.Mark("Track(", "Publish(")),

		d.H2("Per-session delivery"),
		demo.Card(d.H3("Ping me"),
			h.P(h.Str("One topic carries every visitor's pings. Each event names the session it is for, and the "+
				"Listen handler drops the rest. "), APIText("via.Session.ID", "ctx.Session().ID()"),
				h.Str(" is the routing key: it is stable, it is not the cookie, and it grants nothing. The session "+
					"is the unit of fan-out, not the tab.")),
			via.Child(p.Ping), "ping.go",
			demo.Try(h.Str("Ping once here. The first ping mints the session. A tab opened before then joins once it pings or "+
				"reloads; pings from other tabs cannot reach it until it sends the cookie."),
				secondTab("ping-me"),
				h.Str("Ping from the second tab: both tabs of this browser light up."),
				h.Str("Open this page in a different browser and ping: only the browser that pinged lights up."))),
		Callout(Caveat, "One process",
			h.P(h.Str("A Topic, a package-level count and every State live in this process's memory. A second replica has "+
				"its own, and a Publish on one never reaches tabs connected to the other. See "),
				h.A(h.Href(d.Href("/deploy")+"#state-across-pods"), h.Str("state across pods")), h.Str("."))),

		d.H2("Presence"),
		demo.Card(d.H3("Tabs connected"),
			h.P(API("via.Ctx.OnConnect"), h.Str(" runs once when a unit's stream opens and "),
				API("via.Ctx.OnDispose"), h.Str(" when it closes, so the pair brackets a real connection: a GET "+
					"that never connects counts for nothing. They move a count and publish it under one lock; "),
				API("via.StateTrack"), h.Str(" does the rest. Neither hook makes a unit live on its own; the "+
					"tracked State does.")),
			via.Child(p.Presence), "live_presence.go",
			demo.Try(secondTab("tabs-connected"),
				h.Str("Close that tab: the count drops as soon as its stream ends."))),

		d.H2("Frame size"),
		h.P(h.Str("A push sends the unit's whole HTML: via re-renders the unit that changed, with no diff, and Datastar "+
			"morphs it into the page. A render identical to the last sends nothing. A change costs the size of its unit, "+
			"once for every tab that shows it.")),
		h.P(h.Str("Measured on a table of 1000 rows, four cells each, about 168 bytes a row:")),
		table([]string{"Layout", "Sent per tab, per change"},
			[]h.H{h.Span(h.Str("The rows in a live "), API("via.List"), h.Str("; one row's status changes")), h.Str("168 KB")},
			[]h.H{h.Span(h.Str("The rows plain, in a root that also renders a "), API("via.State"), h.Str(" counter; the counter changes")), h.Str("168 KB")},
			[]h.H{h.Span(h.Str("The rows plain in the root; the counter in its own live "), API("via.Child"), h.Str("; the counter changes")), h.Str("about 100 bytes")},
		),
		h.P(h.Str("A live State anywhere in a unit makes the whole unit the patch. At 1000 open tabs, one change is 168 MB "+
			"against 100 KB. When a small part changes often, keep the large part plain and move the small part into its "+
			"own live Child. A live List sends every row on every change, so cap or page one that can grow.")),
		h.P(h.Str("Patches are not compressed. The "), h.A(h.Href(d.Href("/deploy")+"#reverse-proxy"), h.Str("Caddyfile")),
			h.Str(" on the deploy page leaves "), Code("text/event-stream"), h.Str(" out of "), Code("encode"),
			h.Str(", because a compressing proxy holds patches back. Size the unit instead.")),

		d.H2("Connection lifecycle"),
		table([]string{"Event", "What happens"},
			[]h.H{h.Str("Idle stream"), h.Str("A keepalive frame every 25 seconds, fixed. A failed write is how the server notices a peer that vanished without closing, and the stream then runs its disposers.")},
			[]h.H{h.Str("Stalled peer"), h.Span(h.Str("A frame write gives up after 10 seconds and the stream closes. Past "),
				API("via.WithPinnedDeadline"), h.Str(" (5 s) the tab is logged as pinned, blocked writing to a client that is not reading, and its actions answer 503."))},
			[]h.H{h.Str("Blocked handler"), h.Span(h.Str("A Tick, Listen or action handler running past "),
				API("via.WithPinnedDeadline"), h.Str(" is logged with its tab and unit type. Everything else on the tab waits behind it, keepalives included, and its actions answer 503."))},
			[]h.H{h.Str("Click before the stream connects"), h.Str("The action waits up to 2 seconds for the stream, then runs; several run in click order. Past 2 seconds it answers 410 and the tab reloads.")},
			[]h.H{h.Str("Session written elsewhere"), h.Str("A Put or Delete from any request reaches the Tick and Listen handlers of every open tab on the session: at once in this process, on the next keepalive from another process.")},
			[]h.H{h.Str("Session rotated"), h.Str("Every other open tab on the session ends its stream and reloads under the new cookie. The tab whose action rotated keeps its stream.")},
			[]h.H{h.Str("Session gone"), h.Str("On the keepalive, a stream whose session another process rotated away, deleted or let expire closes, and the tab reloads.")},
			[]h.H{h.Str("Network drop"), h.Str("The browser retries the stream under the same tab id and shows a Reconnecting banner; a click in the gap waits up to 2 seconds for it, then answers 410 and the tab reloads. " +
				"If the session cookie changed since the page loaded (a Rotate, or a session minted after the load), the retry gets a fresh id and a click in the gap answers 410.")},
			[]h.H{h.Str("Stream ends"), h.Str("A clean close, from a deploy or r.Shutdown(ctx), shows a Disconnected banner, then probes the page URL with backoff from 0.5 to 8 seconds and reloads once the server answers.")},
			[]h.H{h.Str("Action on a gone tab"), h.Span(h.Str("Waits up to 2 seconds, or "), API("via.WithPinnedDeadline"), h.Str(" if that is shorter, for the stream to come back, then answers 410 and the tab reloads once. "+
				"When via ended the stream itself (Rotate, a session gone, an aborted push), the 410 comes at once."))},
			[]h.H{h.Str("Failed action"), h.Str("A 500 from a handler panic, a 503 or a 403 leaves the banner alone: the stream is fine. Datastar fires a datastar-fetch error event on the element that made the request.")},
			[]h.H{h.Str("Too many streams"), h.Span(h.Str("A connect past "), API("via.WithMaxSSEConn"), h.Str(" (10,000 by default) answers 503. "+
				"So does an action once as many actions, at most 1024, are already waiting for their streams."))},
		),
		h.P(h.Str("A reconnect is a new connection: OnInit runs again and State starts from what it sets. After a "+
			"server restart nothing of the old process remains, so a tab gets back whatever your stores and the "+
			"session hold. Automatic reloads are capped per episode, and probing stops after about two and a half "+
			"minutes; either way the banner then offers a Reconnect button.")),
		h.P(h.Str("The banner's state is also on "), Code("<html data-via-connection>"),
			h.Str(" as online, connecting or offline, for styling your own connection UI.")),
	)
}
