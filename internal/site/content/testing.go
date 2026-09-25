package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/snippet"
)

type Testing struct{ page }

func NewTesting(env Env) Testing { return Testing{page: newPage("/testing", env)} }

func (p *Testing) PageMeta() via.Meta {
	return p.meta("Test via pages with vt in-process and with vtbrowser in headless Chromium.")
}

func (p *Testing) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("Tests drive a composition through the same HTTP path the server uses, so the router, the origin "+
			"check, the session cookie and the SSE stream all run. There is no direct method seam: a test asserts on "+
			"status codes, rendered HTML and stream frames, never on internal state. "), Code("vt"),
			h.Str(" does this in-process with no browser; "), Code("vtbrowser"),
			h.Str(" does it in headless Chromium, for what only a browser can show.")),

		d.H2("Test a page without a browser"),
		snippet.Region("testing/plain.go", "counter", snippet.Title("counter.go")),
		snippet.Region("testing/plain.go", "test", snippet.Title("counter_test.go"), snippet.Mark("vt.Serve", "Action(1)")),
		h.P(API("vt.Serve"), h.Str(" mounts any "), Code("http.Handler"), h.Str(" on an in-memory "), Code("httptest"),
			h.Str(" server and closes it when the test ends. "), API("vt.App.Get"), h.Str(" returns the status and body; a render "+
				"that panics comes back as a 500, not a transport error.")),
		h.P(API("vt.App.Action"), h.Str(" addresses an action by position: "), Code("Action(n)"),
			h.Str(" is the n-th action the root renders, in document order. vt reads the action's URL off the rendered page "+
				"when the action fires, because the wire id is a hash of the handler and cannot be built by hand. "),
			API("vt.Action.Fire"), h.Str(" returns the status and the body, which on a plain page is the Datastar patch.")),
		Callout(Caveat, "Positions move with the View",
			h.P(h.Str("Adding a button above another shifts every index after it. That is the cost of addressing by "+
				"render order; a comment naming each index, as above, keeps the test readable.")),
		),

		d.H2("Actions and arguments"),
		snippet.Region("testing/args.go", "body", snippet.Title("stepper_test.go"), snippet.Mark("Body(")),
		h.P(API("vt.Action.Body"), h.Str(" sets the JSON the browser would post: the page's signals, keyed by wire name. "+
			"The handler reads them with "), API("via.Signal.Get"), h.Str(" as it would from a real click.")),
		snippet.Region("testing/args.go", "arg", snippet.Title("shelf_test.go"), snippet.Mark("Action(1)")),
		h.P(h.Str("An action bound with "), API("on.WithArg"),
			h.Str(" renders one URL per row, each carrying its own argument. Picking a row by position picks its argument; "+
				"there is nothing to encode.")),
		snippet.Region("testing/args.go", "child", snippet.Title("layout_test.go"), snippet.Mark("ChildAction")),
		h.P(API("vt.App.ChildAction"), h.Str(" does the same inside a child. The key is the child's container: "),
			Code(`"0"`), h.Str(" for the root's first "), API("via.Child"), h.Str(", "), Code(`"1"`),
			h.Str(" for its second, "), Code(`"0-1"`), h.Str(" for the second child inside the first. "+
				"A child's signals carry its field name and a double underscore.")),
		snippet.Region("testing/args.go", "origin", snippet.Title("counter_test.go"), snippet.Mark("Origin(", "NoOrigin")),
		h.P(h.Str("Every action is a same-origin fetch by default. "), API("vt.Action.Origin"), h.Str(", "),
			API("vt.Action.NoOrigin"), h.Str(", "), API("vt.Action.SecFetch"), h.Str(" and "), API("vt.Action.Host"),
			h.Str(" change the headers the origin check reads. The check is on only with "), API("via.WithTrustedOrigin"),
			h.Str("; without it every origin is accepted and the test above would see 200.")),
		snippet.Region("testing/args.go", "tls", snippet.Title("counter_test.go"), snippet.Mark("vt.ServeTLS")),
		h.P(API("vt.ServeTLS"), h.Str(" serves over TLS on a loopback listener, so the request carries "), Code("req.TLS"),
			h.Str(" and an "), Code("http://"), h.Str(" Origin counts as a downgrade. Over plain "), API("vt.Serve"),
			h.Str(" the scheme is not checked, because behind a TLS-terminating proxy it is unknown.")),

		d.H2("Live units and streams"),
		snippet.Region("testing/live.go", "board", snippet.Title("board_test.go"), snippet.Mark("Connect()", "Over(conn)", "Await(")),
		h.P(API("vt.App.Connect"), h.Str(" opens the tab's SSE stream and waits for its tab id. "),
			API("vt.Action.Over"), h.Str(" routes an action to that connection. A live action answers 204 with no body; "+
				"the re-render arrives on the stream, where "), API("vt.Conn.Await"),
			h.Str(" waits up to 2s for a frame line containing the text and returns it. For a page mounted below the root, "+
				"open the stream with "), API("vt.App.ConnectAt"), h.Str(".")),
		snippet.Region("testing/live.go", "clock", snippet.Title("clock_test.go"), snippet.Mark("synctest.Test", "Peek()")),
		h.P(API("vt.Serve"), h.Str(" uses an in-memory network, so a live test runs inside "), Code("synctest.Test"),
			h.Str(" and a minute of Tick costs no wall time. "), API("vt.Conn.Peek"),
			h.Str(" reads a buffered frame without blocking, which is how to assert that something has not arrived yet: "+
				"an Await would let fake time run until it did.")),
		snippet.Region("testing/live.go", "close", snippet.Title("board_test.go"), snippet.Mark("AwaitClose")),
		h.P(API("vt.Conn.AwaitClose"), h.Str(" waits for the server to end the stream and returns what ended it: nil for "+
			"a clean close, an error for a truncated one. Use it to test graceful shutdown.")),

		d.H2("Two tabs"),
		snippet.Region("testing/tabs.go", "room", snippet.Title("room_test.go"), snippet.Mark("app.Connect(), app.Connect()", "bob.Await")),
		h.P(h.Str("Each "), API("vt.App.Connect"), h.Str(" is a new tab with its own "), API("vt.Conn.TabID"),
			h.Str(". An action over one tab that publishes on a "), API("topic.Topic"),
			h.Str(" reaches every tab listening on it, so fan-out needs no browser. "), API("vt.Action.Tab"),
			h.Str(" sets a tab id by hand, for a test that routes an action to the wrong tab on purpose.")),
		Callout(Caveat, "No cookie jar",
			h.P(h.Str("vt's client keeps no cookies, so every request starts without a session and two Connects are two "+
				"visitors, not two tabs of one. To carry a session across requests, send them through an "),
				Code("http.Client"), h.Str(" with a cookie jar and the transport of "), API("vt.App.Client"),
				h.Str(", or use "), API("vtbrowser.Session.NewTab"), h.Str(".")),
		),

		d.H2("Real browser tests"),
		h.P(API("vtbrowser.Open"), h.Str(" starts an "), Code("httptest"), h.Str(" server for your handler, launches headless "+
			"Chromium through chromedp, and loads the root. The test then runs against a live DOM with Datastar executing "+
			"under via's CSP. Use it for what vt cannot see: a click reaching the handler, an SSE patch morphing the DOM, "+
			"a bound input, focus surviving a patch, the reconnect banner.")),
		table([]string{"Helper", "Does"},
			[]h.H{h.Span(API("vtbrowser.Session.Click"), h.Str(", "), API("vtbrowser.Session.Type")), h.Str("A real mouse click; real key events, so binds and key handlers fire.")},
			[]h.H{h.Span(API("vtbrowser.Session.WaitTextContains"), h.Str(", "), API("vtbrowser.Session.WaitValue"), h.Str(", "), API("vtbrowser.Session.WaitFor")), h.Str("Poll the DOM until it matches, for up to 20s. No sleeps.")},
			[]h.H{API("vtbrowser.Session.WaitLiveConnected"), h.Str("Block until the tab's stream is open.")},
			[]h.H{API("vtbrowser.Session.WaitBoundSignal"), h.Str("Block until the Datastar signal behind an input holds a value.")},
			[]h.H{h.Span(API("vtbrowser.Session.Eval"), h.Str(", "), API("vtbrowser.Session.WaitEvalTrue")), h.Str("Run JavaScript for what the other helpers don't read.")},
			[]h.H{API("vtbrowser.Session.NewTab"), h.Str("A second tab in the same browser: same cookies, its own stream.")},
			[]h.H{API("vtbrowser.Session.Restart"), h.Str("Take the server down for a gap and bring a new handler up on the same port, like a deploy.")},
			[]h.H{API("vtbrowser.Session.RequireCleanConsole"), h.Str("Fail on any console.error or uncaught exception. End every browser test with it.")},
		),
		h.P(Code("vtbrowser"), h.Str(" is its own Go module, so chromedp never enters the dependency graph of via or of your "+
			"app unless you import it. The via repo's "),
			h.A(h.Href(repo+"vtbrowser/vtbrowser_test.go"), h.Code(h.Str("vtbrowser_test.go"))),
			h.Str(" is a working set of examples.")),
		snippet.Text("", "cd vtbrowser && VIA_CHROME=/usr/bin/chromium go test -race -tags browser ./...\n"+
			"./ci.sh --browser    # fail if no browser is found\n"+
			"./ci.sh --chrome=/path/to/chromium"),
		h.P(API("vtbrowser.Open"), h.Str(" looks for chromium, chromium-browser, chrome, google-chrome or headless-shell on "),
			Code("PATH"), h.Str(", or uses "), Code("VIA_CHROME"), h.Str(", and skips the test when it finds none. "+
				"In the via repo the browser tests carry "), Code("//go:build browser"), h.Str(", so plain "),
			Code("go test ./..."), h.Str(" never starts Chromium; "), Code("ci.sh"),
			h.Str(" runs them when a browser is present and warns when one is not.")),
		Callout(Warning, "A skip reads as a pass",
			h.P(h.Str("On a machine without Chromium every browser test skips and the run is green. In CI, run "),
				Code("./ci.sh --browser"), h.Str(" or pass "), Code("VIA_CHROME"),
				h.Str(" so a missing browser fails the job instead.")),
		),

		d.H2("What vt does not simulate"),
		h.P(h.Str("vt runs the real server, but Datastar never executes. A green vt test is necessary, not sufficient:")),
		h.Ul(
			h.Li(h.Strong(h.Str("Client-only signals are sent. ")),
				h.Str("A browser never posts a signal whose name starts with an underscore; "), API("vt.Action.Body"),
				h.Str(" posts whatever you write. A test can pass on input a real page never sends.")),
			h.Li(h.Strong(h.Str("No event modifiers or bind coercion. ")), API("on.Debounce"), h.Str(", "),
				API("on.Throttle"), h.Str(", key filters and "), Code("data-bind"),
				h.Str(" value coercion run in the browser. vt posts the action directly.")),
			h.Li(h.Strong(h.Str("Frames are text. ")), API("vt.Conn.Await"),
				h.Str(" matches a substring of one frame line, not a parsed DOM. It cannot assert structure, and it can "+
					"match a stale frame.")),
			h.Li(h.Strong(h.Str("Action URLs come from the root page. ")), API("vt.App.Action"),
				h.Str(" reads URLs from a GET of "), Code("/"), h.Str(". For a page mounted elsewhere, take the URL from its "),
				API("vt.App.Get"), h.Str(" and pass it to "), API("vt.Action.Raw"), h.Str(".")),
		),
		h.P(h.Str("For behaviour that depends on the client (client-only signals, modifiers, morphing, reconnect), "+
			"write a "), Code("vtbrowser"), h.Str(" test.")),

		d.H2("Conventions"),
		h.P(h.Str("Tests enter through exported symbols (use "), Code("package foo_test"),
			h.Str("), assert on observable output (status, HTML, stream frames, errors), and call "), Code("t.Parallel()"),
			h.Str(" wherever no mutable state is shared; a "), Code("synctest.Test"),
			h.Str(" test manages its own time and does not. Give each test its own store and "), API("topic.Topic"),
			h.Str(" in the root literal, as the samples above do, so parallel tests never share state. Prefer real or stub "+
				"implementations of interfaces you own over mocks; keep mocks for true system boundaries.")),
	)
}
