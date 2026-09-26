package content

import (
	"strconv"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/shell"
)

// Links track the default branch rather than a tag: a pinned version goes
// stale the day after a release and nobody notices until the links 404.
const repo = "https://github.com/go-via/via/blob/main/"

type row struct{ name, use string }

// overrides replace a symbol's doc-comment first sentence where the site says
// it better for a reader of the page. Keyed by row id; a test fails on a key
// that no longer names an exported symbol.
var overrides = map[string]string{
	"via.NewRouter":       "An empty router. Mount pages onto it, serve it, and Shutdown it when the server shuts down.",
	"via.Mount":           "Registers a page composition at an http.ServeMux pattern, serving that exact path and never a subtree: \"/docs/\" does not answer /docs/intro. root is taken by value, and the page's actions post to {path}/_via/a/{child}/{act}, so a {name...} or {$} wildcard, or one named child or act, panics.",
	"via.Handler":         "A one-page app: a Router with root mounted at \"/\". It returns the Router rather than an http.Handler so the live half stays reachable.",
	"via.Router.Shutdown": "Drains the live half — stream goroutines, Tick timers, Listen subscriptions — and returns once it is quiet, or with ctx's error at its deadline, logging the tabs whose handler is still blocked. Call it before http.Server.Shutdown, which does not cancel the router's own context.",
	"via.Router.Close":    "Shutdown with no deadline.",
	"via.Child":           "Renders a struct field as its own child, keyed by its position among the parent's Child calls. The argument must be a field selector: a composite literal would re-seed the child on every render.",
	"via.When":            "Renders build() when cond holds and does not call it otherwise. cond decides what is dispatchable as well as what is drawn, so a handler inside a closed branch answers 410.",
	"via.Each":            "Renders row(item) for every item, in order, in place. Rows morph by position, so give a row a stable id when the order can change.",
	"via.PostForm":        "A native multipart form whose submit is a real navigation, read back with ctx.Request().FormValue.",
	"via.State":           "Server-authoritative per-tab state, rendered as text and morphed when it changes. Rendering one makes its unit live.",
	"via.List":            "A State over a slice, with Append, Remove and Each.",
	"via.Signal":          "Client-resident state that round-trips per request.",
	"via.SignalCS":        "Client-only state. Its name is _-prefixed, which Datastar's fetch filter drops, so the server never sees it.",
	"via.StateOf":         "Seeds a State from a parent's composite literal, for a child that has no OnInit of its own to Set it in.",
	"via.ListOf":          "Seeds a List from a parent's composite literal, for a child that has no OnInit of its own to Set it in.",
	"via.StateTrack":      "A State that seeds from load at every init of its unit, re-seeds once the stream has subscribed, then follows the topic. For a source fixed at mount; State.Track in OnInit is for one that depends on the request.",

	"via.Ctx":                  "The per-request binder, handed to every callback but View. It is not safe for concurrent use: call it only from the via callback it was given to.",
	"via.Ctx.Param":            "The mount pattern's named segment, decoded into T. A segment that will not decode answers 404 rather than a zero value, and naming a segment the pattern does not have panics.",
	"via.Ctx.Request":          "The request behind this render. Its query string is populated on the GET and empty on every action, because an action's URL is the mount pattern and nothing else.",
	"via.Ctx.Context":          "The context bounding this unit's work: the stream's on a live unit, the request's otherwise. Never nil. Watch it in a Tick or Listen handler to abandon a slow call against a dead socket.",
	"via.Ctx.Session":          "The browser session — one value, Get/Put/Delete, ID and Rotate — resolved from the signed cookie and created lazily on the first write.",
	"via.Ctx.Tick":             "Runs the handler every d for the life of the connection and re-renders the unit after each run. Makes the unit live. OnInit only.",
	"via.Ctx.Listen":           "Subscribes the unit to a topic and runs the handler on the unit's own goroutine for every value, unsubscribing on disconnect. Makes the unit live. OnInit only.",
	"via.Ctx.OnConnect":        "Runs once, when this unit's stream opens, after every Listen has subscribed. OnInit only; on a unit nothing made live it never runs.",
	"via.Ctx.OnDispose":        "Runs when the unit's connection closes — stop subscriptions, release producers. OnInit only, and only meaningful on a unit something has made live.",
	"via.Ctx.Redirect":         "Navigates the browser after the current handler returns, from OnInit, OnReload, a PostForm submit or an action alike. The render that called it ships nothing. Only a relative path or a URL on this site's host or a trusted origin is followed; anything else is dropped and logged.",
	"via.Ctx.RedirectExternal": "Redirect to any http(s) URL, for a hand-off that leaves the site (OAuth, payment). Other schemes are still dropped. Build the target yourself; one taken from the request is an open redirect.",

	"via.WithHead":                "The router-wide document shell: lang, raw head markup, and the assets every page carries. An invalid head panics at startup.",
	"via.WithErrorPage":           "Renders via's failures as HTML documents instead of plain text. It applies to document responses only.",
	"via.WithTrustedOrigin":       "Turns on origin enforcement for the action endpoint and the stream connect and allowlists one origin. Without any set, every action and the stream connect accept any origin and via logs a warning at startup — set this in production. Host case, a default port and a trailing \"/\" don't matter; a value with a path, query, fragment or userinfo panics.",
	"via.WithSessionKey":          "The HMAC key signing the session cookie id; at least 16 bytes, or it panics. Unset, via falls back to the VIA_SESSION_KEY environment variable, and failing that mints a random per-process key, so those cookies survive neither a restart nor a second process.",
	"via.WithSessionStore":        "Points sessions at a shared, durable store instead of the default process-local map. Pair it with WithSessionKey; both are required past one pod, see Deploy.",
	"via.WithSessionStoreTimeout": "Caps one session store round-trip (default 5s). Without it a hung backend pins the request goroutine, since session calls survive client cancellation.",
	"via.WithSessionTTL":          "How long a session may sit idle before it expires (default 24h). Each access slides the window.",
	"via.WithSessionCookieName":   "Overrides the cookie name (default \"via_session\"). Set a distinct one per app when two via apps share a host.",
	"via.WithSecureCookies":       "Forces Secure on the session cookie. Without it Secure follows TLS or the proxy's X-Forwarded-Proto / Forwarded header, so it is for a proxy that sends neither.",
	"via.WithMaxSSEConn":          "Caps how many live streams this router serves at once (default 10000); past the cap a connect is refused 503. The same number, up to 1024, caps actions parked waiting for their tab's stream; past it an action answers 503.",
	"via.WithPinnedDeadline":      "How long an action POST waits for the tab's stream goroutine to pick it up before answering 503 and logging the tab as pinned (default 5s). An action that has started is waited for, since it writes the POST's own response. The same deadline covers the wait for a tab's stream that has not connected yet, capped at 2s: 410 if it has not come by then, and at once if via itself ended it.",
	"via.WithMaxBody":             "Caps an action POST body, and how much of a form submit stays in RAM before spilling to a temp file (default 1 MiB). Over the cap answers 413.",
	"via.WithMaxUpload":           "Caps a native form submit's whole multipart body (default 8 MiB). Over the cap answers 413.",
	"via.WithLogger":              "Routes via's own diagnostics to l. Default is slog.Default().",
	"via.WithUnsafeEval":          "Adds 'unsafe-eval' to every page's script-src, for a library that compiles code from strings. The nonce and hashes stay. While set, via logs a warning at startup: a string that reaches eval, Function or setTimeout(string) runs as script.",

	"via.ErrNotFound":  "Return it from OnInit when the data the page needs no longer exists: the request was honest, so the answer is 404, not 500. Wrap it freely; errors.Is matches.",
	"via.ErrForbidden": "Denies with a 403. For \"you may not do this\"; queue a ctx.Redirect instead for \"please sign in\".",
	"via.ErrStoreDown": "The session store could not be read for this request. The request was fine, a dependency is not; answer it like an outage.",
	"via.ErrStaleTab":  "The tab that would have bound this action is gone: its stream closed, or the id belongs to a render that no longer exists. The one failure a reload fixes.",
	"via.PageError":    "Everything via knows about a failure it is about to answer, handed to the WithErrorPage handler. Status and Reason are the contract; Detail and Err are for logs and dev builds.",
	"via.Reason":       "The stable code an error page switches on: bad_request, forbidden, not_found, method_not_allowed, gone, too_large, internal, unavailable. One per status class, so a switch with a default is exhaustive.",

	"on.Click":    "Binds a DOM event to a method: the click posts to the method, not to a URL you invented. Takes a method value or an on.WithArg, then modifiers.",
	"on.WithArg":  "Attaches a typed value to the method, so the row's own datum rides with the event. Only an argument the render bound is dispatchable.",
	"on.Event":    "Posts a method on an event with no function of its own. The name must be lower-case, such as \"pointerdown\" or \"via:patch\"; anything else panics.",
	"on.ClickCS":  "The client-only twin of Click: runs the expression in the browser and posts nothing.",
	"on.EventCS":  "The client-only twin of Event: runs the expression in the browser and posts nothing.",
	"on.Debounce": "Runs the handler once the event stops firing for d. A non-positive d panics.",
	"on.Throttle": "Runs the handler at most once per d. A non-positive d panics.",
	"on.Once":     "Runs the handler once.",
	"on.Prevent":  "Calls preventDefault.",
	"on.Stop":     "Calls stopPropagation.",
	"on.Outside":  "Fires only for targets outside the element.",
	"on.Window":   "Listens on window.",

	"expr.El":              "The element the attribute is written on.",
	"expr.Val":             "Encodes v as a JavaScript literal; an Expr passes through unchanged. An @ is escaped so Datastar does not read an @name( in it as an action call.",
	"expr.All":             "Joins the expressions with &&. A single one is returned unchanged; none panics.",
	"expr.Any":             "Joins the expressions with ||. A single one is returned unchanged; none panics.",
	"expr.Do":              "Sequences statements, for an attribute that runs more than one.",
	"expr.Call":            "Applies a function by name, which may be a dotted path (\"console.log\"). An invalid name panics.",
	"expr.CopyToClipboard": "Writes text to the clipboard. Browsers allow it only in a secure context and from a user gesture, so bind it to a click.",
	"expr.CopyTextOf":      "Copies the text of the first element matching sel inside the handler element's parent, so a button copies the block beside it.",
	"expr.Class":           "Adds or removes a class on the handler element, for feedback not worth a signal. A morph of the element resets it.",
	"expr.Raw":             "Emits js verbatim and unchecked. Never build one from user input.",
	"expr.Rawf":            "Splices checked expressions into unchecked text: each %s takes the next Expr verbatim. The text itself is emitted as written, like Raw.",
	"expr.Expr.Eq":         "Comparison with JavaScript's strict ===.",
	"expr.Expr.Ne":         "Comparison with JavaScript's strict !==.",
	"expr.Expr.Not":        "Negates the expression.",
	"expr.Expr.Assign":     "Writes v to the signal in place. The receiver must be a bare $name.",
	"expr.Expr.Add":        "Adds v to the signal in place. The receiver must be a bare $name.",
	"expr.Expr.Toggle":     "Negates the signal in place. The receiver must be a bare $name.",
	"expr.Expr.String":     "The expression source.",
}

var hooks = []row{
	{"View() h.H",
		"The unit's markup, and the one required method. It takes no Ctx: whatever it draws was loaded into fields by OnInit. A missing or mistyped View on a mounted root is a compile error."},
	{"OnInit(*via.Ctx) error",
		"Loads request and session data, and registers the timers and subscriptions that make the unit live. It runs on every request that renders the unit — the GET, the stream connect, and each action on a page that is not streaming."},
	{"OnReload(*via.Ctx) error",
		"Runs after one of the unit's actions and before the render that answers it, so a handler that mutated a store re-reads here. Skipped behind a Redirect."},
	{"PageMeta() via.Meta",
		"The mounted root's own document: title, description, social cards, assets. Read after OnInit and OnReload, on a render that writes a document, never on an SSE push."},
}

type dataHelper struct{ name, attr, use string }

var dataHelpers = []dataHelper{
	{"h.DataShow(e)", "data-show", "Shows the element while the expression is truthy."},
	{"h.DataText(e)", "data-text", "Replaces the element's text with the expression's value."},
	{"h.DataClass(name, e)", "data-class:<name>", "Toggles the named class while the expression is truthy."},
	{"h.DataAttr(name, e)", "data-attr:<name>", "Sets the named attribute from the expression."},
	{"h.DataStyle(prop, e)", "data-style:<prop>", "Sets the named CSS property from the expression."},
	{"h.DataOn(event, stmts…)", "data-on:<event>", "Runs the statements on that DOM event. Package on writes the same attribute."},
	{"h.DataEffect(stmts…)", "data-effect", "Runs the statements whenever a signal they read changes."},
	{"h.DataComputed(name, e)", "data-computed:<name>", "Declares a read-only signal derived from the expression."},
	{"h.DataIndicator(sig)", "data-indicator", "Names the signal held true while a request from this element is in flight."},
	{"h.DataRef(sig)", "data-ref", "Names the signal Datastar puts this element into."},
	{"h.DataIgnoreMorph()", "data-ignore-morph", "Marks a subtree some JavaScript owns, so a live patch leaves it alone."},
}

// Reference is the page listing every exported name of the public packages,
// generated from their source, plus the lifecycle hooks and the Datastar
// attribute map, which no package exports.
type Reference struct{ page }

func NewReference(env Env) Reference { return Reference{page: newPage("/reference", env)} }

func (p *Reference) PageMeta() via.Meta {
	return p.meta(
		"Every exported name in via, h, on, expr, topic, vt and vtbrowser, generated from the source: signature, what it does, and a link to its pkg.go.dev entry.")
}

type refPackage struct {
	name  string
	intro func(d *shell.Doc) h.H
	extra func(d *shell.Doc) h.H
}

var refPackages = []refPackage{
	{name: "via", intro: func(*shell.Doc) h.H {
		return h.P(h.Str("The router, the pages mounted on it, the state handles a page renders, "),
			API("via.Ctx"), h.Str(", sessions and the error surface. Router options are policy passed to "),
			API("via.NewRouter"), h.Str(" or "), API("via.Handler"),
			h.Str(": a page cannot widen its own, which is why none of them is a method on a composition. "+
				"Match a sentinel error with errors.Is and switch on "), APIText("via.PageError", "PageError.Reason"),
			h.Str(" with a default: a status via does not emit today maps to ReasonInternal or ReasonBadRequest."))
	}, extra: func(d *shell.Doc) h.H {
		return h.Div(
			d.H3("Lifecycle hooks"),
			h.P(h.Str("Four methods on your own type, duck-typed: a unit opts in by having one. A hook-named "+
				"method with the wrong signature panics at Mount, and a near-miss name carrying a hook's exact "+
				"signature is logged once.")),
			refTable("Method", hooks),
		)
	}},
	{name: "h", intro: func(*shell.Doc) h.H {
		return h.P(h.Str("Markup as Go calls: one function per HTML element and one per attribute, all returning "),
			API("h.H"), h.Str(". Text and attribute values are escaped at render time, and URL attributes are "+
				"scheme-checked."))
	}, extra: func(d *shell.Doc) h.H {
		return h.Div(
			d.H3("Datastar attributes"),
			h.P(h.Str("One typed helper per Datastar plugin, so the attribute key is spelled once. The helpers take "+
				"an expr.Expr, and "), API("h.Data"), h.Str(" writes a plugin attribute h has no helper for.")),
			helperTable(),
		)
	}},
	{name: "on", intro: func(*shell.Doc) h.H {
		return h.P(h.Str("Each DOM event as a function, so a misspelled event or modifier does not compile. "+
			"Each has a server form that posts a method and a CS twin that runs an expression in the browser. "),
			API("via.On"), h.Str(" and "), API("via.OnArg"), h.Str(" still work, but are deprecated and removed in v0.9."))
	}},
	{name: "expr", intro: func(*shell.Doc) h.H {
		return h.P(h.Str("The small JavaScript expressions Datastar evaluates in the browser. "),
			APIText("via.Signal.Ref", "Signal.Ref()"), h.Str(" is where one starts: it returns the signal's $name as an "),
			API("expr.Expr"), h.Str(". Everything is checked except "), API("expr.Raw"), h.Str(" and the text of "),
			API("expr.Rawf"), h.Str("."))
	}},
	{name: "topic", intro: func(*shell.Doc) h.H {
		return h.P(h.Str("An in-process fan-out broker: one Publish reaches every subscriber. It is one process's " +
			"memory, with no durability, replay or cross-pod delivery; put a real bus behind a Topic for those."))
	}},
	{name: "vt", intro: func(*shell.Doc) h.H {
		return h.P(h.Str("A black-box test harness: drives a handler over real HTTP through via's public surface, " +
			"so a test can fire an action or read a live stream without request plumbing."))
	}},
	{name: "vtbrowser", intro: func(*shell.Doc) h.H {
		return h.P(h.Str("Drives a handler in a real headless Chromium, for the bugs httptest cannot see. A separate " +
			"module, so chromedp stays out of via's dependency graph; a test skips when no browser is found."))
	}},
}

func (p *Reference) View() h.H {
	d := p.doc()
	jumps := []h.H{h.Class("ref-jump")}
	for i, rp := range refPackages {
		if i > 0 {
			jumps = append(jumps, h.Str(" · "))
		}
		jumps = append(jumps, h.A(h.Href("#"+shell.Slug(rp.name)), h.Code(h.Str(rp.name))),
			h.Str(" "+strconv.Itoa(countPkg(rp.name))))
	}
	body := []h.H{
		h.P(h.Str("Every exported name, generated from the source by go generate; a test fails when this page "+
			"and the code disagree. Each name links to its pkg.go.dev entry, and each row has an anchor: "),
			h.Code(h.Str("/reference#via.WithSessionTTL")), h.Str(". To install, see "),
			h.A(h.Href(d.Href("/start")), h.Str("Getting started")), h.Str(".")),
		h.P(jumps...),
	}
	for _, rp := range refPackages {
		body = append(body, d.H2(rp.name), rp.intro(d))
		body = append(body, pkgGroups(rp.name)...)
		if rp.extra != nil {
			body = append(body, rp.extra(d))
		}
	}
	return d.Page(body...)
}

func countPkg(pkg string) int {
	n := 0
	for _, s := range apiSymbols {
		if s.pkg == pkg {
			n++
		}
	}
	return n
}

func isOption(s apiSymbol) bool {
	return s.kind == "func" && (strings.HasPrefix(s.name, "With") ||
		strings.HasSuffix(s.sig, " Option") || strings.HasSuffix(s.sig, " MountOption"))
}

// pkgGroups splits a package into types (each followed by its methods, which
// the generator's name order already places there), functions, options and
// values.
func pkgGroups(pkg string) []h.H {
	var types, funcs, opts, values []apiSymbol
	for _, s := range apiSymbols {
		if s.pkg != pkg {
			continue
		}
		switch {
		case s.kind == "type" || s.kind == "method":
			types = append(types, s)
		case isOption(s):
			opts = append(opts, s)
		case s.kind == "func":
			funcs = append(funcs, s)
		default:
			values = append(values, s)
		}
	}
	var out []h.H
	for _, g := range []struct {
		title string
		syms  []apiSymbol
	}{
		{"Types", types}, {"Functions", funcs}, {"Options", opts}, {"Constants and variables", values},
	} {
		if len(g.syms) == 0 {
			continue
		}
		id := pkg + "-" + shell.Slug(g.title)
		out = append(out,
			h.H3(h.ID(id), h.A(h.Class("anchor"), h.Href("#"+id), h.Aria("label", "Link to this section"), h.Str("#")),
				h.Str(g.title)),
			symbolTable(g.syms))
	}
	return out
}

func symbolTable(syms []apiSymbol) h.H {
	rows := make([][]h.H, 0, len(syms))
	for _, s := range syms {
		rows = append(rows, []h.H{symbolCell(s), h.Str(does(s))})
	}
	return table([]string{"Symbol", "Does"}, rows...)
}

func symbolCell(s apiSymbol) h.H {
	id := s.id()
	// A break only after a dot, so a narrow column never splits a name mid-word.
	var parts []h.H
	for i, part := range strings.Split(s.name, ".") {
		if i > 0 {
			parts = append(parts, h.Str("."), h.Wbr())
		}
		parts = append(parts, h.Str(part))
	}
	name := h.Span(parts...)
	class := "ref-sym"
	if s.deprecated != "" {
		name = h.S(name)
		class += " deprecated"
	}
	return h.Div(h.ID(id), h.Class(class),
		h.A(h.Class("anchor"), h.Href("#"+id), h.Aria("label", "Link to "+id), h.Str("#")),
		h.Code(h.A(h.Href(pkgGoDev(s)), name)),
		h.Div(h.Class("ref-sig"), h.Samp(h.Str(s.sig))),
	)
}

func does(s apiSymbol) string {
	if s.deprecated != "" {
		return "Deprecated: " + s.deprecated
	}
	if o, ok := overrides[s.id()]; ok {
		return o
	}
	return s.doc
}

func pkgGoDev(s apiSymbol) string {
	path := "github.com/go-via/via"
	if s.pkg != "via" {
		path += "/" + s.pkg
	}
	return "https://pkg.go.dev/" + path + "#" + s.name
}
