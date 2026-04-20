package main

import (
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/picocss"
)

func main() {
	app := via.New(
		via.WithTitle("Via Auth"),
		via.WithPlugins(picocss.Plugin(
			picocss.WithThemes([]picocss.PicoTheme{
				picocss.PicoThemeAmber,
				picocss.PicoThemeBlue,
				picocss.PicoThemeGreen,
				picocss.PicoThemePurple,
				picocss.PicoThemeSlate,
			}),
			picocss.WithDefaultTheme(picocss.PicoThemeAmber),
			picocss.WithColorClasses(),
		)),
	)

	app.Layout(func(cmp *via.Cmp) {
		logout := cmp.Action(func(ctx *via.Ctx) error {
			via.ClearSess(ctx.Writer(), ctx.Request())
			ctx.Redirect("/")
			return nil
		})

		cmp.Init(func(ctx *via.Ctx) {
			user := via.GetSess[User](ctx)
			if user.Email != "" {
				if p, ok := getPrefs(user.Email); ok {
					picocss.DarkModeSig().SetValue(ctx, p.DarkMode)
					picocss.ThemeSig().SetValue(ctx, p.Theme)
				}
			}
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			user := via.GetSess[User](ctx)
			loggedIn := user.Email != ""

			return h.Div(
				h.Nav(h.Class("container"),
					h.Ul(
						h.Li(h.A(h.Href("/"), h.Strong(h.Text("⚡ Via Auth")))),
					),
					h.Ul(
						h.Li(h.A(h.Href("/about"), h.Text("About"))),
						h.If(loggedIn, h.Li(h.A(h.Href("/profile"), h.Text("Profile")))),
						h.If(loggedIn, h.Li(h.Button(
							h.Class("outline secondary"),
							h.Text("Logout"),
							logout.OnClick(),
						))),
						h.If(!loggedIn, h.Li(h.A(h.Href("/login"), h.Text("Login")))),
						h.If(!loggedIn, h.Li(h.A(h.Href("/register"), h.Role("button"), h.Text("Register")))),
					),
				),
				h.Main(h.Class("container"), cmp.Content(ctx)),
			)
		})
	})

	// Public pages
	app.Page("/", landingPage)
	app.Page("/about", aboutPage)
	app.Page("/register", registerPage)
	app.Page("/login", loginPage)

	// Protected pages
	protected := app.Group("")
	protected.Use(requireAuth)
	protected.Page("/profile", profilePage)

	app.Start()
}

func requireAuth(w http.ResponseWriter, r *http.Request, next http.Handler) {
	user := via.GetSess[User](r)
	if user.Email == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	next.ServeHTTP(w, r)
}
