package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/shell"
)

// Actions is the page on wiring clicks and form submits to Go methods.
type Actions struct {
	page
	Counter demos.Counter
	Vote    demos.Vote
	Signup  demos.Signup
}

func (p *Actions) PageMeta() via.Meta {
	return p.meta("Clicks and form submits as Go methods: via.On, via.OnArg and PostForm, and the morph that answers them.")
}

// NewActions builds the limiter once, so every request's copy of the page
// spends from the same per-session buckets.
func NewActions(origin string) Actions {
	return Actions{page: newPage("/actions", origin), Vote: demos.NewVote(demo.NewLimiter(30))}
}

// OnInit mints the session the limiter keys on.
func (p *Actions) OnInit(ctx *via.Ctx) error {
	shell.EnsureSession(ctx)
	return nil
}

func (p *Actions) View() h.H {
	return shell.Page(p.nav,
		h.P(h.Str("A click POSTs to a method on your page type, the handler mutates, and via renders the page "+
			"again and patches back only what changed.")),

		demo.Card("Counter", h.P(h.Str("via.On(\"click\", c.Dec) binds a method, not a URL you invented. "+
			"Each click posts to this child alone, and the response patches this region — the rest of the page, including the other demos, is untouched.")),
			via.Child(p.Counter), "counter.go"),

		demo.Card("Vote", h.P(h.Str("via.OnArg sends the row's own option index with the click, and the handler takes it as a typed parameter. "+
			"Only an argument the render bound is dispatchable, so a vote cannot land on a row this page never drew. "+
			"The tallies are shared with everyone reading this, hence the cap.")),
			via.Child(p.Vote), "vote.go"),

		demo.Card("Signup", h.P(h.Str("via.PostForm renders a native multipart form, so the submit is a real navigation and the handler reads fields with ctx.Request().FormValue. "+
			"Validation is server-side and the page comes back with the errors and whatever was typed. "+
			"This is the flow for sign-up, upload and anything that ends in a redirect.")),
			via.Child(p.Signup), "signup.go"),

		h.P(h.A(h.Href("/signals"), h.Str("Next: state that stays in the browser"))),
	)
}
