package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/shell"
)

// Signals is the page on client-resident state and the expr vocabulary.
type Signals struct {
	Greeting demos.Greeting
	Toggle   demos.Toggle
	Computed demos.Computed
	Seed     demos.Seed
}

func (p *Signals) PageMeta() via.Meta {
	return shell.Meta(shell.NavFor("/signals"), "Client-resident state whose wire name is the lower-cased field name: Bind, Display, SignalCS and the expr package.")
}

func (p *Signals) View() h.H {
	return shell.Page(shell.NavFor("/signals"),
		h.P(h.Str("A Signal lives in the browser, and its wire name is its field name lower-cased, prefixed by the "+
			"path of children it sits under: a Count on the page is $count, and a Draft inside an embedded Chat is "+
			"$chat__draft. Bind it to an input, display it somewhere else, compare it in an expression — none of "+
			"that is a request. The server still owns the signal when it wants to: a Set declares it and ships it.")),

		demo.Card("Greeting", h.P(h.Str("Bind() and Display() are two views of one field, so they resolve to the same wire name whatever order they render in. "+
			"Typing here reaches no handler and opens no connection; the page is still the plain request-and-response it was served as.")),
			via.Child(p.Greeting), "greeting.go"),

		demo.Card("Client-only toggle", h.P(h.Str("SignalCS is the client-resident sibling. Its wire name is _-prefixed, which Datastar's fetch filter drops, so the server never sees this flag and has nowhere to store it. "+
			"It is for UI state — a panel, the active tab — and a via:\"init=…\" tag gives it a start value.")),
			via.Child(p.Toggle), "toggle.go"),

		demo.Card("Computed", h.P(h.Str("Ref() hands a signal to the expr package as $a, and the h.Data* attributes take the expression it builds. "+
			"The sum and the class on it both derive from the same two refs and are evaluated by the browser on every keystroke.")),
			via.Child(p.Computed), "computed.go"),

		demo.Card("Server seed", h.P(h.Str("Nothing here binds or displays the signal — Set is what declares it. "+
			"OnInit's Set puts the server clock into the first paint, and the action re-Sets it. "+
			"No element spells the time out: the value rides on the region's signals and data-text reads it, which is also how a JS island is fed from Go.")),
			via.Child(p.Seed), "seed.go"),

		h.P(h.A(h.Href("/live"), h.Str("Next: state the server pushes"))),
	)
}
