package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
	"go-via.dev/site/snippet/src/signals"
)

// Signals is the page on client-resident state and the expr vocabulary.
type Signals struct {
	page
	Greeting demos.Greeting
	Toggle   demos.Toggle
	Seed     demos.Seed
	Computed demos.Computed
	Copy     demos.SignalsCopy
}

func NewSignals(env Env) Signals { return Signals{page: newPage("/signals", env)} }

func (p *Signals) PageMeta() via.Meta {
	m := p.meta("Browser-side state named after its Go field: Signal, SignalCS, Bind, Display, Set and the expr package.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

func (p *Signals) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("A signal is a Datastar value that lives in the browser, declared by a field of your composition. "+
			"Bind it to an input, display it, compute over it with "), API("expr.Expr"),
			h.Str(" expressions: none of that sends a request. The server writes one with a Set when it has something to say.")),

		d.H2("Where state lives"),
		h.P(h.Str("The field's type decides where a value lives and who sees it.")),
		table([]string{"Handle", "Lives on", "Scope"},
			[]h.H{API("via.Signal"), h.Str("browser, sent with each action"), h.Str("per tab")},
			[]h.H{API("via.SignalCS"), h.Str("browser only, never sent"), h.Str("per tab")},
			[]h.H{API("via.State"), h.Str("server"), h.Str("per tab, pushed when it changes")},
			[]h.H{API("via.List"), h.Str("server"), h.Str("per tab; a State of a slice, with Append, Remove and Each")},
			[]h.H{API("via.StateTrack"), h.Str("server"), h.Str("per tab, following one store every tab shares")},
			[]h.H{API("topic.Topic"), h.Str("server"), h.Str("per process; fan-out to every Ctx.Listen on it")},
		),
		h.P(h.Str("Signal and SignalCS are covered below. The server-side handles are on "),
			h.A(h.Href(d.Href("/live")), h.Str("Live state")), h.Str(".")),

		d.H2("Wire names"),
		snippet.Region("signals/names.go", "names"),
		h.P(h.Str("A signal's wire name is its Go field name with the first letter lower-cased. "+
			"A plain nested struct joins with one underscore, a child rendered with "), API("via.Child"),
			h.Str(" prefixes its field name and a double underscore, and a "), API("via.SignalCS"),
			h.Str(" name starts with an underscore. "), API("via.Signal.Ref"),
			h.Str(" returns the name as an expression, so you never spell "), Code("$chat__draft"),
			h.Str(" by hand; renaming the field renames the signal.")),
		Callout(Caveat, "Plain fields only",
			h.P(h.Str("A signal is named by its field, so it must be a plain field, through plain structs if you like. "+
				"One reached through a pointer, slice, array or map has no name, and Mount panics on it. "+
				"One behind an interface panics only when it renders or Ref is called. "+
				"Two child fields of the same type are told apart by position instead, with an "), Code("i<n>__"), h.Str(" prefix.")),
		),

		d.H2("Binding and display"),
		demo.Card(d.H3("Greeting"),
			h.P(API("via.Signal.Bind"), h.Str(" and "), API("via.Signal.Display"),
				h.Str(" are two views of one field, so they share its wire name whatever order they render in. "+
					"Typing reaches no handler: the browser updates the text on its own.")),
			via.Child(p.Greeting), "greeting.go", demo.WireOpen(),
			demo.Try(h.Str("Type a name. The greeting follows as you type."),
				h.Str("Watch the Wire pane: the signal changes with each keystroke and no request appears."))),
		h.P(h.Str("A "), Code(`via:"init=…"`),
			h.Str(" tag gives the start value as JSON, so a string is quoted. The source, then the HTML it renders:")),
		snippet.Region("signals/greeting.go", "greeting", snippet.Mark("init=")),
		snippet.Text("rendered HTML", signals.GreetingHTML),
		h.P(h.Code(h.Str("data-signals")), h.Str(" on the root declares the value, "), h.Code(h.Str("data-bind")),
			h.Str(" makes the input write it, and "), h.Code(h.Str("data-text")),
			h.Str(" shows it. The span carries the value as text too, so the first paint reads right before Datastar loads. "+
				"When an action runs, the browser posts its signals back, and "), API("via.Signal.Get"),
			h.Str(" returns the posted value of a Bound one.")),
		Callout(Warning, "A bound signal is client input",
			h.P(h.Str("Bind makes the slot writable by the client: its value is whatever the browser last posted. "+
				"Never base an authorization decision on it. A Display-only signal is not writable: via ignores a posted value for it.")),
		),

		d.H2("Client-only signals"),
		demo.Card(d.H3("Toggle"),
			h.P(API("via.SignalCS"), h.Str(" is for UI state the server has no use for: an open panel, the active tab. "+
				"Its name starts with an underscore, which Datastar's fetch filter drops, so it never reaches a POST. "+
				"It has no Get and no Set; the tag is the only way to give it a start value.")),
			via.Child(p.Toggle), "toggle.go",
			demo.Try(h.Str("Click the button to show and hide the panel."),
				h.Str("Open the Wire pane: _toggle__open flips and no request appears. The flag and the handler both live in the browser."))),

		d.H2("Server-set signals"),
		demo.Card(d.H3("Server seed"),
			h.P(API("via.Signal.Set"), h.Str(" writes the value and declares the signal, so the View need not render it. "+
				"A Set in OnInit puts the server clock in the first paint, and the action sets it again. "+
				"The action's response carries only the signals it wrote, so an input the user is editing is not overwritten.")),
			via.Child(p.Seed), "seed.go",
			demo.Try(h.Str("Click read the clock and open the Wire pane: one POST, and a patch carrying the new signal value."))),
		h.P(h.Str("A JavaScript island gets data from Go the same way: Set a signal the island reads, and no element has to spell the value out. "+
			"Declaring is not binding: the server ignores a posted value for a Set signal unless something Binds it.")),

		d.H2("Expressions"),
		demo.Card(d.H3("Computed"),
			h.P(h.Str("Ref hands a signal to the "), h.Code(h.Str("expr")),
				h.Str(" package, and the h.Data* attributes take the expression it builds. "+
					"The sum and the class on it derive from the same two refs, and the browser re-evaluates both on every keystroke.")),
			via.Child(p.Computed), "computed.go",
			demo.Try(h.Str("Raise A or B until the sum passes 10; it turns amber."),
				h.Str("Open the Wire pane: the signals change and no request appears. A Bound signal is sent only when an action runs."))),
		h.P(h.Str("An "), API("expr.Expr"), h.Str(" goes into any "), API("h.DataShow"), h.Str("-style attribute or an "),
			API("on.ClickCS"), h.Str("-style handler. The right-hand column is what each helper produces, rendered from these calls:")),
		exprTable(),
		demo.Card(d.H3("Copy to clipboard"),
			h.P(API("expr.CopyTextOf"), h.Str(" copies the text of an element next to the button, read from the page at click time. "),
				API("expr.Class"), h.Str(" flags the button for a moment: feedback that needs no signal. "+
					"The clipboard works only in a secure context and from a click. A refused write does not reach the page; the browser logs it as an unhandled rejection.")),
			via.Child(p.Copy), "signals_copy.go",
			demo.Try(h.Str("Click Copy, then paste somewhere."),
				h.Str("Open the Wire pane: no request appears. Copying never involves the server."))),
		Callout(Caveat, "Raw is unescaped JavaScript",
			h.P(API("expr.Val"), h.Str(" and the "), h.Code(h.Str("data-signals")), h.Str(" declaration escape "),
				h.Code(h.Str("@")), h.Str(", so a user's string containing "), h.Code(h.Str("@post(")),
				h.Str(" stays a string instead of becoming a Datastar action. "), API("expr.Raw"), h.Str(" and the text of "),
				API("expr.Rawf"), h.Str(" are emitted as written: never build them from user input. "+
					"Rawf's %s arguments are the only part expr checks.")),
		),
	)
}

func exprTable() h.H {
	n, open, name := expr.Expr("$count"), expr.Expr("$_open"), expr.Expr("$name")
	a, b := expr.Expr("$a"), expr.Expr("$b")
	out := func(e expr.Expr) h.H { return h.Code(h.Str(e.String())) }
	return table([]string{"Helper", "Produces", "Notes"},
		[]h.H{APIText("via.Signal.Ref", "Ref"), out(n), h.Str("the signal; a SignalCS ref starts with $_")},
		[]h.H{API("expr.Val"), out(expr.Val("@post(")), h.Str("any JSON-encodable Go value as a literal, @ escaped")},
		[]h.H{h.Span(APIText("expr.Expr.Eq", "Eq"), h.Str(" "), APIText("expr.Expr.Ne", "Ne"), h.Str(" "),
			APIText("expr.Expr.Lt", "Lt"), h.Str(" "), APIText("expr.Expr.Le", "Le"), h.Str(" "),
			APIText("expr.Expr.Gt", "Gt"), h.Str(" "), APIText("expr.Expr.Ge", "Ge")),
			out(n.Gt(10)), h.Str("Eq and Ne are strict === and !==")},
		[]h.H{APIText("expr.Expr.Not", "Not"), out(open.Not()), h.Str("")},
		[]h.H{APIText("expr.Expr.Assign", "Assign"), out(name.Assign("")), h.Str("receiver must be a bare signal ref")},
		[]h.H{APIText("expr.Expr.Add", "Add"), out(n.Add(1)), h.Str("in place; there is no a + b")},
		[]h.H{APIText("expr.Expr.Toggle", "Toggle"), out(open.Toggle()), h.Str("in place")},
		[]h.H{h.Span(API("expr.All"), h.Str(" "), API("expr.Any")), out(expr.All(n.Gt(0), open)), h.Str("&& and ||, operands grouped")},
		[]h.H{API("expr.Do"), out(expr.Do(n.Add(1), open.Toggle())), h.Str("statements in sequence")},
		[]h.H{API("expr.Call"), out(expr.Call("console.log", n)), h.Str("a function by dotted name")},
		[]h.H{API("expr.Class"), out(expr.Class("copied", true)), h.Str("on the element the attribute sits on")},
		[]h.H{API("expr.CopyToClipboard"), out(expr.CopyToClipboard(name)), h.Str("secure context and a click only")},
		[]h.H{API("expr.CopyTextOf"), out(expr.CopyTextOf("pre")), h.Str("first match inside the element's parent")},
		[]h.H{h.Span(API("expr.Raw"), h.Str(" "), API("expr.Rawf")), out(expr.Rawf("(%s + %s)", a, b)), h.Str("text unchecked, %s args spliced as-is")},
	)
}
