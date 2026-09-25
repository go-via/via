package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/shell"
	"go-via.dev/site/snippet"
)

// Troubleshooting is symptom → cause → fix, one H2 per symptom. Every quoted
// status, body and log line is reproduced by snippet/src/troubleshooting's tests.
type Troubleshooting struct{ page }

func NewTroubleshooting(env Env) Troubleshooting {
	return Troubleshooting{page: newPage("/troubleshooting", env)}
}

func (p *Troubleshooting) PageMeta() via.Meta {
	return p.meta("Common via errors and panics, what causes each, and the fix.")
}

const tsForbidden = `403 forbidden origin`

const tsGoneBodies = `410 no such action cGAmF8Xg; this render does not bind it
410 this render does not bind that action for that argument
410 no such child
410 this tab id has no open stream for this page: the connection closed, or the id belongs to a page that is gone — reload
410 this action needs the page's tab id and the request carried no viatab signal: the page never opened its stream, or the id was filtered out`

const tsGoneLog = `level=WARN msg="via: no such action; this render binds others" act=cGAmF8Xg binds="Of-LgGbQ (main.(*Todos).Delete-fm)"
level=WARN msg="via: the action is bound, but not for this arg" act=main.(*Todos).Delete-fm arg="\"2\"" bound=1`

const tsMadeLive = `500 action failed
level=ERROR msg="via: action panic" err="via: this action made a unit live on a page that was served plain — the tab has no stream. Every action after this one would 410. Liveness must not depend on a branch an action can open: render the State unconditionally, or register a Tick/Listen in OnInit so the page is live from the first render"`

const tsLiveNested = `500 render failed
level=ERROR msg="via: render panic" err="via: via.Child: a live unit cannot sit inside another live unit — embed it directly from a plain ancestor instead"`

const tsChildPointer = `500 render failed
level=ERROR msg="via: render panic" err="via: via.Child(child) requires child to have a View() method"`

const tsMisnamed = `level=WARN msg="via: main.Room.Oninit looks like a mis-named OnInit — it has the hook's exact signature but main.Room has no OnInit func(*via.Ctx) error, so nothing will ever call it. Rename it to OnInit."`

const tsMountPanics = `panic: via: main.Room.OnInit has signature func(*via.Ctx), not func(*via.Ctx) error — so the hook will never run
panic: via: main.Search.View has a VALUE receiver and the composition holds Signals — View must take a POINTER receiver (func (p *Search) View() h.H), or every rendered Signal binds against a discarded copy
panic: via: Mount path "/files/{path...}": a {name...} wildcard is not supported — a page's action and stream routes live under its path`

const tsTooLarge = `413 request body too large`

const tsUnavailable = `503 stream capacity reached
level=WARN msg="via: refusing an SSE connect with 503: the router is at its live-stream cap. There is no per-IP share of that cap, so a single client can hold all of it; every tab is refused until one closes. The limit is WithMaxSSEConn — raise it only with the memory to back it." open=10000 cap=10000

503 stream busy
level=WARN msg="via: live action queue not drained — the connection's goroutine is blocked inside a Tick, Listen or action handler, so its keepalives have stopped too and every action on this tab answers 503 until it returns. Move blocking work off the handler."`

const tsEval = `EvalError: Refused to evaluate a string as JavaScript because 'unsafe-eval' is not an allowed source of script`

func tsSee(d *shell.Doc, path, label string) h.H {
	return h.P(h.Str("See "), h.A(h.Href(d.Href(path)), h.Str(label)), h.Str("."))
}

func (p *Troubleshooting) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("Each section is a symptom as you would see it: a status, a body, a log line or a panic. "+
			"The quoted text is what via prints.")),

		d.H2("Every action answers 403 forbidden origin"),
		snippet.Text("response", tsForbidden),
		h.P(API("via.WithTrustedOrigin"), h.Str(" turned the origin check on and the browser's request fails it. "+
			"The allowlist compares exact strings, so the usual misses are:")),
		h.Ul(
			h.Li(h.Str("A different spelling: "), Code("http://"), h.Str(" for "), Code("https://"), h.Str(", "),
				Code("www."), h.Str(", a port, a trailing slash.")),
			h.Li(h.Str("A sibling subdomain: its "), Code("Sec-Fetch-Site"), h.Str(" is "), Code("same-site"),
				h.Str(", which fails. List it.")),
			h.Li(h.Str("A proxy that strips "), Code("Sec-Fetch-*"), h.Str(" or rewrites "), Code("Host"),
				h.Str(" to the upstream address. nginx needs "), Code("proxy_set_header Host $host;"), h.Str(".")),
			h.Li(h.Str("An empty origin read from the environment, like the "), Code("VIA_ORIGIN"),
				h.Str(" the Deploy page's main.go reads, so the listed value is not the site's.")),
		),
		snippet.Region("troubleshooting/limits.go", "origin", snippet.Title("main.go"), snippet.Mark("app.example.com")),
		h.P(h.Str("A 403 with the body "), Code("session mismatch"), h.Str(" is a different check: a live action carried a "+
			"different session than the one its tab connected with. The cookie expired or was cleared, or another app on "+
			"the same host overwrote it; give each app its own "), API("via.WithSessionCookieName"), h.Str(". A bare "),
			Code("forbidden"), h.Str(" is your "), Code("OnInit"), h.Str(" or "), Code("OnReload"), h.Str(" returning "),
			API("via.ErrForbidden"), h.Str(".")),
		tsSee(d, "/security#origin-checks-and-csrf", "Sessions & security: Origin checks and CSRF"),

		d.H2("The page never updates live"),
		h.P(h.Str("Clicks POST and answer 200, or a Tick runs on the server, and the page does not change. Check in this order:")),
		h.Ul(
			h.Li(h.Strong(h.Str("No live unit. ")), h.Str("A page streams only if a unit renders a "), API("via.State"),
				h.Str(" or "), API("via.List"), h.Str(", or registers "), API("via.Ctx.Tick"), h.Str(" or "),
				API("via.Ctx.Listen"), h.Str(" in "), Code("OnInit"), h.Str(". A page of Signals is plain: an action's "+
					"response patches it, and nothing else can.")),
			h.Li(h.Strong(h.Str("The proxy buffers the stream. ")), h.Str("Caddy needs "), Code("flush_interval -1"),
				h.Str(" on the "), Code("reverse_proxy"), h.Str("; nginx needs "), Code("proxy_buffering off;"),
				h.Str(". Compression middleware on "), Code("text/event-stream"), h.Str(" holds frames back the same way.")),
			h.Li(h.Strong(h.Str("An idle timeout under 25 s. ")), h.Str("via writes a keepalive every 25 seconds, fixed. "+
				"A proxy or balancer that closes a silent connection sooner drops every quiet stream; 60 s is enough.")),
			h.Li(h.Strong(h.Str("The State sits behind a closed branch. ")), h.Str("The GET rendered no State, so the page was "+
				"served plain, and the action that opens the branch fails:")),
		),
		snippet.Text("response and log", tsMadeLive),
		h.P(h.Str("Render the State outside the "), API("via.When"), h.Str(", or register the Tick or Listen in "),
			Code("OnInit"), h.Str(" so the unit is live from the first render.")),
		tsSee(d, "/deploy", "Deploy"),

		d.H2("Clicks answer 410 Gone"),
		snippet.Text("response", tsGoneBodies),
		snippet.Text("log", tsGoneLog),
		h.P(h.Str("An action is dispatchable only if the render before it bound that handler, with that argument, in that "+
			"child. A 410 means the render the server last ran no longer contains what the client clicked: the button "+
			"sits inside a "), API("via.When"), h.Str(" that is now closed, the "), API("on.WithArg"),
			h.Str(" argument changed since the page was drawn, or the "), API("via.Child"),
			h.Str(" moved. The two stream messages are a live unit's action with no open stream behind it.")),
		h.Ul(
			h.Li(h.Str("Bind a stable key as the argument, a row's primary key, never a position, count or cursor.")),
			h.Li(h.Str("Gate "), API("via.When"), h.Str(" on session or database state, and keep any When around a "),
				API("via.Child"), h.Str(" fixed for the life of the page: it shifts later siblings' keys.")),
			h.Li(h.Str("On a plain page, a handler that exists only inside a branch opened by a bound Signal always 410s: "+
				"the render that authorises runs before the posted signals apply. Move the handler outside the branch.")),
		),
		h.P(h.Str("The browser reloads the tab on its own after a 410, with a \"Page is out of date - reloading…\" banner.")),
		tsSee(d, "/actions#arguments", "Actions: Arguments"),

		d.H2("A Disconnected banner, or the tab keeps reloading"),
		h.P(h.Str("via's reconnect script shows a banner at the top of the page. What each state means:")),
		table([]string{"Banner", "Cause", "What happens next"},
			[]h.H{h.Str("Reconnecting…"), h.Str("The network dropped the stream."), h.Str("The browser retries; the banner clears on the next patch.")},
			[]h.H{h.Str("Disconnected."), h.Str("The stream ended cleanly: a deploy, a restart, Router.Close."),
				h.Str("Probes the page URL with backoff (0.5 s to 8 s) and reloads once it answers.")},
			[]h.H{h.Str("Page is out of date - reloading…"), h.Str("A request answered 410."), h.Str("Probes, then reloads.")},
			[]h.H{h.Str("Disconnected., and it stays"), h.Str("A request answered 403 or 5xx; or two automatic reloads already; or 20 probes failed."),
				h.Str("Nothing until you press Reconnect, which clears the reload count.")},
		),
		h.P(h.Str("Every banner but Reconnecting… carries a Reconnect button that probes at once.")),
		h.P(h.Str("A banner right after every deploy is expected; state that lives only in a "), API("via.State"),
			h.Str(" starts over, since a reconnect is a new connection. A Disconnected banner that stays on first load is a 403 or 5xx: "+
				"read the Network tab for the status and find it on this page. The reload count clears 5 seconds after a "+
				"load, so a server that fails straight after every reload stops at two.")),
		tsSee(d, "/live#connection-lifecycle", "Live state: Connection lifecycle"),

		d.H2("OnConnect or OnDispose never runs"),
		snippet.Region("troubleshooting/hooks.go", "hooks", snippet.Title("room.go"), snippet.Mark("ctx.OnConnect", "ctx.OnDispose")),
		h.P(h.Str("Besides "), Code("View"), h.Str(" and "), Code("PageMeta"), h.Str(", v0.8 calls two methods: "),
			Code("OnInit"), h.Str(" and "), Code("OnReload"), h.Str(". A v0.7 "), Code("OnConnect(ctx *via.Ctx) error"), h.Str(" or "), Code("OnDispose(ctx *via.Ctx)"),
			h.Str(" method still compiles and is never called, and on a type that has "), Code("OnInit"),
			h.Str(" via says nothing. Register both from "), Code("OnInit"), h.Str(" with "), API("via.Ctx.OnConnect"),
			h.Str(" and "), API("via.Ctx.OnDispose"), h.Str(". They run only on a unit something else made live; on a plain "+
				"unit there is no connection to open or close.")),
		h.P(h.Str("A method named like a hook (Init, Oninit, Refresh, a v0.7 OnConnect) with the hook's exact signature, on a "+
			"type that lacks the hook, warns once:")),
		snippet.Text("log", tsMisnamed),
		tsSee(d, "/migrate", "Migrating from v0.7"),

		d.H2("Mount panics at startup"),
		snippet.Text("panic", tsMountPanics),
		h.P(h.Str("Registration mistakes panic when the router is built, before it serves: a hook with the wrong signature, a "),
			Code("View"), h.Str(" with a value receiver on a type holding Signals, a "), Code("{name...}"),
			h.Str(" wildcard or a reserved path segment, a session key under 16 bytes, two mounts on one path. The message "+
				"names the type or the pattern; fix that and restart.")),
		tsSee(d, "/compositions", "Compositions"),

		d.H2("A page answers 500 render failed"),
		h.P(h.Str("A composition mistake that only a render can see. The body is always "), Code("render failed"),
			h.Str("; the log line says which.")),
		d.H3("A live unit inside another live unit"),
		snippet.Text("response and log", tsLiveNested),
		h.P(h.Str("A live unit may not contain another live unit, and a live child's View may not call "), API("via.Child"),
			h.Str(" at all. Keep the live units as siblings under a plain parent:")),
		snippet.Region("troubleshooting/compose.go", "siblings", snippet.Title("dashboard.go"), snippet.Mark("via.Child")),
		d.H3("via.Child given a pointer"),
		snippet.Text("response and log", tsChildPointer),
		h.P(API("via.Child"), h.Str(" takes the child by value and calls "), Code("View"), h.Str(" on its own copy, so "),
			Code("via.Child(&p.Chat)"), h.Str(" hands it a "), Code("**Chat"), h.Str(". Pass the field: "),
			Code("via.Child(p.Chat)"), h.Str(".")),
		tsSee(d, "/compositions", "Compositions"),

		d.H2("Rows keep the wrong state after a delete or reorder"),
		snippet.Region("troubleshooting/rows.go", "rows", snippet.Title("todos.go"), snippet.Mark(`h.ID("todo-"+strconv.Itoa(t.ID))`, "t.ID)")),
		h.P(h.Str("Datastar morphs a re-rendered list by position. Delete the second of three rows without ids and the "+
			"browser keeps the second element and rewrites its text, so a ticked checkbox, typed input or open "),
			Code("<details>"), h.Str(" now belongs to the row below. A stable "), Code("id"),
			h.Str(" per row makes the morph match by id. The click itself is not affected: "), API("on.WithArg"),
			h.Str(" carries the row's key.")),
		tsSee(d, "/actions#arguments", "Actions: Arguments"),

		d.H2("An upload answers 413 request body too large"),
		snippet.Text("response", tsTooLarge),
		h.P(h.Str("Two caps: "), API("via.WithMaxBody"), h.Str(" (1 MiB) for a JSON action body, and "), API("via.WithMaxUpload"),
			h.Str(" (8 MiB) for the whole multipart body of a "), API("via.PostForm"), h.Str(" submit. Raise the one that "+
				"matches; a proxy in front may have its own limit, such as nginx's "), Code("client_max_body_size"), h.Str(".")),
		snippet.Region("troubleshooting/limits.go", "limits", snippet.Title("main.go")),
		tsSee(d, "/security#request-limits", "Sessions & security › Request limits"),

		d.H2("Connects or actions answer 503"),
		snippet.Text("response and log", tsUnavailable),
		h.P(Code("stream capacity reached"), h.Str(" is the router at "), API("via.WithMaxSSEConn"),
			h.Str(" (10,000 open streams). The cap is router-wide with no per-client share; raise it only with the memory for "+
				"one goroutine and one composition tree per stream.")),
		h.P(Code("stream busy"), h.Str(" is one tab's goroutine stuck in a handler for longer than "), API("via.WithPinnedDeadline"),
			h.Str(" (5 s). Tick, Listen and actions on a tab run one at a time, so a blocking call in any of them stalls the "+
				"rest. Do slow work in a goroutine and publish the result back through a topic. A 503 with "),
			Code("session store unavailable"), h.Str(" is a "), API("via.SessionStore"), h.Str(" that failed to load.")),
		tsSee(d, "/security#request-limits", "Sessions & security › Request limits"),

		d.H2("EvalError: Refused to evaluate a string as JavaScript"),
		snippet.Text("console", tsEval),
		h.P(h.Str("Datastar's bindings need "), Code("'unsafe-eval'"), h.Str(", and via's own policy has it. The error means a second policy "+
			"reached the browser, from middleware or the proxy (nginx "), Code("add_header"), h.Str(", Caddy "),
			Code("header"), h.Str("), and browsers enforce every policy they receive. Remove the extra header, or add "),
			Code("'unsafe-eval'"), h.Str(" to its "), Code("script-src"), h.Str(". Asset origins belong in "),
			APIText("via.Meta", "PageMeta().Assets"), h.Str(", which via folds into its own policy.")),
		tsSee(d, "/security#content-security-policy", "Sessions & security › Content-Security-Policy"),

		d.H2("Copy to clipboard does nothing"),
		h.P(API("expr.CopyToClipboard"), h.Str(" and "), API("expr.CopyTextOf"), h.Str(" call "),
			Code("navigator.clipboard.writeText"), h.Str(" and do not await it, so a refused write fails with only a "+
				"console error. Browsers allow it in a secure context (HTTPS, or "), Code("localhost"),
			h.Str("), from a user gesture, in a focused document. Over plain HTTP on a LAN address "),
			Code("navigator.clipboard"), h.Str(" is undefined; from "), API("on.LoadCS"), h.Str(" or a background tab the write "+
				"is refused. Bind it to "), API("on.ClickCS"), h.Str(" and serve over HTTPS.")),
		tsSee(d, "/signals#expressions", "Signals: Expressions"),
	)
}
