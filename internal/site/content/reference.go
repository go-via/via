package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/shell"
)

// Links track the default branch rather than a tag: a pinned version goes
// stale the day after a release and nobody notices until the links 404.
const repo = "https://github.com/go-via/via/blob/main/"

type row struct{ name, use string }

var composition = []row{
	{"via.NewRouter(opts…) *via.Router",
		"An empty router. Mount pages onto it, serve it, and Close it when the server shuts down."},
	{`via.Mount(r, "/thread/{id}", Thread{})`,
		"Registers a page composition at an http.ServeMux pattern, serving that exact path and never a subtree: \"/docs/\" does not answer /docs/intro. root is taken by value, and the page's actions post to {path}/_via/a/{child}/{act}, so a {name...} or {$} wildcard, or one named child or act, panics."},
	{"via.Handler(root, opts…) *via.Router",
		"A one-page app: a Router with root mounted at \"/\". It returns the Router rather than an http.Handler so the live half stays reachable."},
	{"r.Close()",
		"Drains the live half — stream goroutines, Tick timers, Listen subscriptions — and returns once it is quiet. Call it before http.Server.Shutdown, which does not cancel the router's own context."},
	{"via.Child(p.Chat)",
		"Renders a struct field as its own child, keyed by its position among the parent's Child calls. The argument must be a field selector: a composite literal would re-seed the child on every render."},
	{"via.When(cond, p.build)",
		"Renders build() when cond holds and does not call it otherwise. cond decides what is dispatchable as well as what is drawn, so a handler inside a closed branch answers 410."},
	{"via.Each(items, p.row)",
		"Renders row(item) for every item, in order, in place. Rows morph by position, so give a row a stable id when the order can change."},
	{"via.On(event, p.Handler)",
		"Binds a DOM event to a method. The click posts to the method, not to a URL you invented."},
	{"via.OnArg(event, p.Handler, arg)",
		"The same, carrying one typed value with the click. Only an argument the render bound is dispatchable."},
	{"via.PostForm(p.Submit, children…)",
		"A native multipart form whose submit is a real navigation, read back with ctx.Request().FormValue."},
	{"via.State[T], via.List[E]",
		"Server-authoritative per-connection state, rendered as text and morphed when it changes. Rendering one makes its unit live. List adds Append, Remove and Each over State[[]E]."},
	{"via.Signal[T], via.SignalCS[T]",
		"Client-resident state that round-trips per request. SignalCS is _-prefixed, which Datastar's fetch filter drops, so the server never sees it."},
	{"via.StateOf(v), via.ListOf(v…)",
		"Seed a State or List from a parent's composite literal, for a child that has no OnInit of its own to Set it in."},
	{"via.StateTrack(t, load)",
		"A State that seeds from load at every init of its unit, re-seeds once the stream has subscribed, then follows the topic. For a source fixed at mount; State.Track in OnInit is for one that depends on the request."},
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

var ctxCalls = []row{
	{`ctx.Param[int]("id")`,
		"The mount pattern's named segment, decoded into T. A segment that will not decode answers 404 rather than a zero value, and naming a segment the pattern does not have panics."},
	{"ctx.Request() *http.Request",
		"The request behind this render. Its query string is populated on the GET and empty on every action, because an action's URL is the mount pattern and nothing else."},
	{"ctx.Context() context.Context",
		"The context bounding this unit's work: the stream's on a live unit, the request's otherwise. Never nil. Watch it in a Tick or Listen handler to abandon a slow call against a dead socket."},
	{"ctx.Session() *via.Session",
		"The browser session — one value, Get/Put/Delete, ID and Rotate — resolved from the signed cookie and created lazily on the first write."},
	{"ctx.Tick(d, p.beat)",
		"Runs the handler every d for the life of the connection and re-renders the unit after each run. Makes the unit live. OnInit only."},
	{"ctx.Listen(t, p.recv)",
		"Subscribes the unit to a topic and runs the handler on the unit's own goroutine for every value, unsubscribing on disconnect. Makes the unit live. OnInit only."},
	{"ctx.OnConnect(fn)",
		"Runs once, when this unit's stream opens, after every Listen has subscribed. OnInit only; on a unit nothing made live it never runs."},
	{"ctx.OnDispose(fn)",
		"Runs when the unit's connection closes — stop subscriptions, release producers. OnInit only, and only meaningful on a unit something has made live."},
	{"ctx.Redirect(path)",
		"Navigates the browser after the current handler returns, from OnInit, OnReload, a PostForm submit or an action alike. The render that called it ships nothing."},
}

var options = []row{
	{"via.WithHead(head)",
		"The router-wide document shell: lang, raw head markup, and the assets every page carries. An invalid head panics at startup."},
	{"via.WithErrorPage(fn)",
		"Renders via's failures as HTML documents instead of plain text. It applies to document responses only."},
	{"via.WithTrustedOrigin(origin)",
		"Turns on origin enforcement for the action endpoint and allowlists one exact origin. Without any set, every action and the stream connect accept any origin and via logs a warning at startup — set this in production."},
	{"via.WithSessionKey(key)",
		"The HMAC key signing the session cookie id; at least 16 bytes, or it panics. Unset, via falls back to the VIA_SESSION_KEY environment variable, and failing that mints a random per-process key, so those cookies survive neither a restart nor a second process."},
	{"via.WithSessionStore(s)",
		"Points sessions at a shared, durable store instead of the default process-local map. Pair it with WithSessionKey; both are required past one pod, see Deploy."},
	{"via.WithSessionStoreTimeout(d)",
		"Caps one session store round-trip (default 5s). Without it a hung backend pins the request goroutine, since session calls survive client cancellation."},
	{"via.WithSessionTTL(d)",
		"How long a session may sit idle before it expires (default 24h). Each access slides the window."},
	{"via.WithSessionCookieName(name)",
		"Overrides the cookie name (default \"via_session\"). Set a distinct one per app when two via apps share a host."},
	{"via.WithSecureCookies()",
		"Forces Secure on the session cookie even when via cannot see TLS — which is the case behind a TLS-terminating proxy."},
	{"via.WithMaxSSEConn(n)",
		"Caps how many live streams this router serves at once (default 10000); past the cap a connect is refused 503."},
	{"via.WithPinnedDeadline(d)",
		"How long an action POST waits for the tab's stream goroutine to pick it up before answering 503 and logging the tab as pinned (default 5s). An action that has started is waited for, since it writes the POST's own response."},
	{"via.WithMaxBody(bytes)",
		"Caps an action POST body, and how much of a form submit stays in RAM before spilling to a temp file (default 1 MiB). Over the cap answers 413."},
	{"via.WithMaxUpload(bytes)",
		"Caps a native form submit's whole multipart body (default 8 MiB). Over the cap answers 413."},
	{"via.WithLogger(l)",
		"Routes via's own diagnostics to l. Default is slog.Default()."},
}

var errorSurface = []row{
	{"via.ErrNotFound",
		"Return it from OnInit when the data the page needs no longer exists: the request was honest, so the answer is 404, not 500. Wrap it freely; errors.Is matches."},
	{"via.ErrForbidden",
		"Denies with a 403. For \"you may not do this\"; queue a ctx.Redirect instead for \"please sign in\"."},
	{"via.ErrStoreDown",
		"The session store could not be read for this request. The request was fine, a dependency is not; answer it like an outage."},
	{"via.ErrStaleTab",
		"The tab that would have bound this action is gone: its stream closed, or the id belongs to a render that no longer exists. The one failure a reload fixes."},
	{"via.PageError{Status, Reason, Detail, Err}",
		"Everything via knows about a failure it is about to answer, handed to the WithErrorPage handler. Status and Reason are the contract; Detail and Err are for logs and dev builds."},
	{"via.Reason",
		"The stable code an error page switches on: bad_request, forbidden, not_found, method_not_allowed, gone, too_large, internal, unavailable. One per status class, so a switch with a default is exhaustive."},
}

var exprAPI = []row{
	{"expr.El",
		"The element the attribute is written on."},
	{"expr.Lit(v)",
		"Encodes v as a JavaScript literal; an Expr passes through unchanged."},
	{"expr.All(es…), expr.Any(es…)",
		"Joins the expressions with && and ||. A single one is returned unchanged; none panics."},
	{"expr.Do(es…)",
		"Sequences statements, for an attribute that runs more than one."},
	{"expr.Call(name, args…)",
		"Applies a function by name, which may be a dotted path (\"console.log\"). An invalid name panics."},
	{"expr.Raw(js)",
		"Emits js verbatim and unchecked. Never build one from user input."},
	{"expr.Rawf(format, args…)",
		"Splices checked expressions into unchecked text: each %s takes the next Expr verbatim. The text itself is emitted as written, like Raw."},
	{"e.Eq(v), e.Ne(v)",
		"Comparison with JavaScript's strict === and !==."},
	{"e.Lt(v), e.Le(v), e.Gt(v), e.Ge(v)",
		"The four orderings."},
	{"e.Not()",
		"Negates the expression."},
	{"e.Assign(v), e.Add(v), e.Toggle()",
		"Write, add to, and negate the signal in place. The receiver must be a bare $name."},
	{"e.String()",
		"The expression source."},
}

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

// Reference is the page listing the API, the links and the attribute helpers.
type Reference struct{ page }

// NewReference builds the reference page for a deployment at origin.
func NewReference(origin string) Reference { return Reference{page: newPage("/reference", origin)} }

func (p *Reference) PageMeta() via.Meta {
	return p.meta(
		"The v0.8 API in tables: composition, Ctx, the router options, the expr vocabulary, the h.Data* helpers and the lifecycle hooks.")
}

func (p *Reference) View() h.H {
	return shell.Page(p.nav,
		h.H2(h.Str("Install")),
		h.Pre(h.Code(h.Str("go get github.com/go-via/via"))),
		h.P(h.Str("Go 1.27 or newer, standard library only, no build step.")),

		h.H2(h.Str("Links")),
		h.Ul(
			h.Li(h.A(h.Href("https://github.com/go-via/via"), h.Str("github.com/go-via/via")),
				h.Str(" — the source.")),
			h.Li(h.A(h.Href("https://pkg.go.dev/github.com/go-via/via"), h.Str("pkg.go.dev/github.com/go-via/via")),
				h.Str(" — the package documentation, every exported name with its contract.")),
			h.Li(h.A(h.Href(repo+"MIGRATION.md"), h.Str("MIGRATION.md")),
				h.Str(" — what changed since v0.7, and how to move a v0.7 app.")),
			h.Li(h.A(h.Href(repo+"CHANGELOG.md"), h.Str("CHANGELOG.md")),
				h.Str(" — the release-by-release record.")),
			h.Li(h.A(h.Href(repo+"AGENTS.md"), h.Str("AGENTS.md")),
				h.Str(" — the rules a coding agent working on via has to follow.")),
		),

		h.H2(h.Str("Composition")),
		h.P(h.Str("A router, the pages mounted on it, and the handles a page renders.")),
		refTable("Call", composition),

		h.H2(h.Str("Lifecycle hooks")),
		h.P(h.Str("Four methods on your own type, duck-typed: a unit opts in by having one. A hook-named "+
			"method with the wrong signature panics at Mount, and a near-miss name carrying a hook's exact "+
			"signature is logged once.")),
		refTable("Method", hooks),

		h.H2(h.Str("Ctx")),
		h.P(h.Str("The per-request binder, handed to every callback but View. It is not safe for concurrent "+
			"use: call it only from the via callback it was given to.")),
		refTable("Call", ctxCalls),

		h.H2(h.Str("Router options")),
		h.P(h.Str("Router-wide policy, passed to NewRouter or Handler. A page cannot widen its own, which is "+
			"why none of these is a method on a composition.")),
		refTable("Option", options),

		h.H2(h.Str("The error surface")),
		h.P(h.Str("What a WithErrorPage handler is handed, and the sentinels a hook returns to choose the "+
			"status. Match a sentinel with errors.Is — PageError.Err is nil except for a hook that failed "+
			"or a panic that was recovered. Switch on PageError.Reason, and keep a default: a status via "+
			"does not emit today maps to ReasonInternal or ReasonBadRequest.")),
		refTable("Name", errorSurface),

		h.H2(h.Str("Expressions")),
		h.P(h.Str("The expr package builds the small JavaScript expressions Datastar evaluates in the browser. "+
			"Signal.Ref() is where one starts: it returns the signal's $name as an Expr.")),
		refTable("Call", exprAPI),

		h.H2(h.Str("Attribute helpers")),
		h.P(h.Str("One typed helper per Datastar plugin, so the attribute key is spelled once. The helpers take "+
			"an expr.Expr, and h.Data(name, value) writes a plugin attribute h has no helper for.")),
		helperTable(),
	)
}

func refTable(head string, rows []row) h.H {
	out := []h.H{h.Class("ref-table"), h.Tr(h.Th(h.Str(head)), h.Th(h.Str("Does")))}
	for _, r := range rows {
		out = append(out, h.Tr(h.Td(h.Code(h.Str(r.name))), h.Td(h.Str(r.use))))
	}
	return h.Table(out...)
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
