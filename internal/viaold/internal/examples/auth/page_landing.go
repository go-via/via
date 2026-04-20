package main

import (
	"github.com/go-via/via/internal/viaold"
	"github.com/go-via/via/internal/viaold/h"
)

func landingPage(cmp *via.Cmp) {
	cmp.View(func(ctx *via.Ctx) h.H {
		flash := via.GetSess[regFlash](ctx)
		user := via.GetSess[User](ctx)
		loggedIn := user.Email != ""

		return h.Div(
			h.If(bool(flash), h.Article(
				h.Style("border-left: 3px solid var(--pico-ins-color); color: var(--pico-ins-color)"),
				h.H4(h.Text("Account created!")),
				h.P(
					h.Text("You can now "),
					h.A(h.Href("/login"), h.Text("log in")),
					h.Text("."),
				),
			)),

			h.H1(h.Text("Zero JavaScript. Full power.")),
			h.P(h.Text("Via is a reactive Go web engine that doesn't need you to write a single line of JavaScript. Yes, really. No npm, no node_modules, no build step, no webpack config that makes you question your career choices.")),

			h.If(loggedIn, h.P(
				h.Text("Welcome back, "+user.Name+"! "),
				h.A(h.Href("/profile"), h.Text("Go to your profile")),
				h.Text("."),
			)),

			h.Hr(),

			h.H2(h.Text("What you get")),
			h.Div(
				h.Article(
					h.H4(h.Text("Server-side reactivity")),
					h.P(h.Text("State lives on the server. Changes stream to the browser over SSE. Your Go code is the single source of truth — not some tangled mess of client state, server state, and prayers.")),
				),
				h.Article(
					h.H4(h.Text("Type-safe everything")),
					h.P(h.Text("Generics for state and signals. If it compiles, it probably works. If it doesn't compile, the error message is in Go, not a 47-line TypeScript novel about why 'undefined is not assignable to type never'.")),
				),
				h.Article(
					h.H4(h.Text("One binary to rule them all")),
					h.P(h.Text("go build. scp. Done. No Docker required (but sure, put it in Docker if that's your thing). No CI pipeline that takes longer than your lunch break.")),
				),
			),

			h.Hr(),

			h.H2(h.Text("What you don't get")),
			h.Ul(
				h.Li(h.Text("A node_modules folder heavier than your production database")),
				h.Li(h.Text("A package.json with 847 dependencies, 3 of which you actually chose")),
				h.Li(h.Text("A Next.js app that needs 2GB of RAM to serve a todo list")),
				h.Li(h.Text("Existential dread every time you run npm audit")),
			),

			h.If(!loggedIn, h.Article(
				h.H3(h.Text("Still reading?")),
				h.P(h.Text("Register an account and try the profile preferences. It's all server-rendered, real-time, and there's not a single useEffect in sight.")),
				h.A(h.Href("/register"), h.Role("button"), h.Text("Convince me")),
			)),
		)
	})
}
