package ui

import (
	"context"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/viashowcase/internal/auth"
	"github.com/go-via/viashowcase/internal/core"
)

// Login is the host sign-in form.
type Login struct {
	Email    via.SignalStr `via:"email"`
	Password via.SignalStr `via:"password"`
	Err      via.StateTabStr
}

func (p *Login) Submit(ctx *via.Ctx) error {
	email := core.NormalizeEmail(p.Email.Read(ctx))
	u, hash, err := Deps.DB.UserByEmail(context.Background(), email)
	if err != nil || !auth.Verify(hash, p.Password.Read(ctx)) {
		p.Err.Write(ctx, "Invalid email or password")
		return nil
	}
	auth.Login(ctx, auth.SessionUser{ID: u.ID, Email: u.Email, Display: u.Display})
	ctx.Redirect("/")
	return nil
}

func (p *Login) View(ctx *via.CtxR) h.H {
	bad := p.Err.Read(ctx) != ""
	return Shell(ctx, "Log in",
		h.Article(h.Class("form-card"),
			h.When(bad, func() h.H { return h.Div(h.Class("err"), h.Role("alert"), p.Err.Text(ctx)) }),
			h.Form(
				h.Label(h.Text("Email"),
					h.Input(h.Type("email"), p.Email.Bind(), h.Placeholder("you@example.com"),
						h.Attr("autocomplete", "email"), h.Attr("autofocus"), h.Attr("required"),
						h.If(bad, h.Attr("aria-invalid", "true")))),
				h.Label(h.Text("Password"),
					h.Input(h.Type("password"), p.Password.Bind(), h.Placeholder("Your password"),
						h.Attr("autocomplete", "current-password"), h.Attr("required"),
						h.If(bad, h.Attr("aria-invalid", "true")))),
				h.Button(h.Text("Log in"), on.Click(p.Submit)),
			),
			h.P(h.Class("form-foot"), h.Text("No account? "), h.A(h.Href("/signup"), h.Text("Sign up"))),
		),
	)
}

// Signup creates a host account, logs in, and redirects home.
type Signup struct {
	Email    via.SignalStr `via:"email"`
	Password via.SignalStr `via:"password"`
	Display  via.SignalStr `via:"display"`
	Err      via.StateTabStr
}

func (p *Signup) Submit(ctx *via.Ctx) error {
	email := core.NormalizeEmail(p.Email.Read(ctx))
	display := strings.TrimSpace(p.Display.Read(ctx))
	pw := p.Password.Read(ctx)
	if email == "" || display == "" || pw == "" {
		p.Err.Write(ctx, "All fields are required")
		return nil
	}
	if !core.PasswordLongEnough(pw) {
		p.Err.Write(ctx, "Password must be at least 8 characters")
		return nil
	}
	hash, err := auth.Hash(pw)
	if err != nil {
		return err
	}
	u, err := Deps.DB.CreateUser(context.Background(), email, hash, display)
	if err != nil {
		p.Err.Write(ctx, "Email already registered")
		return nil
	}
	auth.Login(ctx, auth.SessionUser{ID: u.ID, Email: u.Email, Display: u.Display})
	ctx.Redirect("/")
	return nil
}

func (p *Signup) View(ctx *via.CtxR) h.H {
	bad := p.Err.Read(ctx) != ""
	return Shell(ctx, "Create your account",
		h.Article(h.Class("form-card"),
			h.When(bad, func() h.H { return h.Div(h.Class("err"), h.Role("alert"), p.Err.Text(ctx)) }),
			h.Form(
				h.Label(h.Text("Display name"),
					h.Input(h.Type("text"), p.Display.Bind(), h.Placeholder("Ada Lovelace"),
						h.Attr("autocomplete", "name"), h.Attr("autofocus"), h.Attr("required"),
						h.If(bad, h.Attr("aria-invalid", "true")))),
				h.Label(h.Text("Email"),
					h.Input(h.Type("email"), p.Email.Bind(), h.Placeholder("you@example.com"),
						h.Attr("autocomplete", "email"), h.Attr("required"),
						h.If(bad, h.Attr("aria-invalid", "true")))),
				h.Label(h.Text("Password"),
					h.Input(h.Type("password"), p.Password.Bind(), h.Placeholder("At least 8 characters"),
						h.Attr("autocomplete", "new-password"), h.Attr("required"),
						h.If(bad, h.Attr("aria-invalid", "true"))),
					h.Small(h.Class("hint"), h.Text("Use something you don't reuse elsewhere."))),
				h.Button(h.Text("Create account"), on.Click(p.Submit)),
			),
			h.P(h.Class("form-foot"), h.Text("Have an account? "), h.A(h.Href("/login"), h.Text("Log in"))),
		),
	)
}

// Logout clears the session and redirects home. Mounted so the Shell's
// @post('/logout') resolves to this action.
type Logout struct{}

func (Logout) Submit(ctx *via.Ctx) error {
	auth.Logout(ctx)
	ctx.Redirect("/")
	return nil
}

func (Logout) View(ctx *via.CtxR) h.H { return Shell(ctx, "Logout") }
