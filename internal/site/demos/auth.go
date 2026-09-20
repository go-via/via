package demos

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// User is what this demo keeps in the session. A session holds one value, so
// this struct is the whole of it.
type User struct{ Name string }

// Auth is a login with no middleware and no guard type: OnInit reads the
// session, and the View branches on what it found.
type Auth struct {
	user User
	in   bool
}

func (a *Auth) OnInit(ctx *via.Ctx) error { return a.OnReload(ctx) }

// OnReload re-reads after an action on this unit — sign out mutates the
// session OnInit had already read.
func (a *Auth) OnReload(ctx *via.Ctx) error {
	u, ok := ctx.Session().Get[User]()
	// The live page mints an empty session to key its rate limit by, so a
	// stored value with no name is a visitor, not a user.
	a.user, a.in = u, ok && u.Name != ""
	return nil
}

// Login answers a native form submit, so it can Redirect. It has to: the
// browser only carries the new session cookie on the request after this one.
func (a *Auth) Login(ctx *via.Ctx) {
	name := strings.TrimSpace(ctx.Request().FormValue("name"))
	if name == "" {
		name = "anonymous"
	}
	if r := []rune(name); len(r) > 24 {
		name = string(r[:24])
	}
	ctx.Session().Put(User{Name: name})
	// A pre-auth id an attacker planted must not survive the login.
	ctx.Session().Rotate()
	ctx.Redirect("/platform")
}

func (a *Auth) Logout(ctx *via.Ctx) { ctx.Session().Delete() }

func (a *Auth) View() h.H {
	if !a.in {
		return via.PostForm(a.Login,
			h.Div(h.Class("row"),
				h.Label(h.For("auth-name"), h.Str("name")),
				h.Input(h.ID("auth-name"), h.Name("name"), h.AutoComplete("off"), h.Placeholder("anything")),
				h.Button(h.Type("submit"), h.Str("sign in")),
			),
		)
	}
	return h.Div(h.Class("row"),
		h.Str("signed in as "),
		h.Strong(h.Str(a.user.Name)),
		h.Button(via.On("click", a.Logout), h.Str("sign out")),
	)
}
