package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// User represents an authenticated user.
type User struct {
	ID    string
	Name  string
	Email string
	Role  string
}

// Session handle for storing the authenticated user
var userHandle = via.NewSessionDataHandle[User]()

func main() {
	v := via.New()
	v.Config(via.Options{
		LogLvl: via.LogLvlDebug,
	})

	// Login page
	v.Page("/login", func(c *via.Composition) {
		username := via.Signal(c, "")
		password := via.Signal(c, "")
		loginError := via.Signal(c, "")

		login := via.Action(c, func(ctx *via.Context) {
			u := username.Get(ctx)
			p := password.Get(ctx)

			// Simple auth check (in real app, check against database)
			if u == "admin" && p == "secret" {
				user := User{
					ID:    "1",
					Name:  "Administrator",
					Email: "admin@example.com",
					Role:  "admin",
				}
				userHandle.Set(ctx, user)
			} else if u == "user" && p == "pass" {
				user := User{
					ID:    "2",
					Name:  "Regular User",
					Email: "user@example.com",
					Role:  "user",
				}
				userHandle.Set(ctx, user)
			} else {
				loginError.Set(ctx, "Invalid credentials")
			}
		})

		c.View(func(ctx *via.Context) h.H {
			errorMsg := loginError.Get(ctx)
			return h.Div(
				h.H1(h.Text("Login")),
				h.Label(h.Text("Username: ")),
				h.Input(h.Type("text"), h.Name("username"), username.Bind()),
				h.Br(),
				h.Label(h.Text("Password: ")),
				h.Input(h.Type("password"), h.Name("password"), password.Bind()),
				h.Br(),
				h.Button(h.Text("Login"), login.OnClick()),
				h.If(errorMsg != "", h.P(h.Text(errorMsg), h.Class("text-red"))),
				h.P(h.Text("Hint: admin/secret or user/pass")),
			)
		})
	})

	// Protected dashboard
	v.Page("/dashboard", func(c *via.Composition) {
		logout := via.Action(c, func(ctx *via.Context) {
			userHandle.Clear(ctx)
		})

		c.View(func(ctx *via.Context) h.H {
			user, ok := userHandle.Get(ctx)

			if !ok {
				return h.Div(
					h.P(h.Text("Please login")),
					h.A(h.Href("/login"), h.Text("Go to Login")),
				)
			}

			return h.Div(
				h.H1(h.Text("Dashboard")),
				h.P(h.Textf("Welcome, %s!", user.Name)),
				h.P(h.Textf("Email: %s", user.Email)),
				h.P(h.Textf("Role: %s", user.Role)),
				h.Button(h.Text("Logout"), logout.OnClick()),
			)
		})
	})

	// Protected group
	v.Group("/admin", func(g *via.Group) {
		g.Page("/settings", func(c *via.Composition) {
			c.View(func(ctx *via.Context) h.H {
				user, ok := userHandle.Get(ctx)
				ctx.TabID()
				if !ok {
					return h.Div(
						h.P(h.Text("Please login")),
						h.A(h.Href("/login"), h.Text("Go to Login")),
					)
				}

				return h.Div(
					h.H1(h.Text("Admin Settings")),
					h.P(h.Textf("Logged in as: %s (%s)", user.Name, user.Role)),
				)
			})
		})
	})

	v.Start()
}
