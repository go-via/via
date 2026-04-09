package main

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func loginPage(cmp *via.Cmp) {
	email := via.Signal(cmp, "")
	password := via.Signal(cmp, "")
	errMsg := via.State(cmp, "")

	submit := cmp.Action(func(ctx *via.Ctx) error {
		e := strings.TrimSpace(email.Get(ctx))
		p := password.Get(ctx)

		user, err := authenticate(e, p)
		if err != nil {
			errMsg.Set(ctx, err.Error())
			return nil
		}

		via.SetSess(ctx.W, ctx.R, user)
		via.SetSess(ctx.W, ctx.R, regFlash(false))
		ctx.Redirect("/profile")
		return nil
	})

	cmp.View(func(ctx *via.Ctx) h.H {
		return h.Div(
			h.H1(h.Text("Login")),
			h.If(errMsg.Get(ctx) != "", h.Article(
				h.Style("border-left: 3px solid var(--pico-del-color); color: var(--pico-del-color)"),
				h.Small(h.Text(errMsg.Get(ctx))),
			)),
			h.Label(h.Text("Email"),
				h.Input(h.Type("email"), email.Bind(), h.Placeholder("you@example.com")),
			),
			h.Label(h.Text("Password"),
				h.Input(h.Type("password"), password.Bind(), h.Placeholder("Password")),
			),
			h.Button(h.Text("Log in"), submit.OnClick()),
			h.P(
				h.Small(
					h.Text("Don't have an account? "),
					h.A(h.Href("/register"), h.Text("Register")),
				),
			),
		)
	})
}
