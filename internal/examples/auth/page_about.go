package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func aboutPage(cmp *via.Cmp) {
	cmp.View(func(ctx *via.Ctx) h.H {
		return h.Div(
			h.H1(h.Text("About")),
			h.P(h.Text("This example demonstrates authentication, sessions, middleware, route groups, layouts, and preferences — all in pure Go with no JavaScript.")),
			h.H3(h.Text("Features used")),
			h.Ul(
				h.Li(h.Text("Session-based authentication (SetSess / GetSess / ClearSess)")),
				h.Li(h.Text("Route groups with middleware for protected pages")),
				h.Li(h.Text("App-level layout with conditional nav")),
				h.Li(h.Text("PicoCSS plugin with theme and dark mode preferences")),
				h.Li(h.Text("Signals for form inputs, State for error messages")),
			),
		)
	})
}
