package content

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/shell"
)

type Glossary struct{ page }

func NewGlossary(env Env) Glossary { return Glossary{page: newPage("/glossary", env)} }

func (p *Glossary) PageMeta() via.Meta {
	return p.meta("The terms the via docs use, from action to wire pane: what each means and where it is taught.")
}

// glossaryTerm is one entry. Its anchor is shell.Slug(name), which other pages
// link to, so renaming a term breaks their links.
type glossaryTerm struct {
	name string
	def  []h.H
	see  []seeLink
	api  []h.H
}

// seeLink is a page section that teaches a term; href is a site path with an
// optional fragment, resolved against the build's base at render.
type seeLink struct{ label, href string }

// text names the page before the section, since a bare section title does
// not say where the link goes.
func (l seeLink) text() string {
	path, _, _ := strings.Cut(l.href, "#")
	page := shell.NavFor(path).Title
	if l.label == page {
		return page
	}
	return page + " › " + l.label
}

func glossaryTerms() []glossaryTerm {
	s := h.Str[string]
	return []glossaryTerm{
		{"Action",
			[]h.H{s("A method of a composition, "), Code("func(*via.Ctx)"), s(" or "), Code("func(*via.Ctx, V)"), s(" bound with "), API("on.WithArg"),
				s(", attached to a DOM event with package "), Code("on"), s(". The browser POSTs to it; via runs it on the server, then OnReload, then re-renders the unit and answers with the patch. It returns nothing: a failure the user can fix goes into state, and a panic is a 500.")},
			[]seeLink{{"Actions", "/actions#handling-a-click"}, {"Action signatures", "/actions#action-signatures"}},
			[]h.H{API("on.Click"), API("on.WithArg")}},
		{"Composition",
			[]h.H{s("A Go struct with a "), Code("View() h.H"), s(" method. Its fields are its state, its methods are its actions, and the optional hooks OnInit, OnReload and PageMeta opt it into lifecycle phases. "),
				API("via.Mount"), s(" serves one at a path and "), API("via.Child"), s(" renders one as a field of another; both take it by value.")},
			[]seeLink{{"A composition is a struct", "/compositions#a-composition-is-a-struct"}, {"The parts", "/start#the-parts"}},
			[]h.H{API("via.Mount"), API("via.Child")}},
		{"CSP",
			[]h.H{s("The Content-Security-Policy header via sends with each document. It is derived once per mount from the router's "), API("via.WithHead"),
				s(" assets and the page's Meta.Assets, and admits via's own inline scripts by hash. "), Code("script-src"), s(" carries a nonce, fresh per document, that Datastar compiles its expressions under, and no "), Code("'unsafe-eval'"),
				s(" unless "), API("via.WithUnsafeEval"), s(" adds it.")},
			[]seeLink{{"Content-Security-Policy", "/security#content-security-policy"}, {"Libraries from another origin", "/islands#libraries-from-another-origin"}},
			[]h.H{API("via.Assets")}},
		{"Datastar",
			[]h.H{s("The browser runtime via targets, vendored in the via module and served by the router. via emits the "), Code("data-*"),
				s(" attributes and the patches; Datastar evaluates the expressions, sends signals with each action, and morphs patches into the page. You do not write Datastar by hand, but its reconnect and morph behaviour is via's.")},
			[]seeLink{{"What you get", "/why#what-you-get"}, {"What it costs", "/why#what-it-costs"}},
			nil},
		{"expr",
			[]h.H{s("The package that composes the JavaScript expressions Datastar evaluates in the browser: a signal reference, a comparison, an assignment, a call. An "),
				API("expr.Expr"), s(" goes into an "), Code("on.*CS"), s(" binder or an "), Code("h.Data*"), s(" attribute. Everything is checked except "),
				API("expr.Raw"), s(" and the text of "), API("expr.Rawf"), s(", which are emitted verbatim.")},
			[]seeLink{{"Expressions", "/signals#expressions"}},
			[]h.H{API("expr.Expr"), API("expr.Val")}},
		{"Island",
			[]h.H{s("A container via renders once and then leaves to your JavaScript. "), API("h.DataIgnoreMorph"),
				s(" keeps later patches out of its subtree, and Go feeds it by setting signals the script reads through "), API("h.DataEffect"), s(".")},
			[]seeLink{{"The island contract", "/islands#the-island-contract"}},
			[]h.H{API("h.DataIgnoreMorph"), API("h.DataEffect")}},
		{"Listen",
			[]h.H{API("via.Ctx.Listen"), s(" subscribes a unit to a Topic: each published value runs the handler on the unit's own goroutine, then the unit re-renders and pushes. Valid only in OnInit, and it makes the unit live. The subscription is taken when the stream connects and stopped when it closes.")},
			[]seeLink{{"Topics", "/live#topics"}},
			[]h.H{API("via.Ctx.Listen")}},
		{"Live unit",
			[]h.H{s("A composition, the mounted root or a Child, that calls "), API("via.Ctx.Tick"), s(" or "), API("via.Ctx.Listen"), s(" in OnInit, or renders a "),
				API("via.State.Display"), s(" or a "), API("via.List.Each"), s(". Its instance stays in memory for the life of the tab's SSE stream, and every callback on it runs on that stream's one goroutine. A live unit may not contain another live unit.")},
			[]seeLink{{"A page is plain until a unit goes live", "/live#a-page-is-plain-until-a-unit-goes-live"}},
			nil},
		{"Meta.Assets",
			[]h.H{s("The "), Code("Assets"), s(" field of "), API("via.Meta"), s(": the scripts and styles a page loads. It decides the mount's CSP, so it must be a constant of the type: via reads it at Mount and on every document render, and panics if two readings disagree.")},
			[]seeLink{{"The island contract", "/islands#the-island-contract"}, {"Content-Security-Policy", "/security#content-security-policy"}},
			[]h.H{API("via.Meta"), API("via.Assets")}},
		{"Morph",
			[]h.H{s("How Datastar applies an element patch: it matches incoming elements to the live ones by id and updates them in place, so focus, scroll position and element identity survive. A subtree whose old and new versions both carry "),
				API("h.DataIgnoreMorph"), s(" is left alone.")},
			[]seeLink{{"The island contract", "/islands#the-island-contract"}},
			[]h.H{API("h.DataIgnoreMorph")}},
		{"OnConnect",
			[]h.H{API("via.Ctx.OnConnect"), s(" registers a function that runs once when the unit's stream opens, after every Listen has subscribed. Register it in OnInit. It does not make a unit live, so on a plain unit it never runs.")},
			[]seeLink{{"Presence", "/live#presence"}, {"Stopping a producer on disconnect", "/islands#stopping-a-producer-on-disconnect"}},
			[]h.H{API("via.Ctx.OnConnect")}},
		{"OnDispose",
			[]h.H{API("via.Ctx.OnDispose"), s(" registers a teardown function that runs when the unit's connection closes: stop a subscription, release a producer. Register it in OnInit. It runs only on a unit something else made live.")},
			[]seeLink{{"Presence", "/live#presence"}, {"Stopping a producer on disconnect", "/islands#stopping-a-producer-on-disconnect"}},
			[]h.H{API("via.Ctx.OnDispose")}},
		{"OnInit",
			[]h.H{Code("OnInit(*via.Ctx) error"), s(" runs before the View on every request that renders the unit, page or child, including each plain action. It loads request and session data into fields and registers Tick, Listen, OnConnect and OnDispose. It may "),
				API("via.Ctx.Redirect"), s(" or return "), API("via.ErrNotFound"), s(" for a 404; any other error is a 500. On a live unit it runs once, at connect, not per action.")},
			[]seeLink{{"Lifecycle hooks", "/compositions#lifecycle-hooks"}},
			nil},
		{"OnReload",
			[]h.H{Code("OnReload(*via.Ctx) error"), s(" runs after one of the unit's actions and before the render that answers it, on plain and live units alike: it re-reads what the handler wrote. It is skipped when the handler called Redirect, and Tick and Listen are no-ops inside it.")},
			[]seeLink{{"What an action body can do", "/actions#what-an-action-body-can-do"}, {"Lifecycle hooks", "/compositions#lifecycle-hooks"}},
			nil},
		{"PageMeta",
			[]h.H{Code("PageMeta() via.Meta"), s(" names the document: title, description, social cards and assets. Only the mounted root's counts. It must be pure: via reads it at Mount and on every document render, after OnInit and OnReload, and never on an SSE push.")},
			[]seeLink{{"Lifecycle hooks", "/compositions#lifecycle-hooks"}},
			[]h.H{API("via.Meta")}},
		{"Plain vs live page",
			[]h.H{s("A page streams if and only if it contains a live unit. A plain page is request and response: each GET and action POST gets a fresh copy of the mounted value, and an action's answer is the POST's own body. A live page also holds one SSE stream per tab, and its actions answer over that stream. The render that served the page decides; an action cannot make a plain page live.")},
			[]seeLink{{"A page is plain until a unit goes live", "/live#a-page-is-plain-until-a-unit-goes-live"}, {"Concurrency", "/actions#concurrency"}},
			nil},
		{"Positional child key",
			[]h.H{s("The key via gives each Child: its position among its parent's Child calls, joined to the parent's key ("), Code("0"), s(", "), Code("0-0"),
				s("). The child's container id, signal prefix and action address all derive from it, so it must be the same on every render for the life of a connection. A "),
				API("via.When"), s(" around a Child shifts its later siblings, so it may depend only on data fixed by OnInit or the field literal.")},
			[]seeLink{{"Children and keys", "/compositions#children-and-keys"}, {"Conditional children shift keys", "/compositions#conditional-children-shift-keys"}},
			[]h.H{API("via.Child")}},
		{"PostForm vs on.Submit",
			[]h.H{API("on.Submit"), s(" posts the page's signals through Datastar and the answer patches the page in place. "), API("via.PostForm"),
				s(" renders a native multipart form: the submit is a browser navigation, the handler reads "), Code("ctx.Request().FormValue"), s(" and "), Code("FormFile"),
				s(", and a Redirect answers 303. Use PostForm for sign-in, uploads and flows that end in a redirect.")},
			[]seeLink{{"Forms: on.Submit or PostForm", "/actions#forms-on-submit-or-postform"}},
			[]h.H{API("on.Submit"), API("via.PostForm")}},
		{"Ref",
			[]h.H{API("via.Signal.Ref"), s(" and "), API("via.SignalCS.Ref"), s(" return the signal's Datastar name as an "), API("expr.Expr"), s(": "), Code("$count"),
				s(" for a field "), Code("Count"), s(", "), Code("$_open"), s(" for a SignalCS "), Code("Open"),
				s(". Use it in client expressions and hand-written "), Code("data-*"), s(" attributes. A signal reached through a pointer, slice, array or map has no name, and Ref on it panics. State has no Ref.")},
			[]seeLink{{"Expressions", "/signals#expressions"}, {"Wire names", "/signals#wire-names"}},
			[]h.H{API("via.Signal.Ref"), API("via.SignalCS.Ref")}},
		{"Session",
			[]h.H{s("The per-browser value bag behind "), API("via.Ctx.Session"), s(": one JSON value per session, resolved from a signed cookie and created on the first write, so an app that stores nothing sets no cookie. Expiry is an idle window, 24 hours by default, that slides with use. Ids never rotate on their own: call "),
				API("via.Session.Rotate"), s(" at every login, logout and privilege change.")},
			[]seeLink{{"Sessions and Rotate", "/security#sessions-and-rotate"}, {"The session cookie", "/security#the-session-cookie"}},
			[]h.H{API("via.Session"), API("via.Ctx.Session")}},
		{"SessionStore",
			[]h.H{s("The interface session data lives behind between requests: a map of serialized blobs. The default, "), API("via.MemorySessionStore"),
				s(", is process-local, so a restart logs everyone out and a second pod sees nothing. "), API("via.WithSessionStore"),
				s(" swaps in a shared backend; implement "), API("via.VersionedSessionStore"), s(" as well or two concurrent writes can lose one.")},
			[]seeLink{{"Session stores", "/security#session-stores"}, {"Sessions that survive a restart", "/deploy#sessions-that-survive-a-restart"}},
			[]h.H{API("via.SessionStore"), API("via.WithSessionStore")}},
		{"Signal",
			[]h.H{API("via.Signal"), s(", a field whose value lives in the browser as a Datastar signal named after the field. It is sent with every action and the server can read and Set it; T must round-trip through JSON. "),
				API("via.Signal.Bind"), s(" ties it to an input and "), API("via.Signal.Display"), s(" renders it as text.")},
			[]seeLink{{"Where state lives", "/signals#where-state-lives"}, {"Binding and display", "/signals#binding-and-display"}},
			[]h.H{API("via.Signal")}},
		{"SignalCS",
			[]h.H{API("via.SignalCS"), s(", a browser-only signal. Its name starts with "), Code("_"), s(", which Datastar leaves out of every POST, and it has no Get or Set: the server only picks its starting value. Drive it with the "),
				Code("on.*CS"), s(" binders and expr.")},
			[]seeLink{{"Client-only signals", "/signals#client-only-signals"}},
			[]h.H{API("via.SignalCS")}},
		{"SSE",
			[]h.H{s("Server-Sent Events: the one-way HTTP stream via opens for a tab whose page has a live unit. One stream per tab carries every live unit's element and signal patches; there are no WebSockets. A dropped stream reconnects, and a reconnect is a new connection that runs OnInit again.")},
			[]seeLink{{"A page is plain until a unit goes live", "/live#a-page-is-plain-until-a-unit-goes-live"}, {"Connection lifecycle", "/live#connection-lifecycle"}},
			[]h.H{API("via.WithMaxSSEConn")}},
		{"State",
			[]h.H{API("via.State"), s(", server-only unit state. It never reaches the browser as a signal: "), API("via.State.Display"),
				s(" renders it as text, marks the unit live, and each Set is pushed as a patch. "), API("via.State.Get"), s(" alone does not make a unit live. Mirror it into a Signal when client code must react to it.")},
			[]seeLink{{"Server state", "/live#server-state"}},
			[]h.H{API("via.State"), API("via.StateOf")}},
		{"Tick",
			[]h.H{API("via.Ctx.Tick"), s(" runs a function every interval for the life of the unit's connection, on the unit's goroutine, then re-renders and pushes. Valid only in OnInit, and it makes the unit live.")},
			[]seeLink{{"Timers", "/live#timers"}},
			[]h.H{API("via.Ctx.Tick")}},
		{"Topic",
			[]h.H{API("topic.Topic"), s(", an in-process fan-out broker: one Publish reaches every subscriber, and Publish is safe from any goroutine. It is how another tab's action or a goroutine of yours reaches a live unit, through Listen. Delivery stays in one process, with no durability, replay or cross-pod delivery.")},
			[]seeLink{{"Topics", "/live#topics"}, {"State across pods", "/deploy#state-across-pods"}},
			[]h.H{API("topic.New"), API("topic.Topic.Publish")}},
		{"View",
			[]h.H{Code("View() h.H"), s(", the one required method of a composition. It is pure and takes no context: it reads fields and returns markup, and via calls it on every render, so request reads and side effects belong in OnInit or an action. "),
				API("via.Mount"), s(" rejects a type without one at compile time.")},
			[]seeLink{{"The parts", "/start#the-parts"}, {"A composition is a struct", "/compositions#a-composition-is-a-struct"}},
			nil},
		{"Wire pane",
			[]h.H{s("A go-via.dev feature, not part of via: the pane under each demo card that lists the demo's action POSTs, their statuses, and the SSE patches it receives.")},
			[]seeLink{{"Run a counter", "/start#run-a-counter"}},
			nil},
	}
}

func (p *Glossary) View() h.H {
	d := p.doc()
	terms := glossaryTerms()
	body := []h.H{
		h.P(h.Str("The terms these docs use. Each links to the section that teaches it and to its API row where one exists.")),
		glossaryJump(terms),
	}
	for _, t := range terms {
		body = append(body, d.H3(t.name), h.P(t.def...))
		see := []h.H{h.Str("See: ")}
		for i, l := range t.see {
			if i > 0 {
				see = append(see, h.Str(", "))
			}
			see = append(see, h.A(h.Href(d.Href(l.href)), h.Str(l.text())))
		}
		for _, a := range t.api {
			see = append(see, h.Str(", "), a)
		}
		body = append(body, h.P(append(see, h.Str("."))...))
	}
	return d.Page(body...)
}

// glossaryJump is the A-Z line: a letter with terms links to its first one, a
// letter without stays as text so the line keeps its shape.
func glossaryJump(terms []glossaryTerm) h.H {
	first := map[byte]string{}
	for _, t := range terms {
		c := strings.ToUpper(t.name[:1])[0]
		if _, ok := first[c]; !ok {
			first[c] = shell.Slug(t.name)
		}
	}
	var line []h.H
	for c := byte('A'); c <= 'Z'; c++ {
		if c > 'A' {
			line = append(line, h.Str(" "))
		}
		if id, ok := first[c]; ok {
			line = append(line, h.A(h.Href("#"+id), h.Str(string(c))))
		} else {
			line = append(line, h.Str(string(c)))
		}
	}
	return h.Nav(h.Aria("label", "Terms by letter"), h.P(line...))
}
