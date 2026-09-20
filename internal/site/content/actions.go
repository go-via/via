package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/shell"
)

// voteLimit is the page's cap, one bucket per session across every vote on it.
var voteLimit = demo.NewLimiter(30)

// Actions is the page on wiring clicks and form submits to Go methods.
type Actions struct {
	Counter demos.Counter
	Vote    demos.Vote
	Signup  demos.Signup
}

func (p *Actions) PageMeta() via.Meta {
	return shell.Meta("Actions", "Clicks and form submits as Go methods: via.On, via.OnArg and PostForm, and the morph that answers them.")
}

// OnInit hands the vote demo the page's limiter: via.Child copies the field,
// so the child renders and acts with whatever this set.
func (p *Actions) OnInit(ctx *via.Ctx) error {
	shell.EnsureSession(ctx)
	p.Vote.Lim = voteLimit
	return nil
}

func (p *Actions) View() h.H {
	return shell.Page("Actions", shell.Nav[1],
		h.P(h.Str("A click POSTs to a method on your page type, the handler mutates, and via renders the page again and patches back only what changed. "+
			"The three demos below are the whole vocabulary: an argless action, an action that carries a row's identity, and a native form the server validates.")),

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
