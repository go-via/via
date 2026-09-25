package demos

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// SessionUser is the session's one value. Cookie and Retired exist only so
// the demo can show what each Rotate did: via never exposes the cookie id
// except as Rotate's return value.
type SessionUser struct {
	Name    string
	Cookie  string
	Retired string
}

// SecurityAuth signs in with a native form and signs out with an action,
// rotating the session id on both.
type SecurityAuth struct {
	user SessionUser
	sid  string
}

func (a *SecurityAuth) OnInit(ctx *via.Ctx) error { return a.load(ctx) }

// Logout rewrites the session OnInit already read.
func (a *SecurityAuth) OnReload(ctx *via.Ctx) error { return a.load(ctx) }

func (a *SecurityAuth) load(ctx *via.Ctx) error {
	a.user, _ = ctx.Session().Get[SessionUser]()
	a.sid = ctx.Session().ID()
	return nil
}

// Login is a native submit, so it can Redirect: the browser sends the new
// cookie only on the request after this one.
func (a *SecurityAuth) Login(ctx *via.Ctx) {
	name := strings.TrimSpace(ctx.Request().FormValue("name"))
	if name == "" {
		name = "anonymous"
	}
	if r := []rune(name); len(r) > 24 {
		name = string(r[:24])
	}
	// Rotate before the write: an id planted before the login never holds
	// the signed-in value.
	cookie := ctx.Session().Rotate()
	ctx.Session().Put(SessionUser{Name: name, Cookie: cookie, Retired: a.user.Cookie})
	mount, _, _ := strings.Cut(ctx.Request().URL.Path, "/_via/")
	ctx.Redirect(mount)
}

// Logout is an auth-state change too, so it rotates: a cookie captured while
// signed in resolves to nothing afterwards.
func (a *SecurityAuth) Logout(ctx *via.Ctx) {
	cookie := ctx.Session().Rotate()
	ctx.Session().Put(SessionUser{Cookie: cookie, Retired: a.user.Cookie})
}

// short prints enough of a 128-bit id to tell two apart and keeps the rest
// out of the DOM, where the cookie's HttpOnly flag would not protect it.
func short(id string) string {
	if id == "" {
		return "none"
	}
	return id[:min(8, len(id))] + "…"
}

func (a *SecurityAuth) ids() h.H {
	return h.Ul(h.Class("reflist"),
		h.Li(h.Str("cookie id: "), h.Code(h.Str(short(a.user.Cookie)))),
		h.Li(h.Str("retired by the last Rotate: "), h.Code(h.Str(short(a.user.Retired)))),
		h.Li(h.Str("Session.ID(): "), h.Code(h.Str(short(a.sid)))),
	)
}

func (a *SecurityAuth) View() h.H {
	if a.user.Name == "" {
		return h.Div(
			via.PostForm(a.Login,
				h.Div(h.Class("row"),
					h.Label(h.For("auth-name"), h.Str("name")),
					h.Input(h.ID("auth-name"), h.Name("name"), h.AutoComplete("off"), h.Placeholder("anything")),
					h.Button(h.Type("submit"), h.Str("sign in")),
				),
			),
			a.ids(),
		)
	}
	return h.Div(
		h.Div(h.Class("row"),
			h.Str("signed in as "),
			h.Strong(h.Str(a.user.Name)),
			h.Button(on.Click(a.Logout), h.Str("sign out")),
		),
		a.ids(),
	)
}
