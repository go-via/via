package demos

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Signup validates on the server. via.PostForm renders a native multipart
// form, so the submit is an ordinary navigation: the handler reads the fields
// with stdlib and the whole page is rendered again from what it left here.
//
// The errors are plain fields, not State: a navigation opens a new connection,
// which would reseed a State before the reader ever saw it. Anything that must
// outlive the submit goes in the session or a store; here it need only survive
// into the response this very request renders.
type Signup struct {
	name, email              string
	nameErr, mailErr, thanks string
}

func (s *Signup) Submit(ctx *via.Ctx) {
	s.name = strings.TrimSpace(ctx.Request().FormValue("name"))
	s.email = strings.TrimSpace(ctx.Request().FormValue("email"))
	s.nameErr, s.mailErr, s.thanks = "", "", ""

	if len([]rune(s.name)) < 2 {
		s.nameErr = "Two characters at least."
	}
	if !strings.Contains(s.email, "@") {
		s.mailErr = "That is not an address."
	}
	if s.nameErr != "" || s.mailErr != "" {
		return // no Redirect: the page comes back with the errors and the input
	}

	s.thanks = "Welcome, " + s.name + "."
	s.name, s.email = "", ""
}

func (s *Signup) View() h.H {
	return via.PostForm(s.Submit,
		h.Label(h.Str("Name"),
			h.Input(h.Name("name"), h.Value(s.name), h.AutoComplete("off")),
			h.Small(h.Class("err"), h.Str(s.nameErr)),
		),
		h.Label(h.Str("Email"),
			h.Input(h.Name("email"), h.Value(s.email), h.AutoComplete("off")),
			h.Small(h.Class("err"), h.Str(s.mailErr)),
		),
		h.Button(h.Type("submit"), h.Str("Sign up")),
		h.P(h.Class("notice"), h.Str(s.thanks)),
	)
}
