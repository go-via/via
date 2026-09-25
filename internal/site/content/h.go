package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
	hs "go-via.dev/site/snippet/src/h"
)

// Helpers is the guide to the h package: how nodes, attributes, escaping and
// the Datastar helpers fit together. The per-symbol list is /reference.
type Helpers struct {
	page
	Colon demos.HColon
}

func NewHelpers(env Env) Helpers { return Helpers{page: newPage("/h", env)} }

func (p *Helpers) PageMeta() via.Meta {
	m := p.meta("The h package: element, attribute and data-* helpers for building via views in Go.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

func (p *Helpers) View() h.H {
	d := p.doc()
	ref := d.Href("/reference")
	return d.Page(
		h.P(h.Str("Package h writes markup as Go calls: one function per element, one per attribute, all returning "),
			API("h.H"), h.Str(". Values are escaped when the view renders, URLs are scheme-checked, and the "),
			Code("Data*"), h.Str(" helpers spell Datastar's attributes. Every name is listed in the "),
			h.A(h.Href(ref+"#h"), h.Str("reference")), h.Str(".")),

		d.H2("Nodes"),
		Compare("Go", snippet.Region("h/nodes.go", "nodes", snippet.Title("")),
			"Rendered HTML", snippet.Text("", hs.CardHTML)),
		h.P(h.Str("An element takes a variadic list of "), API("h.H"), h.Str(", and an "), API("h.Attr"),
			h.Str(" is an "), API("h.H"), h.Str(", so attributes and children share one call in any order. "+
				"The renderer moves attributes into the opening tag in the order given: "), API("h.ID"),
			h.Str(" above comes after the children and still lands in the tag.")),
		h.P(API("h.Str"), h.Str(" is the only text node. It takes a string or any integer or float type, so a count needs no strconv. "+
			"It does not take bool, time.Time or a fmt.Stringer: format those in Go and pass the string. "+
			"A nil "), API("h.H"), h.Str(" renders as nothing. A tag h has no function for, such as a custom element or an SVG child, goes through "),
			API("h.El"), h.Str(", which panics on an invalid tag name.")),

		d.H2("Attributes"),
		Compare("Go", snippet.Region("h/attrs.go", "attrs", snippet.Title("")),
			"Rendered HTML", snippet.Text("", hs.QtyHTML)),
		h.P(h.Str("Each attribute is a function with its name spelled once, so a misspelled one does not compile. "+
			"Boolean attributes take a bool and render a bare name when true and nothing when false: "),
			Code(`disabled="false"`), h.Str(" still disables a control, so "), API("h.Disabled"),
			h.Str(" takes the Go condition, not a string. "), API("h.Min"), h.Str(", "), API("h.Max"), h.Str(", "),
			API("h.Step"), h.Str(", "), API("h.Value"), h.Str(" and "), API("h.Width"),
			h.Str(" take any number; the counts, such as "), API("h.Rows"), h.Str(" and "), API("h.MaxLength"),
			h.Str(", take an int.")),
		h.P(API("h.Class"), h.Str(" joins its arguments with spaces and drops empty ones, so a conditional class is an empty string, "+
			"and with none left it renders no attribute at all. "), APIText("h.Aria", `h.Aria("label", …)`),
			h.Str(" is aria-label. "), API("h.RawAttr"), h.Str(" writes an attribute h has no function for.")),
		Callout(Caveat, "RawAttr checks the name, not what the value means",
			h.P(API("h.RawAttr"), h.Str(" escapes its value and panics on an invalid name, a literal "), Code("on*"),
				h.Str(" handler such as onclick, or srcdoc. A URL-bearing name (formaction, poster, srcset and the rest) gets the same scheme check as "),
				API("h.Href"), h.Str(". Any "), Code("data-*"), h.Str(" name passes, though, and Datastar runs "), Code("data-*"),
				h.Str(" values as JavaScript: see the next section.")),
		),

		d.H2("Escaping"),
		Compare("Go", snippet.Region("h/escape.go", "escape", snippet.Title("")),
			"Rendered HTML", snippet.Text("", hs.CommentHTML)),
		h.P(h.Str("Text and every attribute value are HTML-escaped at render, with no opt-out. h has no raw-HTML constructor, and "),
			API("h.H"), h.Str(" is sealed, so no package outside via can add a node type. "+
				"A string from a request or a database is safe in any "), API("h.Str"),
			h.Str(" or attribute value. Attribute and tag names are validated instead, and an invalid one panics: a name is written by you, not read from a request.")),
		h.P(h.Str("Markup you already hold as a string, such as rendered Markdown, has no way into a view. Build it with h calls instead.")),
		Callout(Warning, "Escaping stops HTML, not JavaScript",
			h.P(h.Str("The "), h.A(h.Href(d.Href("/security")+"#content-security-policy"), h.Str("CSP")), h.Str(" carries "),
				Code("'unsafe-eval'"), h.Str(" for Datastar, so it does not stop an expression. A value the user controls in "), API("h.Data"), h.Str(", "),
				API("h.DataOn"), h.Str(", "), API("h.RawAttr"), h.Str(" on a "), Code("data-*"), h.Str(" name, "), API("expr.Raw"),
				h.Str(" or the format of "), API("expr.Rawf"), h.Str(" runs as script. Pass user values through "), API("expr.Val"),
				h.Str(", which encodes them as a JavaScript literal.")),
		),

		d.H2("URL attributes"),
		Compare("Go", snippet.Region("h/url.go", "urls", snippet.Title("")),
			"Rendered HTML", snippet.Text("", hs.LinksHTML)),
		h.P(API("h.Href"), h.Str(", "), API("h.Src"), h.Str(" and "), API("h.Action"),
			h.Str(" admit relative URLs, http: and https:, and "), API("h.Href"),
			h.Str(" also admits mailto: and tel:. Any other scheme, a protocol-relative "),
			Code("//host"), h.Str(" or "), Code(`\\host`), h.Str(", and an empty string become "), Code("#"),
			h.Str(", with a warning in the log; the page still renders. Tabs and newlines are stripped and leading spaces and case ignored first, "+
				"as a browser would, so a padded "), Code("JavaScript:"), h.Str(" is refused too. "),
			API("h.RawAttr"), h.Str(" runs the same check on URL-bearing names, and "), API("via.Ctx.Redirect"),
			h.Str(" shares the policy.")),
		Callout(Caveat, "mailto: and tel: are for links only",
			h.P(h.Str("They pass on an href, typed or through "), API("h.RawAttr"), h.Str(", and nowhere else: "),
				API("h.Src"), h.Str(", "), API("h.Action"), h.Str(" and "), API("via.Ctx.Redirect"),
				h.Str(" still admit only http, https and relative URLs. There is no option to widen either list.")),
		),

		d.H2("Datastar attributes"),
		demo.Card(d.H3("One attribute, three spellings"),
			h.P(h.Str("The buttons write the same attribute three ways. Datastar splits a key at the first colon into plugin and argument, so "),
				Code("data-attr-disabled"), h.Str(" names a plugin called attr-disabled, which does not exist. Datastar ignores it with no console error.")),
			via.Child(p.Colon), "h_colon.go", demo.WireOpen(),
			demo.Try(h.Str("Clear the input: the first two buttons disable, the third stays enabled."),
				h.Str("Type a letter and watch the Wire pane: _colon__draft changes and no request appears."))),
		snippet.Text("rendered HTML", hs.ColonHTML),
		h.P(h.Str("Each typed helper writes one plugin's key, so the colon is spelled for you: "),
			API("h.DataShow"), h.Str(", "), API("h.DataText"), h.Str(", "), API("h.DataClass"), h.Str(", "),
			API("h.DataAttr"), h.Str(", "), API("h.DataStyle"), h.Str(", "), API("h.DataOn"), h.Str(", "),
			API("h.DataEffect"), h.Str(", "), API("h.DataComputed"), h.Str(", "), API("h.DataIndicator"), h.Str(", "),
			API("h.DataRef"), h.Str(" and "), API("h.DataIgnoreMorph"), h.Str(". They take an "), API("expr.Expr"),
			h.Str(" or any string type; the "), h.A(h.Href(ref+"#datastar-attributes"), h.Str("reference table")),
			h.Str(" lists the attribute each one writes.")),
		h.P(API("h.Data"), h.Str(" covers what they miss, such as a modifier suffix ("), Code(`h.Data("on:keydown__debounce.300ms", …)`),
			h.Str(") or a newer plugin, and there the colon is yours to get right. A test that reads the HTML cannot catch the hyphen: the attribute renders, and only the browser ignores it.")),
		h.P(h.Str("There is no h.DataBind or h.DataSignals: via writes those from signal fields ("), API("via.Signal.Bind"),
			h.Str(" and the root's data-signals). "), API("h.DataIndicator"), h.Str(" and "), API("h.DataRef"),
			h.Str(" take a signal's Ref and drop the $. "), API("h.DataIgnoreMorph"),
			h.Str(" only works when the old and new node both carry it; put it on a container some JavaScript owns, such as a chart or a map.")),
		Callout(Caveat, "DataOn and package on write the same attribute",
			h.P(API("h.DataOn"), h.Str(" and "), API("on.ClickCS"), h.Str(" both write "), Code("data-on:click"),
				h.Str(". Using both for one event on one element emits the attribute twice.")),
		),

		d.H2("Composition with via"),
		h.P(h.Str("Conditionals, lists and child compositions are in the root package, not in h: "), API("via.When"), h.Str(", "),
			API("via.Each"), h.Str(" and "), API("via.Child"),
			h.Str(". A child has state and a lifecycle, which h knows nothing about, and When and Each keep the view one expression. "+
				"Since a nil node renders nothing, a Go if that leaves a variable nil works too. "),
			h.A(h.Href(d.Href("/compositions")), h.Str("Compositions")), h.Str(" covers all three.")),

		d.H2("Pairing with expr"),
		snippet.Region("h/expr.go", "expr", snippet.Mark("q.Ne", "expr.Val", "expr.CopyTextOf", "expr.Class")),
		snippet.Text("rendered HTML", hs.InviteHTML),
		h.P(API("via.Signal.Ref"), h.Str(" returns the signal as an "), API("expr.Expr"), h.Str(", and its methods build on it: "),
			Code(`q.Ne("")`), h.Str(" renders "), Code(`($query !== "")`), h.Str(". "), API("expr.Val"),
			h.Str(" encodes a Go value as a JavaScript literal and escapes "), Code("@"),
			h.Str(", so the @ada in this invite URL cannot turn into a Datastar action.")),
		h.P(API("expr.CopyToClipboard"), h.Str(" writes an expression's value to the clipboard. "), API("expr.CopyTextOf"),
			h.Str(" copies the text of an element beside the handler, read at click time. "), API("expr.Class"),
			h.Str(" toggles a class on the handler element, for feedback that needs no signal. "), API("h.DataOn"),
			h.Str(" joins several statements with a semicolon. The rest of the vocabulary is on "),
			h.A(h.Href(d.Href("/signals")+"#expressions"), h.Str("Signals")), h.Str(".")),

		d.H2("Tags via owns"),
		h.P(h.Str("h has no function for html, head, body, script, style, title, base, meta, link, template, slot or data. "+
			"via writes the document shell, and every script and stylesheet must be admitted by the CSP, which via builds once per mount from "),
			API("via.WithHead"), h.Str(" and the page's "), APIText("via.Meta", "Meta.Assets"),
			h.Str(". Declare scripts and styles in "), API("via.Assets"), h.Str(", and the title and description in "), API("via.Meta"), h.Str(".")),
		h.P(API("via.Head"), h.Str("'s Raw field takes other head markup, such as icons or a viewport meta, and panics at startup if it contains "),
			Code("<script"), h.Str(" or "), Code("<style"), h.Str(": via never parses Raw, so the CSP could not admit them and the browser would block them silently.")),
		Callout(Caveat, "El does not refuse script",
			h.P(Code(`h.El("script", …)`), h.Str(" renders. An inline body is blocked by the CSP, which admits only via's own inline scripts by hash; "+
				"a same-origin src passes 'self'. Neither is a supported path: put scripts in Assets.")),
		),
	)
}
