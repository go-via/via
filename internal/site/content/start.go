package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
)

// Start is the page from an empty directory to a running program.
type Start struct {
	page
	Counter demos.StartCounter
}

func NewStart(env Env) Start {
	return Start{page: newPage("/start", env), Counter: demos.NewStartCounter(demo.NewLimiter(30))}
}

func (p *Start) PageMeta() via.Meta {
	m := p.meta("Install via, run a one-file counter, then make it live so every tab follows the same count.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

func (p *Start) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("From an empty directory to a counter every open tab follows: one file, the standard library plus via, no build step.")),

		d.H2("Run a counter"),
		Steps(
			Step("Check your Go version",
				snippet.Text("", "go version"),
				h.P(h.Str("via needs Go 1.27 or newer. Its API uses generic methods ("),
					APIText("via.Ctx.Param", `ctx.Param[int]("id")`), h.Str("); an older toolchain reports those as "+
						"syntax errors, not as a version mismatch.")),
			),
			Step("Create a module",
				snippet.Text("", "go mod init example.com/counter\ngo get github.com/go-via/via"),
			),
			Step("Write main.go", h.ID("program"),
				snippet.Show("counter/main.go"),
			),
			Step("Run it",
				snippet.Text("", "go run ."),
				h.P(h.Str("Open http://localhost:8080 and click +. The button POSTs to "), Code("Inc"),
					h.Str(", via renders "), Code("Counter"), h.Str(" again, and the page morphs the new number in place.")),
			),
			Step("Open a second tab",
				h.P(h.Str("Click + in one tab. The other keeps the old number until its own next click or a reload: "+
					"the count is shared, but nothing on this page pushes, so every click is one request and one response.")),
			),
		),

		d.H2("The parts"),
		h.Ul(
			h.Li(h.Str("A "), h.A(h.Href(d.Href("/compositions")), h.Str("composition")),
				h.Str(" is a struct. "), Code("Counter"), h.Str(" holds a pointer to the count, so every request's copy shares it.")),
			h.Li(h.Str("Its "), Code("View"), h.Str(" is a pure, ctx-free method that returns the markup.")),
			h.Li(h.Str("An action is a method taking "), API("via.Ctx"), h.Str(". "), API("on.Click"),
				h.Str(" takes the method value "), Code("c.Inc"), h.Str(", so a misspelled handler does not compile.")),
			h.Li(API("via.Handler"), h.Str(" and "), API("via.Mount"),
				h.Str(" take the composition by value: no "), Code("&"), h.Str(" at the call site, and a missing or mistyped "),
				Code("View"), h.Str(" is a compile error.")),
			h.Li(APIText("via.Router.Close", "r.Close()"), h.Str(" drains the live half once the server stops. "),
				Code("http.Server.Shutdown"), h.Str(" does not.")),
		),

		d.H2("Make it live"),
		snippet.Show("start/main.go", snippet.Mark(
			"moved = topic.New", "moved.Publish(count)", "via.State[int64]", "c.N.Display()", "via.StateTrack(")),
		h.P(h.Str("The count moves to a package store and a "), API("topic.Topic"), h.Str(" announces each change. Rendering a "),
			API("via.State"), h.Str(" makes the unit live: each tab opens one SSE stream and every publish reaches it.")),
		h.P(Code("add"), h.Str(" holds the mutex across the change and the "), API("topic.Topic.Publish"),
			h.Str(", so values go out in the order the count moved. "), API("via.StateTrack"),
			h.Str(" keeps the State equal to the store, and "), API("via.State.Display"), h.Str(" renders it.")),

		demo.Card(d.H3("Two tabs, one count"),
			h.P(h.Str("The program above, running. Open this page in a second tab and click there: "+
				"this card's "), h.A(h.Href(d.Href("/glossary#wire-pane")), h.Str("Wire pane")), h.Str(" shows the patch arriving on this tab's stream with no POST from it. "+
				"Clicks are capped at 30 a minute per client.")),
			via.Child(p.Counter), "start_counter.go", demo.WireOpen(),
			demo.Try(h.Str("Open this page in a second tab, click + there, and come back."),
				h.Str("Click + here: the POST answers 204 and the new number arrives on the stream."))),

		Callout(Caveat, "One process",
			h.P(h.Str("The count and the topic live in this process's memory. Two replicas behind a load balancer "+
				"each keep their own count, and a publish on one never reaches tabs connected to the other."))),

		d.H2("What happened"),
		table([]string{"Request", "What via does"},
			[]h.H{Code("GET /"), h.Str("Renders Counter to HTML. The View displayed a State, so the page also opens an SSE stream for this tab.")},
			[]h.H{Code("POST"), h.Span(h.Str("A click runs "), Code("Inc"), h.Str(" on this tab's Counter, which publishes the new count."))},
			[]h.H{Code("204"), h.Str("The POST's answer carries no body: with a live unit on the page, the change travels on the stream instead.")},
			[]h.H{h.Str("SSE patch"), h.Span(h.Str("Every tab's State receives the value through "), API("via.Ctx.Listen"),
				h.Str(", re-renders its Counter, and via pushes the changed element down that tab's stream."))},
		),

		d.H2("Try this"),
		h.Ul(
			h.Li(h.Str("Add a "), Code("Reset"), h.Str(" method that stores 0 and publishes it, and a third button with "),
				Code("on.Click(c.Reset)"), h.Str(". Every tab drops to 0.")),
			h.Li(h.Str("Rename "), Code("c.Inc"), h.Str(" to "), Code("c.Increment"), h.Str(" in View only. "), Code("go build"),
				h.Str(" stops with "), Code("c.Increment undefined (type *Counter has no field or method Increment)"),
				h.Str(": an action is a method value, not a string.")),
		),
	)
}
