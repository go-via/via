package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
)

// Signals is the page on client-resident state and the expr vocabulary.
type Signals struct {
	page
	Greeting demos.Greeting
	Toggle   demos.Toggle
	Computed demos.Computed
	Seed     demos.Seed
}

func NewSignals(env Env) Signals { return Signals{page: newPage("/signals", env)} }

func (p *Signals) PageMeta() via.Meta {
	return p.meta("Client-resident state whose wire name is the lower-cased field name: Bind, Display, SignalCS and the expr package.")
}

func (p *Signals) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("A Signal lives in the browser, and its wire name is its field name lower-cased, prefixed by the "+
			"path of children it sits under: a Count on the page is $count, and a Draft inside an embedded Chat is "+
			"$chat__draft. Bind it to an input, display it somewhere else, compare it in an expression — none of "+
			"that is a request. The server still owns the signal when it wants to: a Set declares it and ships it.")),

		demo.Card(d.H3("Greeting"), h.P(h.Str("Bind() and Display() are two views of one field, so they resolve to the same wire name whatever order they render in. "+
			"Typing here reaches no handler and opens no connection; the page is still the plain request-and-response it was served as.")),
			via.Child(p.Greeting), "greeting.go"),

		demo.Card(d.H3("Client-only toggle"), h.P(h.Str("SignalCS is the client-resident sibling. Its wire name is _-prefixed, which Datastar's fetch filter drops, so the server never sees this flag and has nowhere to store it. "+
			"It is for UI state — a panel, the active tab — and a via:\"init=…\" tag gives it a start value.")),
			via.Child(p.Toggle), "toggle.go"),

		demo.Card(d.H3("Computed"), h.P(h.Str("Ref() hands a signal to the expr package as $a, and the h.Data* attributes take the expression it builds. "+
			"The sum and the class on it both derive from the same two refs and are evaluated by the browser on every keystroke.")),
			via.Child(p.Computed), "computed.go"),

		demo.Card(d.H3("Server seed"), h.P(h.Str("Nothing here binds or displays the signal — Set is what declares it. "+
			"OnInit's Set puts the server clock into the first paint, and the action re-Sets it. "+
			"No element spells the time out: the value rides on the region's signals and data-text reads it, which is also how a JS island is fed from Go.")),
			via.Child(p.Seed), "seed.go"),

		d.H2("Where state lives"),
		h.P(h.Str("Whether a value lives in the browser, on the server, or is shared by every tab is the field's type.")),
		table([]string{"Handle", "Lives on", "Scope"},
			[]h.H{h.Code(h.Str("via.Signal[T]")), h.Str("client and server"), h.Str("per tab, round-trips with each action")},
			[]h.H{h.Code(h.Str("via.SignalCS[T]")), h.Str("client only"), h.Str("per tab, never sent")},
			[]h.H{h.Code(h.Str("via.State[T]")), h.Str("server"), h.Str("per connection, pushed when it changes")},
			[]h.H{h.Code(h.Str("via.List[E]")), h.Str("server"), h.Str("State[[]E] with Append, Remove and Each")},
			[]h.H{h.Code(h.Str("via.StateTrack(t, load)")), h.Str("server"), h.Str("one value that every tab follows")},
			[]h.H{h.Code(h.Str("topic.Topic[T]")), h.Str("server"), h.Str("fan-out to every ctx.Listen on it")},
		),
		h.P(h.Str("The first two are this page's subject. The server-side handles are on "),
			h.A(h.Href(d.Href("/live")), h.Str("Live state")), h.Str(".")),
	)
}
