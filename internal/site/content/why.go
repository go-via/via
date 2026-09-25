package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
)

// Why is the page a skeptical Go developer reads before adopting via: the
// model, what it buys, what it costs, and where another tool fits better.
type Why struct {
	page
	Quote demos.WhyQuote
}

func NewWhy(env Env) Why { return Why{page: newPage("/why", env)} }

func (p *Why) PageMeta() via.Meta {
	m := p.meta("Why via: server-owned UI state in one Go process, what it buys and what it costs.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

func (p *Why) View() h.H {
	d := p.doc()
	link := func(path, label string) h.H { return h.A(h.Href(d.Href(path)), h.Str(label)) }
	return d.Page(
		h.P(h.Str("via builds server-rendered web UI from Go structs. A page is a struct, its actions are methods, "+
			"and the browser runs a small client via ships, so there is no JavaScript to write and no build step.")),

		d.H2("The model in one example"),
		snippet.Region("demos/why_quote.go", "model",
			snippet.Mark("via.Signal[int]", "via.State[int]", "on.Click(q.Price)")),
		table([]string{"Part", "What it becomes"},
			[]h.H{Code("Qty"), h.Span(h.Str("A signal in the browser. The input edits it with no request; its value rides along on every action POST. See "), API("via.Signal"), h.Str("."))},
			[]h.H{Code("Total"), h.Span(h.Str("A value held on the server for this tab. Rendering it with "), API("via.State.Display"), h.Str(" makes the unit live, so the tab opens one SSE stream."))},
			[]h.H{Code("Price"), h.Str("An action: a POST endpoint via addresses by the method itself. There is no route to register.")},
			[]h.H{Code("on.Click(q.Price)"), h.Span(API("on.Click"), h.Str(" takes a method value, so renaming or deleting "), Code("Price"), h.Str(" is a compile error, not a dead button."))},
			[]h.H{Code("View"), h.Str("The HTML. It runs again after each action, and via patches the changed element into the page.")},
		),
		demo.Card(d.H3("Price a quote"),
			h.P(h.Str("The code above, running. The quantity is a browser value until you press Price; the total comes back as a patch on the stream.")),
			via.Child(p.Quote), "why_quote.go",
			demo.WireOpen(),
			demo.Try(h.Str("Change the quantity: the Signals box follows it and no request is sent."),
				h.Span(h.Str("Press Price: one POST, then one patch carrying the "), Code("<output>"), h.Str(".")),
				h.Str("Type -5 and press Price: the action clamps it, because a signal is client input."))),

		d.H2("What you get"),
		h.Ul(
			h.Li(h.Strong(h.Str("The compiler checks the wiring. ")),
				h.Str("Event bindings are method values and signal names come from struct fields, so no string ties markup to a handler. "+
					"The optional hooks ("), Code("OnInit"), h.Str(", "), Code("OnReload"), h.Str(", "), Code("PageMeta"),
				h.Str(") are duck-typed: a wrong signature panics at "), API("via.Mount"), h.Str(", and a near-miss name is logged once.")),
			h.Li(h.Strong(h.Str("Plain pages stay plain HTTP. ")),
				h.Str("A page streams only if it holds a live unit: one that renders a "), API("via.State"), h.Str(" or "), API("via.List"),
				h.Str(", or registers "), API("via.Ctx.Tick"), h.Str(" or "), API("via.Ctx.Listen"),
				h.Str(". Otherwise a GET is a response and an action is a POST, with no connection held. "), link("/live", "Live state"), h.Str(" has the rules.")),
			h.Li(h.Strong(h.Str("Two kinds of state, picked by field type. ")),
				API("via.Signal"), h.Str(" lives in the browser and travels with each action. "), API("via.State"), h.Str(" and "), API("via.List"),
				h.Str(" live on the server, one copy per tab, and never reach the client as data.")),
			h.Li(h.Strong(h.Str("Shared state is your store plus a topic. ")),
				h.Str("via holds no global state. Keep the shared value in your own store and publish changes on a "), API("topic.Topic"),
				h.Str("; each tab's unit follows it with "), API("via.State.Track"), h.Str(" or "), API("via.Ctx.Listen"),
				h.Str(", on the unit's own goroutine, so handlers need no locks.")),
			h.Li(h.Strong(h.Str("No JavaScript and no build step. ")),
				h.Str("The "), link("/glossary#datastar", "Datastar"), h.Str(" client is embedded in the via module and served by the router. "+
					"When a view needs a JS library, an "), link("/islands", "island"), h.Str(" hands it one subtree.")),
			h.Li(h.Strong(h.Str("Only the standard library at run time. ")),
				h.Str("via's one module requirement is testify, for its own tests.")),
		),

		d.H2("What it costs"),
		h.Ul(
			h.Li(h.Strong(h.Str("Memory and a goroutine per live tab. ")),
				h.Str("Each open tab with a live unit keeps its composition in memory and one goroutine that runs every live unit on it, "+
					"plus one per Tick timer. Capacity scales with open tabs, not request rate. "), API("via.WithMaxSSEConn"),
				h.Str(" caps streams (default 10,000) and refuses the next with a 503. The repo publishes no per-tab memory figure: it is the size of your structs.")),
			h.Li(h.Strong(h.Str("One SSE connection per live tab. ")),
				h.Str("Proxies must pass long-lived streams without buffering, and each stream holds a file descriptor. "+
					"Push is SSE only; there is no WebSocket transport.")),
			h.Li(h.Strong(Code("'unsafe-eval'"), h.Str(" in the CSP. ")),
				h.Str("Datastar compiles its expressions at run time, so the policy must allow eval; "),
				link("/security#content-security-policy", "Sessions & security"), h.Str(" says what that costs.")),
			h.Li(h.Strong(h.Str("One process unless you bridge. ")),
				h.Str("A tab lives on the process that opened its stream, and a topic fans out inside one process. "+
					"More than one instance needs sticky routing and your own bus between them; "), link("/deploy", "Deploy"), h.Str(" shows both.")),
			h.Li(h.Strong(h.Str("Coupled to Datastar. ")),
				h.Str("via does not abstract over its client. Datastar's reconnect and morph behaviour is via's behaviour.")),
			h.Li(h.Strong(h.Str("Pre-1.0. ")),
				h.Str("APIs change between minor versions; "), link("/migrate", "Migrating from v0.7"), h.Str(" lists what v0.8 broke.")),
		),

		d.H2("When not to use via"),
		h.Ul(
			h.Li(h.Strong(h.Str("Offline-first apps. ")),
				h.Str("Actions and pushes need the server. A tab that loses its stream shows a banner and reloads when it can; it does not queue work.")),
			h.Li(h.Strong(h.Str("Heavy client-side interaction. ")),
				h.Str("A canvas editor, a drag-heavy board or a game wants every frame in the browser, not a round trip. "+
					"If that part is one widget in an otherwise server-driven page, make it an "), link("/islands", "island"),
				h.Str("; if it is the whole app, use a client framework.")),
			h.Li(h.Strong(h.Str("Public static content. ")),
				h.Str("Docs, blogs and marketing pages served as files from a CDN need no process at all.")),
			h.Li(h.Strong(h.Str("Large uploads and media. ")),
				h.Str("Action bodies are capped at 1 MiB ("), API("via.WithMaxBody"), h.Str(") and native form uploads at 8 MiB ("), API("via.WithMaxUpload"), h.Str("). Stream big files through your own handler.")),
			h.Li(h.Strong(h.Str("Built-in auth. ")),
				h.Str("via ships origin checks and signed sessions, not login, users or roles.")),
		),

		d.H2("Compared with"),
		table([]string{"", "Views in", "Client runtime", "UI state", "Event wiring"},
			[]h.H{h.Strong(h.Str("via v0.8")), h.Str("Go functions returning h.H"), h.Str("Datastar, embedded in via"),
				h.Str("Signal in the browser; State and List per tab on the server"),
				h.Str("Method values; checked by the compiler")},
			[]h.H{h.Strong(h.Str("templ + htmx")), h.Str(".templ files compiled to Go"), h.Str("htmx"),
				h.Str("Your handlers and store; no per-connection view state"),
				h.Str("URL strings in hx-* attributes; handlers routed by hand")},
			[]h.H{h.Strong(h.Str("Datastar + Go SDK")), h.Str("Any Go templating (html/template, templ, gomponents)"), h.Str("Datastar"),
				h.Str("Signals in the browser; the server holds what you give it"),
				h.Str("@post('/path') strings; handlers routed by hand")},
			[]h.H{h.Strong(h.Str("Go API + React SPA")), h.Str("JSX/TSX"), h.Str("React plus your app bundle"),
				h.Str("Client memory; the server holds data, not UI"),
				h.Str("JSON endpoints; typed across the boundary only with codegen (OpenAPI, protobuf)")},
			[]h.H{h.Strong(h.Str("Phoenix LiveView")), h.Str("HEEx templates in Elixir"), h.Str("phoenix_live_view.js"),
				h.Str("Assigns in one server process per connected view"),
				h.Str("phx-* event name strings matched in handle_event")},
			[]h.H{h.Strong(h.Str("Hotwire (Rails)")), h.Str("ERB with Turbo Frames and Streams"), h.Str("Turbo, plus Stimulus for JS"),
				h.Str("Server data per request; no per-connection view state"),
				h.Str("Routes and data-controller / data-action strings")},
		),
		h.P(h.Str("via's client is Datastar, so the Datastar row shares its runtime and transport. What via adds is the Go layer: "+
			"state scoped by field type, actions addressed by method, and a stream opened only where a unit needs one. "+
			"The closest Go-native alternative is templ + htmx, which types the templates but leaves the state wiring to strings and handlers you route.")),
		h.P(h.Str("Build step: via needs none, and Datastar needs only templ's if you use templ. templ + htmx runs templ generate, "+
			"React needs npm and a bundler, LiveView builds with Mix and esbuild, and Hotwire uses import maps or a bundler.")),
		h.P(h.Str("Server push: via and Datastar use SSE, via only on pages with a live unit. htmx adds SSE or WebSocket through extensions. "+
			"LiveView runs over a WebSocket with a long-poll fallback and clusters through Phoenix.PubSub. Hotwire sends Turbo Streams over "+
			"WebSocket (Action Cable) or SSE. A React SPA pushes with whatever you add.")),
	)
}
