package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
)

// Start is the page from an empty directory to a running program.
type Start struct{ page }

func NewStart(env Env) Start { return Start{page: newPage("/start", env)} }

func (p *Start) PageMeta() via.Meta {
	return p.meta("Install via, run the one-file counter, and what each part of it does.")
}

func (p *Start) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("One file, the standard library plus via, and no build step.")),

		d.H2("Install"),
		demo.Plain("go mod init example.com/counter\ngo get github.com/go-via/via"),
		h.P(h.Str("Requires Go 1.27 or newer. The public API uses generic methods ("),
			h.Code(h.Str(`ctx.Param[int]("id")`)), h.Str(", "), h.Code(h.Str("ctx.Session().Get[User]()")),
			h.Str("); on an older toolchain those read as ordinary syntax errors, not as a version complaint, "+
				"so check go version first if the program below will not compile.")),

		d.H2("The whole program"),
		h.P(h.Str("Save it as main.go.")),
		demo.Code(quickstart),

		d.H2("Run it"),
		demo.Plain("go run ."),
		h.P(h.Str("Open http://localhost:8080. A click on + POSTs to Inc, the server renders Counter again, "+
			"and the page morphs the new number in place. Nothing on this page ticks, listens or displays a "+
			"State, so it never opens a stream: every click is one request and one response.")),
		h.P(h.Str("The count is one *atomic.Int64 shared by every visitor. Another tab shows the new number on "+
			"its next click or reload, because nothing pushes it.")),

		d.H2("What each part does"),
		h.Ul(
			h.Li(h.Str("A composition is a struct. Counter holds a pointer to the count, so every request's copy shares it.")),
			h.Li(h.Str("Its View is a pure, ctx-free function that returns the markup.")),
			h.Li(h.Str("Actions are methods, wired by named method value: on.Click(c.Inc) takes the method, "+
				"so a misspelled handler does not compile.")),
			h.Li(h.Str("Handler and Mount take the composition by value, so there is no & at any call site, "+
				"and a missing or mistyped View is a compile error.")),
			h.Li(h.Str("r.Close() drains the live half once the server stops. http.Server.Shutdown does not.")),
		),
		h.P(h.Str("From here: "),
			h.A(h.Href(d.Href("/actions")), h.Str("Actions")), h.Str(" for clicks and forms, "),
			h.A(h.Href(d.Href("/signals")), h.Str("Signals")), h.Str(" for state that stays in the browser, "),
			h.A(h.Href(d.Href("/live")), h.Str("Live state")), h.Str(" for the server pushing to the tab.")),
	)
}
