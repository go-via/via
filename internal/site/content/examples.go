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
	return p.meta("Four runnable via programs, from a one-action counter to a multi-page forum, each with its source and the command to run it.")
}

// exampleTree is the directory view on GitHub, since forum spans two files.
const exampleTree = "https://github.com/go-via/via/tree/main/internal/site/snippet/src/examples/"

type example struct {
	name    string
	teaches h.H
	demo    string // "/page#anchor" of the demo showing the same idea, or ""
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
			cells = append(cells, []h.H{
				h.Span(h.Strong(h.Str(e.name)), h.Br(), h.Small(append([]h.H{h.Class("ex-links")}, links...)...)),
				h.Div(h.Div(e.teaches), h.Div(Code("go run ./snippet/src/examples/"+e.name))),
			})
		}
		return table([]string{"Example", "What it teaches"}, cells...)
	}
	s := h.Str[string]
	return d.Page(
		h.P(s("Four programs under "), Code("internal/site/snippet/src/examples"), s(", each one "), Code("main"),
			s(" package with no dependency past via. Run them from "), Code("internal/site"),
			s(" in a checkout of the repo; each listens on "),
			Code(":8080"), s(", or on "), Code("VIA_ADDR"), s(" when set, so two can run side by side.")),
		snippet.Text("", "git clone https://github.com/go-via/via\ncd via/internal/site\ngo run ./snippet/src/examples/counter"),

		d.H2("First steps"),
		h.P(s("Read these two in order. The second adds what the first leaves out: state the server pushes.")),
		rows(
			example{name: "counter", demo: "/", teaches: h.Span(
				s("The whole model with nothing live: a struct, a "), Code("View"), s(" method, handler methods, and "),
				API("on.Click"), s(" wiring them. A click POSTs, the handler mutates a store, the page morphs.")),
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
		snippet.Region("examples/pulse/main.go", "meta", snippet.Title("pulse/main.go"), snippet.Mark("Inline:")),
		h.P(s("via sets "), Code("data-via-connection"), s(" on "), Code("<html>"), s(" while the stream is "+
			"connecting or offline, so one inline style is pulse's whole answer to a drop. The style is in "),
			API("via.Assets"), s(", so its sha256 is in the page's CSP.")),

		d.H2("Live"),
		rows(
			example{name: "chat", demo: "/tutorial#live-chat", teaches: h.Span(
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
			example{name: "forum", demo: "/security#sign-in-sign-out",
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
		Callout(Note, "Session key",
			h.P(s("forum passes no "), API("via.WithSessionKey"), s(", so via reads "), Code("VIA_SESSION_KEY"),
				s(". Without it via signs cookies with a random per-process key and logs a warning, so a restart "+
					"signs everyone out. Set a fixed key in a real deployment."))),
	)
}
