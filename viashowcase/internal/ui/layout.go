package ui

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/picocss"
	"github.com/go-via/viashowcase/internal/assets"
	"github.com/go-via/viashowcase/internal/auth"
	"github.com/go-via/viashowcase/internal/core"
)

// Shell wraps page body in the branded picocss layout: header (wordmark,
// nav, theme picker + dark-mode toggle) and footer. Used by every page.
func Shell(ctx *via.CtxR, title string, body ...h.H) h.H {
	u, in := auth.Current(ctx)
	return h.Main(h.Class("container"),
		h.Header(h.Class("shell-head"),
			h.A(h.Href("/"), h.Class("brand"), h.Aria("label", "Signal home"),
				h.Img(h.Src(assets.Wordmark), h.Alt("Signal")),
			),
			h.Nav(h.Aria("label", "Primary"),
				h.Ul(
					h.Li(h.A(h.Href("/"), h.Text("Home"))),
					h.If(in, h.Li(h.A(h.Href("/app/profile"), h.Text(u.Display)))),
					h.If(!in, h.Li(h.A(h.Href("/login"), h.Text("Login")))),
					h.If(in, h.Li(h.A(h.Href("/logout"), h.DataOnClick("@post('/logout')"), h.Text("Logout")))),
				),
				h.Ul(
					h.Li(h.Div(h.Class("control-cluster"),
						themePicker(),
						modeToggle(),
					)),
				),
			),
		),
		// An empty title lets a page own its own heading (e.g. the home hero or a
		// section label) instead of a redundant auto-rendered <h2>.
		h.Section(append([]h.H{h.When(title != "", func() h.H { return h.H2(h.Text(title)) })}, body...)...),
		h.Footer(h.Class("shell-foot"),
			h.Small(h.Text("Signal — live audience platform built with Via")),
		),
	)
}

// bind returns the datastar signal name for a "$sig" ref (strips the $),
// usable with data-bind for two-way binding of a signal we don't own.
func bind(ref string) string { return strings.TrimPrefix(ref, "$") }

func themePicker() h.H {
	opts := make([]h.H, len(core.Themes))
	for i, t := range core.Themes {
		opts[i] = h.Option(h.Value(t), h.Text(t))
	}
	return h.Select(
		h.Aria("label", "Theme"),
		h.Data("bind", bind(picocss.ThemeRef())),
		// Persist the choice so it survives navigation (restored by the head script).
		h.Data("on:change", "localStorage.setItem('signal-theme',"+picocss.ThemeRef()+")"),
		h.Fragment(opts...),
	)
}

func modeToggle() h.H {
	// Cycle dark-mode on click; an icon glyph reflects the current value and the
	// aria-label announces what the current mode is for screen-reader users.
	ref := picocss.DarkModeRef()
	// Cycle the mode AND persist it so it survives navigation.
	cycle := ref + "=" + ref + "==='dark'?'light':(" + ref + "==='light'?'system':'dark');" +
		"localStorage.setItem('signal-mode'," + ref + ")"
	icon := ref + "==='dark'?'🌙':(" + ref + "==='light'?'☀️':'🖥️')"
	label := "'Appearance: '+" + ref + "+'. Click to change.'"
	return h.Button(
		h.Class("outline", "mode-toggle"),
		h.DataOnClick(cycle),
		h.Data("text", icon),
		h.Data("attr:aria-label", label),
		h.Aria("label", "Toggle appearance"),
	)
}
