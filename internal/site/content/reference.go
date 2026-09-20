package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/shell"
)

// branch is the tree the doc links point at, so they keep working while v0.8
// is unreleased.
const branch = "https://github.com/go-via/via/blob/release/v0.8-mainline/"

// dataHelper is one row of the h.Data* table.
type dataHelper struct{ name, attr, use string }

var dataHelpers = []dataHelper{
	{"h.DataShow(e)", "data-show", "Shows the element while the expression is truthy."},
	{"h.DataText(e)", "data-text", "Replaces the element's text with the expression's value."},
	{"h.DataClass(name, e)", "data-class:<name>", "Toggles the named class while the expression is truthy."},
	{"h.DataAttr(name, e)", "data-attr:<name>", "Sets the named attribute from the expression."},
	{"h.DataStyle(prop, e)", "data-style:<prop>", "Sets the named CSS property from the expression."},
	{"h.DataOn(event, stmts…)", "data-on:<event>", "Runs the statements on that DOM event. via.On writes the same attribute."},
	{"h.DataEffect(stmts…)", "data-effect", "Runs the statements whenever a signal they read changes."},
	{"h.DataComputed(name, e)", "data-computed:<name>", "Declares a read-only signal derived from the expression."},
	{"h.DataIndicator(sig)", "data-indicator", "Names the signal held true while a request from this element is in flight."},
	{"h.DataRef(sig)", "data-ref", "Names the signal Datastar puts this element into."},
	{"h.DataIgnoreMorph()", "data-ignore-morph", "Marks a subtree some JavaScript owns, so a live patch leaves it alone."},
}

// hook is one row of the lifecycle table.
type hook struct{ sig, use string }

var hooks = []hook{
	{"OnInit(*via.Ctx) error",
		"Method on the unit. Runs on every request that renders it — the GET, the stream connect, and each action on a page that is not streaming. Where Tick, Listen and OnConnect are registered."},
	{"OnReload(*via.Ctx) error",
		"Method on the unit. Runs after every action on it and before the response render, so a handler that mutated a store re-reads here. Skipped behind a Redirect."},
	{"ctx.OnConnect(fn)",
		"Runs once, when this unit's stream opens, after every Listen has subscribed. Valid only inside OnInit; on a unit nothing made live, it never runs."},
	{"ctx.Tick(d, fn)",
		"Runs fn every d for the life of the connection and re-renders the unit after each run. Makes the unit live. OnInit only."},
	{"ctx.Listen(topic, fn)",
		"Subscribes the unit to a topic and runs fn on the unit's own goroutine for every value, unsubscribing on disconnect. Makes the unit live. OnInit only."},
	{"ctx.Redirect(path)",
		"Navigates the browser after the current handler returns — from OnInit, OnReload, a PostForm submit or an action alike. The render that called it ships nothing."},
}

// Reference is the page pointing at the manual and the package docs.
type Reference struct{}

func (p *Reference) PageMeta() via.Meta {
	return shell.Meta("Reference", "Install, the manual and the package docs, the h.Data* attribute helpers, and the lifecycle hooks.")
}

func (p *Reference) View() h.H {
	return shell.Page("Reference", shell.Nav[6],
		h.H2(h.Str("Install")),
		h.Pre(h.Code(h.Str("go get github.com/go-via/via"))),
		h.P(h.Str("Go 1.27 or newer, standard library only, no build step.")),

		h.H2(h.Str("Links")),
		h.Ul(
			h.Li(h.A(h.Href("https://pkg.go.dev/github.com/go-via/via"), h.Str("pkg.go.dev/github.com/go-via/via")),
				h.Str(" — the package documentation.")),
			h.Li(h.A(h.Href("https://github.com/go-via/via"), h.Str("github.com/go-via/via")),
				h.Str(" — the repository.")),
			h.Li(h.A(h.Href(branch+"DOCS.md"), h.Str("DOCS.md")),
				h.Str(" — the manual: the whole model, front to back.")),
			h.Li(h.A(h.Href(branch+"AGENTS.md"), h.Str("AGENTS.md")),
				h.Str(" — the rules a coding agent working on via has to follow.")),
		),

		h.H2(h.Str("Attribute helpers")),
		h.P(h.Str("One typed helper per Datastar plugin, so the attribute key is spelled once. "+
			"Expressions come from the expr package, and h.Data(name, value) writes anything the table below misses.")),
		helperTable(),

		h.H2(h.Str("Lifecycle")),
		h.P(h.Str("Two of these are duck-typed methods on your own type and the rest are calls on the Ctx. "+
			"A method with a wrong signature panics at Mount rather than silently never running.")),
		hookTable(),
	)
}

func helperTable() h.H {
	rows := []h.H{h.Class("ref-table"), h.Tr(
		h.Th(h.Str("Helper")), h.Th(h.Str("Attribute")), h.Th(h.Str("Does")),
	)}
	for _, d := range dataHelpers {
		rows = append(rows, h.Tr(
			h.Td(h.Code(h.Str(d.name))),
			h.Td(h.Code(h.Str(d.attr))),
			h.Td(h.Str(d.use)),
		))
	}
	return h.Table(rows...)
}

func hookTable() h.H {
	rows := []h.H{h.Class("ref-table"), h.Tr(h.Th(h.Str("Hook")), h.Th(h.Str("Does")))}
	for _, k := range hooks {
		rows = append(rows, h.Tr(h.Td(h.Code(h.Str(k.sig))), h.Td(h.Str(k.use))))
	}
	return h.Table(rows...)
}
