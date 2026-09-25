package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/shell"
	"go-via.dev/site/snippet"
)

type Examples struct{ page }

func NewExamples(env Env) Examples { return Examples{page: newPage("/examples", env)} }

func (p *Examples) PageMeta() via.Meta {
	return p.meta("Nine runnable via programs, from a one-action counter to a multi-page forum, each with its source and the command to run it.")
}

// exampleTree is the directory view on GitHub, since forum spans two files.
const exampleTree = "https://github.com/go-via/via/tree/main/internal/example/"

type example struct {
	name    string
	teaches h.H
	demo    string // "/page#anchor" of the card showing the same idea, or ""
	run     string // command override, for an example that needs env
}

func (p *Examples) View() h.H {
	d := p.doc()
	rows := func(exs ...example) h.H {
		cells := make([][]h.H, 0, len(exs))
		for _, e := range exs {
			links := []h.H{shell.ExtLink(exampleTree+e.name, h.Str("source"))}
			if e.demo != "" {
				links = append(links, h.Str(" · "), h.A(h.Href(d.Href(e.demo)), h.Str("demo")))
			}
			run := e.run
			if run == "" {
				run = "go run ./internal/example/" + e.name
			}
			cells = append(cells, []h.H{
				h.Span(h.Strong(h.Str(e.name)), h.Br(), h.Small(append([]h.H{h.Class("ex-links")}, links...)...)),
				h.Div(h.Div(e.teaches), h.Div(Code(run))),
			})
		}
		return table([]string{"Example", "What it teaches"}, cells...)
	}
	s := h.Str[string]
	return d.Page(
		h.P(s("Nine programs under "), Code("internal/example"), s(", each one "), Code("main"),
			s(" package with no dependency past via. Run them from a checkout of the repo; each listens on "),
			Code(":8080"), s(", or on "), Code("VIA_ADDR"), s(" when set, so two can run side by side.")),
		snippet.Text("", "git clone https://github.com/go-via/via\ncd via\ngo run ./internal/example/counter"),

		d.H2("First steps"),
		h.P(s("Read these three in order. Each adds one idea to the one before.")),
		rows(
			example{name: "counter", demo: "/actions#counter", teaches: h.Span(
				s("The whole model with nothing live: a struct, a "), Code("View"), s(" method, handler methods, and "),
				API("on.Click"), s(" wiring them. A click POSTs, the handler mutates a store, the page morphs.")),
			},
			example{name: "greeting", demo: "/signals#greeting", teaches: h.Span(
				s("A "), API("via.Signal"), s(": state in the browser. "), API("via.Signal.Bind"), s(" and "),
				API("via.Signal.Display"), s(" share one wire name, so typing updates the text with no request. "),
				API("via.Signal.Ref"), s(" feeds "), API("h.DataShow"), s(".")),
			},
			example{name: "pulse", demo: "/live#pulse", teaches: h.Span(
				s("A "), API("via.State"), s(": state on the server, pushed over SSE by "), API("via.Ctx.Tick"),
				s(" in OnInit. Its "), API("via.Meta"), s(" styles the page off "), Code("data-via-connection"),
				s(" when the stream drops.")),
			},
		),
		snippet.Region("examples/counter/main.go", "model", snippet.Title("counter/main.go"), snippet.Mark("on.Click")),
		h.P(s("The store is a plain dependency held by pointer, so every tab reads the same count; a second tab "+
			"sees a change on its next click or reload, since nothing in counter streams.")),

		d.H2("Live"),
		h.P(s("Pick by what you need to push.")),
		rows(
			example{name: "shared", demo: "/live#shared-counter", teaches: h.Span(
				s("An app-owned value every tab follows. "), API("via.StateTrack"),
				s(" on the field literal follows a "), API("topic.Topic"), s(", so the page has no OnInit at all.")),
			},
			example{name: "feed", demo: "/live#broadcast-feed", teaches: h.Span(
				API("via.Ctx.Listen"), s(" on a "), API("topic.Topic"),
				s(": a server goroutine publishes and every connected tab receives each value, not only the latest.")),
			},
			example{name: "dashboard", demo: "/islands#sparkline-on-a-clock", teaches: h.Span(
				s("Several live units on one stream, each patching only itself. A canvas island fed by "),
				API("via.Signal.Set"), s(" on a signal the View never binds, and a client-only "), API("via.SignalCS"),
				s(" seeded by a "), Code(`via:"init=..."`), s(" tag.")),
			},
			example{name: "chat", demo: "/live#tabs-connected", teaches: h.Span(
				s("Both topic shapes: "), API("via.Ctx.Listen"), s(" appends each message to a "), API("via.List"),
				s(", "), API("via.State.Track"), s(" follows the head-count. "), API("via.Ctx.OnConnect"), s(" and "),
				API("via.Ctx.OnDispose"), s(" count presence; "), API("via.Router.Close"), s(" runs before "),
				Code("Shutdown"), s(".")),
			},
		),
		snippet.Region("examples/chat/main.go", "live", snippet.Title("chat/main.go"),
			snippet.Mark("ctx.Listen", "Track(", "ctx.OnConnect", "ctx.OnDispose")),
		h.P(s("The "), API("via.List"), s(" is per tab and there is no history: a tab that joins late "+
			"starts empty and sees only what is published after its stream subscribes.")),

		d.H2("Apps"),
		rows(
			example{name: "poll", demo: "/actions#vote", teaches: h.Span(
				s("A list that re-sorts on every render. "), API("on.WithArg"),
				s(" carries each row's id, so a vote lands on the option clicked, wherever it now sits. "),
				API("h.DataAttr"), s(" disables the add button while the draft is empty.")),
			},
			example{name: "forum", demo: "/security#sign-in-sign-out",
				run: "VIA_SESSION_KEY=$(head -c32 /dev/urandom | xxd -p -c64) go run ./internal/example/forum",
				teaches: h.Span(
					API("via.NewRouter"), s(" and "), API("via.Mount"), s(" across five pages: sessions, "),
					API("via.PostForm"), s(" with "), API("via.Ctx.Redirect"), s(", "), API("via.Ctx.Param"),
					s(", an avatar upload, and "), API("via.WithErrorPage"), s(".")),
			},
		),
		snippet.Region("examples/forum/main.go", "thread", snippet.Title("forum/main.go"),
			snippet.Mark("ctx.Session().Get", "ctx.Param", "OnReload(ctx *via.Ctx)")),
		h.P(s("A page guards itself in OnInit: no session, redirect. OnReload re-reads the store after an action, "+
			"so a reply is in the render that answers it without a redirect.")),
		Callout(Note, "Needs a key",
			h.P(s("forum refuses to start without "), Code("VIA_SESSION_KEY"),
				s(": the cookie signing key must outlive the process, or a restart signs everyone out. Keep the "+
					"key fixed across restarts in a real deployment."))),
	)
}
