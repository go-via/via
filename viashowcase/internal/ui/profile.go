package ui

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/plugins/picocss"
	"github.com/go-via/viashowcase/internal/auth"
	"github.com/go-via/viashowcase/internal/core"
)

// Profile is the host-only account page: editable display name, avatar upload,
// and persisted theme + dark-mode preference.
type Profile struct {
	Avatar via.File      `via:"avatar"`
	Name   via.SignalStr `via:"display"`
	Theme  via.SignalStr `via:"theme"`
	Mode   via.SignalStr `via:"mode"`
}

// OnInit loads the saved preference into the bound signals and reflects
// it into the live picocss client signals.
func (p *Profile) OnInit(ctx *via.Ctx) error {
	u, ok := auth.Current(ctx)
	if !ok {
		return nil
	}
	theme, mode, err := Deps.DB.Pref(context.Background(), u.ID)
	if err != nil {
		theme, mode = "amber", "dark"
	}
	theme, mode = core.ResolveTheme(theme), core.ValidMode(mode)
	p.Theme.Write(ctx, theme)
	p.Mode.Write(ctx, mode)
	p.Name.Write(ctx, u.Display)
	p.apply(ctx, theme, mode)
	return nil
}

// SaveName persists a new display name and refreshes the session so the nav and
// header reflect it immediately. Empty input is ignored (restores the current
// name); the name is capped to keep it sane.
func (p *Profile) SaveName(ctx *via.Ctx) error {
	u, ok := auth.Current(ctx)
	if !ok {
		return nil
	}
	name := core.NormalizeText(p.Name.Read(ctx), 60)
	if name == "" {
		p.Name.Write(ctx, u.Display)
		return nil
	}
	if err := Deps.DB.SetDisplay(context.Background(), u.ID, name); err != nil {
		return err
	}
	auth.SetCurrent(ctx, auth.SessionUser{ID: u.ID, Email: u.Email, Display: name})
	return nil
}

// SaveTheme persists the chosen theme + mode and reflects it client-side.
func (p *Profile) SaveTheme(ctx *via.Ctx) error {
	u, ok := auth.Current(ctx)
	if !ok {
		return nil
	}
	theme, mode := core.ResolveTheme(p.Theme.Read(ctx)), core.ValidMode(p.Mode.Read(ctx))
	if err := Deps.DB.SetPref(context.Background(), u.ID, theme, mode); err != nil {
		return err
	}
	p.apply(ctx, theme, mode)
	return nil
}

// apply mirrors the persisted pref into the picocss runtime the same way
// the plugin's own boot script does (the picocss signals are client-only
// "_" signals, so we drive the DOM directly).
func (p *Profile) apply(ctx *via.Ctx, theme, mode string) {
	if mode == "system" {
		ctx.ExecScript("document.documentElement.setAttribute('data-theme'," +
			"window.matchMedia('(prefers-color-scheme:dark)').matches?'dark':'light')")
	} else {
		ctx.ExecScript(fmt.Sprintf("document.documentElement.setAttribute('data-theme',%q)", mode))
	}
	ctx.ExecScript(fmt.Sprintf(
		"document.getElementById('_picoThemeLink')?.setAttribute('href',document.getElementById('_picoThemeLink').getAttribute('href').replace(/[^/]*$/,%q))",
		theme))
	// Persist so the choice survives navigation (restored by the head script),
	// matching how the shell's live picker persists it.
	ctx.ExecScript(fmt.Sprintf("localStorage.setItem('signal-theme',%q);localStorage.setItem('signal-mode',%q)", theme, mode))
}

// Upload saves the posted avatar bytes to Postgres, then redirects back.
func (p *Profile) Upload(ctx *via.Ctx) error {
	u, ok := auth.Current(ctx)
	if ok && p.Avatar.Present() {
		data, err := p.Avatar.Bytes()
		if err != nil {
			return err
		}
		ct := http.DetectContentType(data)
		// accept="image/*" is client-side only; a crafted upload could smuggle
		// HTML that avatarHandler would serve inline (stored XSS). Reject
		// anything that isn't a safe raster image.
		if !core.IsAllowedAvatarType(ct) {
			ctx.Toast("Avatar must be a PNG, JPEG, GIF or WebP image.")
			http.Redirect(ctx.Writer(), ctx.Request(), "/app/profile", http.StatusSeeOther)
			return nil
		}
		if err := Deps.DB.SetAvatar(context.Background(), u.ID, ct, data); err != nil {
			return err
		}
	}
	http.Redirect(ctx.Writer(), ctx.Request(), "/app/profile", http.StatusSeeOther)
	return nil
}

func (p *Profile) View(ctx *via.CtxR) h.H {
	u, _ := auth.Current(ctx)
	themeOpts := make([]h.H, len(core.Themes))
	for i, t := range core.Themes {
		themeOpts[i] = h.Option(h.Value(t), h.Text(t))
	}
	modes := []string{"system", "dark", "light"}
	modeOpts := make([]h.H, len(modes))
	for i, m := range modes {
		modeOpts[i] = h.Option(h.Value(m), h.Text(m))
	}
	return Shell(ctx, "Profile",
		h.Article(
			h.Div(h.Class("profile-head"),
				h.Img(h.Src("/avatar/"+u.ID), h.Class("avatar"),
					h.Alt(u.Display+"'s avatar"), h.Width("112")),
				h.Div(h.Class("who"),
					h.H3(h.Text(u.Display)),
					h.P(h.Text(u.Email)),
				),
			),
		),
		h.Article(h.Class("form-card"),
			h.H3(h.Text("Display name")),
			h.Label(h.Text("Name"),
				h.Input(h.Type("text"), p.Name.Bind(), h.MaxLength(60),
					h.Placeholder("Your name"), h.AutoComplete("name")),
			),
			h.Button(h.Text("Save name"), on.Click(p.SaveName)),
		),
		h.Article(h.Class("form-card"),
			h.H3(h.Text("Avatar")),
			// Plain multipart form: file bytes can't ride a Datastar JSON @post.
			h.Form(
				h.Method("POST"),
				h.Action("/_action/Upload"),
				h.Attr("enctype", "multipart/form-data"),
				h.Input(h.Type("hidden"), h.Name("via_tab"), h.Value(ctx.ID())),
				h.Label(h.Text("Choose an image"),
					h.Input(h.Type("file"), h.Name("avatar"), h.Attr("accept", "image/*"), h.Required()),
					h.Small(h.Class("hint"), h.Text("Square images look best. PNG, JPG, GIF or WebP."))),
				h.Button(h.Type("submit"), h.Text("Upload avatar")),
			),
		),
		h.Article(h.Class("form-card"),
			h.H3(h.Text("Appearance")),
			h.Label(h.Text("Theme"),
				h.Select(p.Theme.Bind(), on.Change(p.SaveTheme), h.Fragment(themeOpts...)),
			),
			h.Label(h.Text("Mode"),
				h.Select(p.Mode.Bind(), on.Change(p.SaveTheme), h.Fragment(modeOpts...)),
				h.Small(h.Class("hint"), h.Text("System follows your device's light/dark setting.")),
			),
			h.P(h.Small(h.Text("Live theme: ")),
				h.Span(h.Class("nick-chip"), h.Data("text", picocss.ThemeRef()))),
		),
	)
}
