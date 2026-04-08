package main

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/picocss"
)

var themes = []picocss.PicoTheme{
	picocss.PicoThemeAmber,
	picocss.PicoThemeBlue,
	picocss.PicoThemeGreen,
	picocss.PicoThemePurple,
	picocss.PicoThemeSlate,
}

var darkModes = []struct {
	Value string
	Label string
}{
	{"system", "System"},
	{"light", "Light"},
	{"dark", "Dark"},
}

func profilePage(cmp *via.Cmp) {
	darkMode := via.Signal(cmp, "system")
	theme := via.Signal(cmp, "amber")

	applyPrefs := cmp.Action(func(ctx *via.Ctx) error {
		user := via.GetSess[User](ctx)
		if user.Email == "" {
			return nil
		}
		p := Prefs{
			DarkMode: darkMode.Get(ctx),
			Theme:    theme.Get(ctx),
		}
		setPrefs(user.Email, p)
		applyDarkMode(ctx, p.DarkMode)
		ctx.MarshalAndPatchSignals(map[string]any{"_picoTheme": p.Theme})
		if p.DarkMode == "system" {
			ctx.Redirect("/profile") // reload to let picocss re-init from browser preference
		}
		return nil
	})

	cmp.View(func(ctx *via.Ctx) h.H {
		user := via.GetSess[User](ctx)
		p, _ := getPrefs(user.Email)
		if p.DarkMode == "" {
			p.DarkMode = "system"
		}

		dmOptions := make([]h.H, len(darkModes))
		for i, dm := range darkModes {
			attrs := []h.H{h.Value(dm.Value), h.Text(dm.Label)}
			if dm.Value == p.DarkMode {
				attrs = append(attrs, h.Attr("selected", "selected"))
			}
			dmOptions[i] = h.Option(attrs...)
		}

		themeOptions := make([]h.H, len(themes))
		for i, t := range themes {
			attrs := []h.H{h.Value(t.String()), h.Text(strings.Title(t.String()))}
			if t.String() == p.Theme {
				attrs = append(attrs, h.Attr("selected", "selected"))
			}
			themeOptions[i] = h.Option(attrs...)
		}

		return h.Div(
			h.H1(h.Textf("Hello, %s", user.Name)),
			h.P(h.Textf("Email: %s", user.Email)),

			h.Hr(),
			h.H3(h.Text("Preferences")),

			h.Label(h.Text("Dark mode"),
				h.Select(append(dmOptions, darkMode.Bind(), applyPrefs.OnChange())...),
			),

			h.Label(h.Text("Theme"),
				h.Select(append(themeOptions, theme.Bind(), applyPrefs.OnChange())...),
			),
		)
	})
}
