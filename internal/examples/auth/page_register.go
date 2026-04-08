package main

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func registerPage(cmp *via.Cmp) {
	name := via.Signal(cmp, "")
	email := via.Signal(cmp, "")
	password := via.Signal(cmp, "")
	errMsg := via.State(cmp, "")

	submit := cmp.Action(func(ctx *via.Ctx) error {
		n := strings.TrimSpace(name.Get(ctx))
		e := strings.TrimSpace(email.Get(ctx))
		p := password.Get(ctx)

		if err := register(n, e, p); err != nil {
			errMsg.Set(ctx, err.Error())
			return nil
		}

		via.SetSess(ctx.W, ctx.R, regFlash(true))
		ctx.Redirect("/")
		return nil
	})

	cmp.View(func(ctx *via.Ctx) h.H {
		return h.Div(
			h.H1(h.Text("Register")),
			h.If(errMsg.Get(ctx) != "", h.Article(
				h.Style("border-left: 3px solid var(--pico-del-color); color: var(--pico-del-color)"),
				h.Small(h.Text(errMsg.Get(ctx))),
			)),
			h.Label(h.Text("Name"),
				h.Input(h.Type("text"), name.Bind(), h.Placeholder("Your name")),
			),
			h.Label(h.Text("Email"),
				h.Input(h.Type("email"), email.Bind(), h.Placeholder("you@example.com")),
			),
			h.Label(h.Text("Password"),
				h.Input(h.Type("password"), password.Bind(), h.Placeholder("Password")),
			),
			h.Button(h.Text("Create account"), submit.OnClick()),
		)
	})
}
