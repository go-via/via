package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
)

// Actions is the page on wiring clicks and form submits to Go methods.
type Actions struct {
	page
	Counter demos.Counter
	Vote    demos.Vote
	Note    demos.Note
	Signup  demos.Signup
	Upload  demos.Upload
	Search  demos.Search
}

func (p *Actions) PageMeta() via.Meta {
	m := p.meta("Events as Go methods: package on, action signatures, on.WithArg, on.Submit and PostForm, uploads, modifiers, concurrency and error statuses.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

// NewActions builds the limiters once, so every request's copy of the page
// spends from the same per-client buckets.
func NewActions(env Env) Actions {
	return Actions{
		page:   newPage("/actions", env),
		Vote:   demos.NewVote(demo.NewLimiter(30)),
		Upload: demos.NewUpload(demo.NewLimiter(10)),
	}
}

func (p *Actions) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("An action is a method on your composition. An event in the browser POSTs to it, the method changes "+
			"state, and via answers with patches for what changed. There is no route to name and no JSON to decode.")),

		d.H2("Handling a click"),
		demo.Card(d.H3("Counter"),
			h.P(API("on.Click"), h.Str(" takes the method value "), Code("c.Inc"), h.Str(". via addresses it by the method's name "+
				"and the receiver's place in the page, so the POST goes to this child alone and the answer patches this "+
				"region. The other demos on the page are untouched.")),
			via.Child(p.Counter), "counter.go", demo.WireOpen(),
			demo.Try(
				h.Span(h.Str("Click + and read the POST's URL in the Wire pane: "), Code("/actions/_via/a/{child}/{id}"), h.Str(".")),
				h.Str("Reload the page: the count starts at 0 again, because a State lives on this tab's connection."),
			)),
		h.P(h.Str("Every DOM event binds the same way: "), API("on.Change"), h.Str(", "), API("on.Keydown"), h.Str(", "),
			API("on.Submit"), h.Str(", and "), API("on.Event"), h.Str(" for an event without its own function. The "),
			Code("…CS"), h.Str(" twins, such as "), API("on.ClickCS"), h.Str(", run an expression in the browser and post nothing; they are on "),
			h.A(h.Href(d.Href("/signals")), h.Str("Signals")), h.Str(".")),

		d.H2("Action signatures"),
		snippet.Region("actions/signatures.go", "signatures", snippet.Mark("Clear(ctx *via.Ctx)", "Delete(ctx *via.Ctx, id int)")),
		table([]string{"Handler", "Result"},
			[]h.H{Code("func (t *T) M(ctx *via.Ctx)"), h.Span(h.Str("Bind with "), Code("on.Click(t.M)"), h.Str("."))},
			[]h.H{Code("func (t *T) M(ctx *via.Ctx, v V)"), h.Span(h.Str("Bind with "), Code("on.Click(on.WithArg(t.M, v))"), h.Str("; see Arguments below."))},
			[]h.H{h.Str("A return value, no Ctx, a second argument without WithArg"), h.Span(h.Str("Does not compile: the binders accept "), Code("func(*via.Ctx)"), h.Str(" or an "), API("on.Bound"), h.Str(" only."))},
			[]h.H{h.Str("A func literal in a loop or an Each row"), h.Str("Compiles, then panics at Mount, or at the first render if the empty value has no rows: every copy has the same Go name, so via cannot tell them apart. Use a method and WithArg.")},
			[]h.H{h.Str("A method taken through an interface field, or a value receiver whose type sits at two fields or at none"), h.Str("Compiles, then panics at Mount, or at the first render if the empty value does not reach it: via cannot tell which value the method belongs to. Give the method a pointer receiver and hold the concrete type in the field; a value receiver on the page, or on a type only one field holds, works.")},
		),
		h.P(h.Str("An action returns nothing. Report a failure the user can fix by setting state, such as an error Signal; a panic is a 500. "+
			"Mount checks the lifecycle hooks' signatures, not actions': the two rows above are the only action checks, and a render has to reach the binding to run them.")),
		h.P(h.Str("An action's id is the method's Go name plus the path of the field it was called on, such as "), Code("Stats.A"),
			h.Str(". A deploy that reorders fields keeps every button's id; one that renames a field answers 410 to a tab still holding the old name.")),

		d.H2("What an action body can do"),
		snippet.Region("actions/body.go", "body", snippet.Mark("ctx.Session()", "ctx.Redirect", "ctx.Param", "ctx.Context()")),
		table([]string{"Call", "Gives you"},
			[]h.H{API("via.Ctx.Request"), h.Str("The POST's headers, cookies and RemoteAddr. Read-only: the body is already decoded. On a PostForm submit, FormValue and FormFile.")},
			[]h.H{API("via.Ctx.Session"), h.Span(h.Str("The browser session: "), API("via.Session.Get"), h.Str(", "), API("via.Session.Put"), h.Str(", and "), API("via.Session.Rotate"), h.Str(" after a login."))},
			[]h.H{API("via.Ctx.Param"), h.Str("A {name} segment of the mount path, decoded into T; one that does not decode answers 404. The query string is empty on every action.")},
			[]h.H{API("via.Ctx.Context"), h.Str("A context to pass to slow calls. On a live unit it is cancelled when the tab disconnects.")},
			[]h.H{API("via.Ctx.Redirect"), h.Str("Navigate once the handler returns, on this site only. The answer skips OnReload and the render.")},
			[]h.H{API("via.Ctx.RedirectExternal"), h.Str("The same, to another site: an OAuth provider, a payment page.")},
		),
		h.P(h.Str("The rest is your composition: "), API("via.Signal.Set"), h.Str(", "), API("via.State.Set"), h.Str(", "),
			API("via.List.Append"), h.Str(", or a plain field, and the render that answers the action shows it. "+
				"There is no response writer; a download is a separate net/http handler that the page links to. "),
			API("via.Ctx.Tick"), h.Str(" and "), API("via.Ctx.Listen"), h.Str(" register only in OnInit; in an action they log and do nothing. "+
				"To re-read a store after the action wrote to it, implement OnReload, which is covered on "),
			h.A(h.Href(d.Href("/compositions")), h.Str("Compositions")), h.Str(".")),

		d.H2("Arguments"),
		demo.Card(d.H3("Vote"),
			h.P(APIText("on.WithArg", "on.WithArg(v.Cast, r.Option)"), h.Str(" puts the row's option index in the action URL as JSON, and "),
				Code("Cast"), h.Str(" receives it as an int. Dispatch accepts only an argument that the render before it bound for "+
					"that handler, and answers 410 to any other before Cast runs, so the handler can index with it. "+
					"The tallies are shared with everyone reading this, hence the cap.")),
			via.Child(p.Vote), "vote.go",
			demo.Try(
				h.Span(h.Str("Vote for Go, then expand the POST in the Wire pane: its URL ends in "), Code("?a=0"), h.Str(".")),
				h.Str("Vote in a second tab. This tab shows the new tally on its next vote, not before: nothing pushes it."),
			)),
		Callout(Warning, "The argument is an identity",
			h.P(h.Str("Bind a stable key, such as a row's primary key, never a value that moves between renders. "+
				"A cursor, a count or a position goes stale as soon as a render rebinds the button, and the click already "+
				"in flight answers 410. An index into a list that changes can also land on a different row with no error at all. "+
				"Read changing state from the composition in an argless handler instead.")),
			h.P(h.Str("Vote binds the index because its options are a fixed package-level slice: index 2 is Zig in every render, for every visitor.")),
		),

		d.H2("Forms: on.Submit or PostForm"),
		demo.Card(d.H3("In-place submit"),
			h.P(API("on.Submit"), h.Str(" on a "), Code("<form>"), h.Str(" stops the native submit and posts the tab's signals as JSON, "+
				"so the text box is a bound Signal and "), Code("Add"), h.Str(" reads it with Get. The answer is a patch: the note appears, "+
				"Set(\"\") empties the box, and focus and scroll stay where they were.")),
			via.Child(p.Note), "actions_note.go",
			demo.Try(
				h.Str("Submit an empty box: the error arrives as a signal patch."),
				h.Str("Add six notes: the list keeps the last five, and the page never reloads."),
			)),
		demo.Card(d.H3("Native submit"),
			h.P(API("via.PostForm"), h.Str(" renders a native multipart form, so the submit is a real navigation and the handler "+
				"reads fields with "), Code("ctx.Request().FormValue"), h.Str(". Validation is server-side, and the page comes back with "+
				"the errors and whatever was typed.")),
			via.Child(p.Signup), "signup.go",
			demo.Try(
				h.Str("Submit with one letter in Name: the page reloads with the error and your input kept."),
				h.Str("Sign up with valid input: the counter above is back to 0, because the whole document was replaced."),
			)),
		table([]string{"", "on.Submit", "via.PostForm"},
			[]h.H{h.Str("Request"), h.Str("Datastar fetch, signals as JSON"), h.Str("browser form POST, multipart")},
			[]h.H{h.Str("Handler reads"), h.Str("bound Signals, with Get"), Code("ctx.Request().FormValue")},
			[]h.H{h.Str("Answer"), h.Str("patches for what changed"), h.Str("a whole new document")},
			[]h.H{h.Str("ctx.Redirect"), h.Str("a navigation script Datastar runs"), h.Str("303 See Other")},
			[]h.H{h.Str("Files"), h.Str("no"), h.Str("yes")},
			[]h.H{h.Str("Body cap"), h.Span(API("via.WithMaxBody"), h.Str(", 1 MiB")), h.Span(API("via.WithMaxUpload"), h.Str(", 8 MiB"))},
		),
		h.P(h.Str("Use on.Submit for anything that stays on the page. Use PostForm for sign-in, uploads, and flows that end in a redirect.")),

		d.H3("PostForm with bound Signals"),
		snippet.Region("actions/formsignals.go", "formsignals", snippet.Title("profile.go"), snippet.Mark("h.Name(", "FormValue", "Set(")),
		h.P(h.Str("A form can keep Signals on its inputs, so an "), API("on.Change"),
			h.Str(" reshapes it before the submit: here the country picks the region list. The submit is native: "+
				"it posts fields by name, not the signal store, so in the handler "), API("via.Signal.Get"),
			h.Str(" returns the initial value. Give each bound input an "), API("h.Name"),
			h.Str(", read it with "), Code("FormValue"), h.Str(", and "), API("via.Signal.Set"),
			h.Str(" the Signal from it. The answer renders those values, with the error beside them.")),
		Callout(Caveat, "Plain units only",
			h.P(h.Str("In a plain unit the submit answers with the instance the handler changed. "+
				"In a live unit it answers with a fresh page, as a reconnect would, and what the handler set is gone. "+
				"Keep the form in a plain unit, or end the handler with a Redirect."))),

		d.H2("File uploads"),
		demo.Card(d.H3("Inspect a file"),
			h.P(h.Str("PostForm always sends "), Code("multipart/form-data"), h.Str(", so "), Code("ctx.Request().FormFile"),
				h.Str(" works in the handler. This one hashes the bytes, sniffs the type from the first 512, and keeps nothing.")),
			via.Child(p.Upload), "actions_upload.go",
			demo.Try(
				h.Str("Upload a PNG renamed to .txt: the sniffed type ignores the name and the claimed type."),
				h.Str("Submit with no file chosen."),
			)),
		h.P(h.Str("A handler that keeps the file names it and checks it itself:")),
		snippet.Region("actions/upload.go", "upload", snippet.Mark("FormFile", "DetectContentType", "CreateTemp")),
		snippet.Region("actions/upload.go", "router", snippet.Mark("WithMaxUpload", "WithMaxBody")),
		h.P(API("via.WithMaxUpload"), h.Str(" caps the whole multipart body (default 8 MiB), and a larger one answers 413 before the handler runs. "),
			API("via.WithMaxBody"), h.Str(" caps how much of it is held in memory (default 1 MiB); via spills the rest to temp files and removes them after the handler returns.")),
		Callout(Warning, "Filename and Content-Type are client input",
			h.P(h.Str("The header's Filename can hold "), Code("../"), h.Str(" and its Content-Type is whatever the browser claims. "+
				"Name the file yourself and sniff the bytes."))),

		d.H2("Event modifiers"),
		demo.Card(d.H3("Live search"),
			h.P(API("on.Input"), h.Str(" fires on every keystroke. "), APIText("on.Debounce", "on.Debounce(300*time.Millisecond)"),
				h.Str(" holds the POST until typing stops for 300 ms. The box is a bound Signal, so the handler reads the latest text whatever the timing.")),
			via.Child(p.Search), "actions_search.go",
			demo.Try(
				h.Span(h.Str("Type "), Code("sync"), h.Str(" quickly and count the POSTs in the Wire pane: one, not four.")),
				h.Str("Type, pause, type again: one POST per pause."),
			)),
		table([]string{"Option", "Effect", "Datastar modifier"},
			[]h.H{API("on.Debounce"), h.Str("Runs once the event has stopped firing for d."), Code("__debounce.300ms")},
			[]h.H{API("on.Throttle"), h.Str("Runs at most once per d."), Code("__throttle.300ms")},
			[]h.H{API("on.Once"), h.Str("Removes the listener after its first run."), Code("__once")},
			[]h.H{API("on.Prevent"), h.Str("Calls preventDefault. Not needed with on.Submit."), Code("__prevent")},
			[]h.H{API("on.Stop"), h.Str("Calls stopPropagation."), Code("__stop")},
			[]h.H{API("on.Outside"), h.Str("Fires only for events whose target is outside the element."), Code("__outside")},
			[]h.H{API("on.Window"), h.Str("Listens on window instead of the element."), Code("__window")},
		),
		h.P(h.Str("The options take the same form on the …CS twins. A duration of zero or less panics, and so do two different durations for one option on the same binding.")),

		d.H2("Concurrency"),
		h.P(h.Str("On a plain unit every action POST gets its own copy of the composition and runs on its own net/http goroutine. "+
			"Two POSTs from one tab never share memory, but they can run at the same time and finish in either order.")),
		h.P(h.Str("On a live unit, one that renders a State or List or registers a Tick or Listen, every action, Tick and Listen handler "+
			"for the tab runs on that tab's stream goroutine, one at a time, and the POST waits for its turn.")),
		h.P(h.Str("Neither protects what you share outside the composition. A package-level map, a store, or a tally every visitor sees needs its own lock, as Vote's does. "+
			"In the browser, Datastar aborts an in-flight request when the same binding fires again, so a double click reads only the second answer; the first may still have run on the server.")),
		Callout(Caveat, "A slow action holds its tab",
			h.P(h.Str("On a live unit a handler that blocks stalls every other action and tick for that tab. A POST still waiting after "),
				API("via.WithPinnedDeadline"), h.Str(" (default 5s) answers 503. Pass "), API("via.Ctx.Context"),
				h.Str(" to slow calls, or hand the work to a goroutine that publishes the result to a topic."))),

		d.H2("Errors and panics"),
		table([]string{"Status", "When"},
			[]h.H{h.Str("400"), h.Str("The ?a= argument does not decode into the handler's type, or the body is malformed. A JSON body without Datastar-Request: true is read as a form, and the 400 names the header.")},
			[]h.H{h.Str("403"), h.Span(h.Str("The Origin is not trusted (see "), API("via.WithTrustedOrigin"), h.Str("), or the tab's stream is bound to another session."))},
			[]h.H{h.Str("404"), h.Span(h.Str("A path segment does not decode for ctx.Param, or OnReload returned "), API("via.ErrNotFound"), h.Str("."))},
			[]h.H{h.Str("410"), h.Str("The render does not bind this action, or not with this argument, or the tab's stream is gone.")},
			[]h.H{h.Str("413"), h.Str("The body is over WithMaxBody, or a native submit is over WithMaxUpload.")},
			[]h.H{h.Str("500"), h.Str("The handler panicked, or OnReload returned any other error. The panic is logged with its stack, and the process keeps serving.")},
			[]h.H{h.Str("503"), h.Str("The session store did not answer, a live tab's goroutine did not pick the action up in time, too many actions are already waiting for their streams, or the router is shutting down.")},
		),
		h.P(h.Str("An action posted by Datastar leaves the page as it was. On a page with a live unit, via's reconnect script reacts: "+
			"a 410 shows \"Page is out of date\" and reloads, and a 403 or 5xx shows \"Disconnected.\" with a Reconnect button. "+
			"On a page with no live unit nothing is shown; listen for the "), Code("datastar-fetch"), h.Str(" event of type "), Code("error"),
			h.Str(" to show your own notice. A PostForm submit navigates to the error response: plain text, or the document "),
			API("via.WithErrorPage"), h.Str(" renders.")),
	)
}
