package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
)

// Tutorial builds internal/example/chat in five runnable steps.
type Tutorial struct {
	page
	Chat demos.TutorialChat
}

func NewTutorial(env Env) Tutorial {
	return Tutorial{page: newPage("/tutorial", env), Chat: demos.NewTutorialChat(demo.NewLimiter(20))}
}

func (p *Tutorial) PageMeta() via.Meta {
	m := p.meta("Build a live chat with via, from an empty main.go to a page that updates every open tab.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

func (p *Tutorial) View() h.H {
	d := p.doc()
	run := func(see ...h.H) h.H {
		return Steps(
			Step("Run it", snippet.Text("", "go run .")),
			Step("Open http://localhost:8080", see...),
		)
	}
	return d.Page(
		h.P(h.Str("Build the repo's "), h.A(h.Href(exampleTree+"chat"), Code("internal/example/chat")),
			h.Str(" from an empty main.go: a room every tab shares, a composer, a send action, a clean shutdown and a "+
				"live head-count. Each step is the whole file and it builds; highlighted lines are new or changed since "+
				"the step before. Start in a module that has via, as in "),
			h.A(h.Href(d.Href("/start")), h.Str("Getting started")), h.Str(". Stop the previous run with "),
			Kbd("Ctrl", "C"), h.Str(" before each new one.")),

		d.H2("1. The room"),
		h.P(h.Str("One topic every tab subscribes to, and a list that shows what arrives on it.")),
		snippet.Show("tutorial/step1/main.go", snippet.Title("main.go")),
		run(h.P(h.Str("A heading and nothing else yet. The browser's network panel shows one request that stays open: "+
			"the tab's SSE stream. The warning about "), API("via.WithTrustedOrigin"), h.Str(" is expected on localhost; see "),
			h.A(h.Href(d.Href("/security")), h.Str("Sessions & security")), h.Str(" before you deploy."))),
		h.P(h.Str("A "), API("topic.Topic"), h.Str(", made with "), API("topic.New"), h.Str(" and kept for the life of "+
			"the process, fans each published value out to every subscriber. "), API("via.Ctx.Listen"),
			h.Str(" in OnInit subscribes this tab once its stream opens and runs "), Code("onMessage"),
			h.Str(" on the unit's own goroutine for every publish, then pushes the re-render. "), Code("Chat"),
			h.Str(" holds a pointer to the room, so the copy each request gets shares it. "), Code("Log"),
			h.Str(" is a "), API("via.List"), h.Str(": server state, one per tab, rendered by "), API("via.List.Each"), h.Str(".")),

		d.H2("2. The view"),
		h.P(h.Str("Two inputs the browser owns: who you are and what you are typing.")),
		snippet.Show("tutorial/step2/main.go", snippet.Title("main.go"),
			snippet.Mark("Who   via.Signal", "Draft via.Signal", "c.Who.Bind()", "c.Draft.Bind()")),
		run(h.P(h.Str("Type in both inputs. The network panel stays quiet: nothing reaches the server until an action runs."))),
		h.P(h.Str("A "), API("via.Signal"), h.Str(" is a value both sides hold. "), API("via.Signal.Bind"),
			h.Str(" hands it to the browser: the input writes it, and the next action posts it with the request, where "),
			API("via.Signal.Get"), h.Str(" reads it.")),

		d.H2("3. Sending"),
		h.P(h.Str("Enter publishes the draft to every tab and empties the sender's input.")),
		snippet.Show("tutorial/step3/main.go", snippet.Title("main.go"), snippet.Mark(
			`"github.com/go-via/via/on"`, "func (c *Chat) Send", `if c.Draft.Get() == ""`, "\t\treturn",
			"c.room.bus.Publish(", `c.Draft.Set("")`, "h.Form(on.Submit(c.Send)", `h.Label(h.Str("you ")`,
			"h.Input(c.Draft.Bind()", `h.Button(h.Str("send"))`)),
		Steps(
			Step("Run it", snippet.Text("", "go run .")),
			Step("Open http://localhost:8080 in two tabs",
				h.P(h.Str("Type a line in one and press Enter. It shows in both, and the sender's input empties."))),
		),
		h.P(API("on.Submit"), h.Str(" posts "), Code("Send"), h.Str(" when the form submits, by Enter or the button, "+
			"and the page does not navigate. "), Code("Send"), h.Str(" publishes, and "),
			APIText("via.Signal.Set", `Draft.Set("")`), h.Str(" clears the composer: a signal the server writes goes back "+
				"to that tab, and only the signals the action wrote are sent, so a field still being edited is left alone. "+
				"The sender's own line comes back through Listen, like everyone else's.")),

		d.H2("4. Running it"),
		h.P(h.Str("Stop the server without waiting on the tabs that are still open.")),
		snippet.Show("tutorial/step4/main.go", snippet.Title("main.go"), snippet.Mark(
			`"cmp"`, `"context"`, `"errors"`, `"os"`, `"os/signal"`, `"time"`, "signal.NotifyContext", "defer stop()",
			"srv := &http.Server", "go func()", "srv.ListenAndServe()", "log.Fatal(err)", "}()", "<-ctx.Done()",
			"shut, cancel :=", "defer cancel()", "srv.Shutdown(shut)", "log.Print(err)")),
		Steps(
			Step("Run it with a tab open", snippet.Text("", "go run .")),
			Step("Press Ctrl-C",
				h.P(h.Str("The process exits at once. Delete the "), Code("r.Close()"), h.Str(" line and repeat: "),
					Code("Shutdown"), h.Str(" waits out its 5-second deadline, then logs "), Code("context deadline exceeded"), h.Str("."))),
		),
		h.P(h.Str("Every open tab holds a streaming response. "), APIText("via.Router.Close", "r.Close()"),
			h.Str(" ends each one the way a closed tab would, running its OnDispose; "), Code("http.Server.Shutdown"),
			h.Str(" waits for in-flight responses and never ends a stream itself, so Close goes first. "), API("via.Handler"),
			h.Str(" returns the "), APIText("via.Router", "*Router"), h.Str(" so main can reach it. Steps 1 to 3 call Close "+
				"only after ListenAndServe returns, which Ctrl-C never lets happen. "), Code("VIA_ADDR"),
			h.Str(" overrides the listen address.")),

		d.H2("5. Why it's live"),
		h.P(h.Str("Show how many tabs are connected, which needs a connection to count.")),
		snippet.Show("tutorial/step5/main.go", snippet.Title("main.go"), snippet.Mark(
			`"sync"`, "presence *topic", "mu       sync.Mutex", "online   int64", "presence: topic.New", "func (r *Room) join",
			"func (r *Room) part", "func (r *Room) add", "r.mu.", "r.online += n", "r.presence.Publish", "func (r *Room) count",
			"return r.online", "Online via.State", "c.Online.Track", "ctx.OnConnect", "ctx.OnDispose", "c.Online.Display()")),
		run(h.P(h.Str("The heading reads 1 online. Open a second tab: both read 2. Close it: back to 1."))),
		h.P(APIText("via.State.Track", "Online.Track"), h.Str(" keeps a "), API("via.State"),
			h.Str(" equal to a store: it seeds from "), Code("count"), h.Str(", seeds again once the stream has "+
				"subscribed, then applies every value on "), Code("presence"), h.Str(". OnInit runs on the plain GET and on "+
				"every action as well as on the stream, so joining there would count requests. "), API("via.Ctx.OnConnect"),
			h.Str(" and "), API("via.Ctx.OnDispose"), h.Str(" run once each, when the stream opens and when it closes. "),
			Code("add"), h.Str(" holds the mutex across the change and the publish, so two tabs joining at once cannot "+
				"publish their counts out of order.")),
		table([]string{"Line", "Why this page streams"},
			[]h.H{Code("ctx.Listen(c.room.bus, …)"), h.Str("A subscription needs somewhere to push, so the unit is live.")},
			[]h.H{h.Span(Code("c.Log.Each"), h.Str(", "), Code("c.Online.Display()")), h.Str("Rendering a List or a State marks the unit live: a later change must reach the tab.")},
			[]h.H{h.Span(Code("ctx.OnConnect"), h.Str(", "), Code("ctx.OnDispose")), h.Str("Neither makes a unit live; they run only on a unit something else made live.")},
		),
		h.P(h.Str("Every live unit on a page shares the tab's one SSE stream; actions still POST. "),
			h.A(h.Href(d.Href("/live")), h.Str("Live state")), h.Str(" covers the rest.")),
		h.P(h.Str("This file is "), Code("internal/example/chat/main.go"), h.Str(" without its comments, and a test in "+
			"this site's build fails if the two drift. A "), Code("diff"), h.Str(" shows comment lines only.")),

		d.H2("Try it here"),
		demo.Card(d.H3("Live chat"),
			h.P(h.Str("The finished chat, running on this page. It adds what a public page needs: 20 sends a minute per "+
				"client IP, names capped at 24 characters and messages at 200, a blank name posts as anon, a "+
				"whitespace-only draft is dropped, and each tab keeps the newest 50 lines. The count is tabs open on "+
				"this page. Nothing stores a message, so a reload starts empty.")),
			via.Child(p.Chat), "tutorial_chat.go", demo.WireOpen(),
			demo.Try(h.A(h.Href(d.Href("/tutorial")+"#live-chat"), h.Target("_blank"), h.Rel("noopener"),
				h.Str("Open in a second tab ↗")),
				h.Str("Send a line from one tab: Wire shows the POST, then the patch arriving on this tab's stream."),
				h.Str("Close the second tab: the count drops as soon as its stream ends."))),
		Callout(Caveat, "One process",
			h.P(h.Str("The topics and the counter live in this process's memory. A second replica is a second room, and "+
				"a publish on one never reaches tabs connected to the other. See "),
				h.A(h.Href(d.Href("/deploy")+"#state-across-pods"), h.Str("state across pods")), h.Str("."))),

		d.H2("6. Next steps"),
		h.Ul(
			h.Li(h.Str("Rooms by name: mount on "), Code("/room/{name}"), h.Str(", look the room up with "),
				API("via.Ctx.Param"), h.Str(" in OnInit, and follow its count with "), API("via.State.Track"),
				h.Str(", the form for a source that depends on the request.")),
			h.Li(h.Str("History: a tab starts empty. Keep the newest lines in the Room behind a mutex and copy them into "),
				Code("Log"), h.Str(" in "), API("via.Ctx.OnConnect"), h.Str(", which runs after Listen has subscribed, so a line "+
					"published between the two is not lost.")),
			h.Li(h.Str("Test it: "), API("vt.Serve"), h.Str(" runs the router in-process, "), API("vt.App.Connect"),
				h.Str(" opens a tab's stream and "), API("vt.Conn.Await"), h.Str(" waits for a line on it. See "),
				h.A(h.Href(d.Href("/testing")), h.Str("Testing")), h.Str(".")),
		),
	)
}
