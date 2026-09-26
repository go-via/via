package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
)

// Compositions is the page on building a page out of structs: children, keys,
// hooks, and the component patterns of JS frameworks mapped onto them.
type Compositions struct {
	page
	Compose demos.Compose
	Keys    demos.ShiftPair
	Hooks   demos.HookLog
	Relay   demos.Relay
}

func NewCompositions(env Env) Compositions {
	return Compositions{
		page: newPage("/compositions", env),
		Keys: demos.NewShiftPair(),
	}
}

func (p *Compositions) PageMeta() via.Meta {
	m := p.meta("Build a via page from structs: children and their keys, the lifecycle hooks, lists, layouts, and React, Vue and Svelte patterns in via.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

func (p *Compositions) View() h.H {
	d := p.doc()
	link := func(path, label string) h.H { return h.A(h.Href(d.Href(path)), h.Str(label)) }
	return d.Page(
		h.P(h.Str("A via page is a tree of Go structs. A struct is a component: fields hold its props and state, "+
			"methods are its actions, and "), Code("View"), h.Str(" renders it.")),

		d.H2("A composition is a struct"),
		snippet.Region("compositions/component.go", "stepper"),
		h.P(h.Str("A plain field is a prop. It comes from the literal the struct was built from, which via copies for every "+
			"new instance: one per plain request, one per stream connection. "), API("via.State"), h.Str(", "),
			API("via.List"), h.Str(" and "), API("via.Signal"), h.Str(" fields are state that outlives a request: "+
				"State and List on the server, for the life of the tab's connection; Signal in the browser, posted with each action. "+
				"A method taking a "), API("via.Ctx"), h.Str(" is an action, bound with "), APIText("on.Click", "on.Click(s.Add)"),
			h.Str(". "), Code("View"), h.Str(" takes no ctx and does no I/O: fields in, markup out. It may run several times per request.")),
		demo.Card(d.H3("Two instances"),
			h.P(h.Code(h.Str("Region")), h.Str(" is one struct with its own State and action. "), h.Code(h.Str("Compose")),
				h.Str(" holds two of them as fields and renders each with "), API("via.Child"),
				h.Str(". Each gets its own container, its own state and its own action URL.")),
			via.Child(p.Compose), "compose.go", demo.WireOpen(),
			demo.Try(h.Str("Click + on left, then on right. The two counts move apart."),
				h.Str("In the Wire pane, the two POSTs differ only in the child key in their URL."))),

		d.H2("Children and keys"),
		snippet.Region("compositions/names.go", "names"),
		h.P(APIText("via.Child", "via.Child(p.Chat)"), h.Str(" renders a field as its own unit, with its own container, "),
			Code("OnInit"), h.Str(" and actions. The argument must be a field selector: Child copies the child at every render, "+
				"and a composite literal there would re-seed it each time. Value fields are copied, so each connection has its own; "+
				"pointer fields are shared, which is how two children reach one store.")),
		h.P(h.Str("A child's "), h.A(h.Href(d.Href("/glossary#positional-child-key")), h.Str("key")), h.Str(" is its position among its parent's Child calls, joined to the parent's key: "), Code("0"),
			h.Str(", "), Code("0-1"), h.Str(". The key is the container id ("), Code("#via-i0-1"),
			h.Str(") and the address in the child's action URLs. Signal names come from the field path instead: the child's field name "+
				"and a double underscore, so "), Code("Draft"), h.Str(" inside "), Code("Inbox.Chat"), h.Str(" is "),
			Code("$chat__draft"), h.Str(". Two fields of the same type cannot be told apart by type, so their signals take the "+
				"position: "), Code("$i0__chat__draft"), h.Str(".")),

		demo.Card(d.H3("Conditional children shift keys"),
			h.P(h.Str("Both halves hold a banner and a clock. The first wraps the banner's Child in "), API("via.When"),
				h.Str("; the second always calls Child and lets the banner draw nothing once dismissed. "+
					"Dismissing re-renders only the banner, so the page keeps the clock's old action URL. "+
					"In the first half the server's next render has one Child fewer ahead of the clock, the clock's position drops "+
					"from 1 to 0, and the old URL points at nothing.")),
			via.Child(p.Keys), "compositions_keys.go",
			demo.Try(h.Str("First half: read the clock, dismiss the banner, read the clock again. The Wire pane shows a 410."),
				h.Str("Second half: the same three clicks keep working."),
				h.Str("Click bring the banner back: it re-renders the whole half with fresh URLs, which repairs the first one."))),
		snippet.Region("demos/compositions_keys.go", "broken", snippet.Mark("via.When(!p.hidden, p.banner)")),
		snippet.Region("demos/compositions_keys.go", "fixed", snippet.Mark("via.Child(p.Banner)")),
		Callout(Warning, "Keep every key fixed for the life of the page",
			h.P(h.Str("A When around a Child may depend only on what the field literal or "), Code("OnInit"),
				h.Str(" fixed when the page loaded. A condition that can change while the page is open (time, a client signal, "+
					"a store another request writes) goes inside the child, or the conditional Child goes after all its siblings, "+
					"where its absence moves nobody. A stale key that lands on a unit of another type answers 410. "+
					"One of the same type answers 410 when the two come from separate Child calls in the View or a When branch; "+
					"one call that renders either copy (a loop, a variable swapped between two fields) cannot tell them apart."))),

		d.H2("Lifecycle hooks"),
		h.P(h.Str("A hook is a method with a fixed name and signature; via calls it if the type has it. "+
			"OnInit and OnReload take a *via.Ctx and return an error; View and PageMeta take nothing. "+
			"A hook name with the wrong signature panics at "), API("via.Mount"), h.Str(", or, on a child the empty page does not render, at its first render. Which hooks run depends on the request:")),
		table([]string{"Request", "Runs", "On instance"},
			[]h.H{h.Str("GET of the page"), h.Span(Code("OnInit"), h.Str(" on every unit, each right before its "), Code("View"),
				h.Str(", so a parent's runs before its children's. The root's "), Code("PageMeta"), h.Str(" names the document.")),
				h.Str("fresh, dropped after the response")},
			[]h.H{h.Str("Stream connect, when a unit is live"), h.Span(Code("OnInit"), h.Str(" on every unit again. Then each "),
				API("via.Ctx.Listen"), h.Str(" subscribes and each "), API("via.Ctx.OnConnect"), h.Str(" function runs.")),
				h.Str("fresh, kept for the connection")},
			[]h.H{h.Str("Action on a plain unit"), h.Span(Code("OnInit"), h.Str(" on every unit of the page, the handler, "),
				Code("OnReload"), h.Str(" on the acted unit, then that unit's "), Code("View"), h.Str(".")),
				h.Str("fresh, dropped after the response")},
			[]h.H{h.Str("Action on a live unit"), h.Span(h.Str("The handler, "), Code("OnReload"), h.Str(", "), Code("View"),
				h.Str("; the patch goes down the stream. No "), Code("OnInit"), h.Str(".")),
				h.Str("the connection's")},
			[]h.H{h.Str("Tick or Listen value"), h.Span(h.Str("The handler, then "), Code("View"), h.Str(".")), h.Str("the connection's")},
			[]h.H{h.Str("Stream closes"), h.Span(API("via.Ctx.OnDispose"), h.Str(" functions.")), h.Str("the connection's")},
		),
		demo.Card(d.H3("Hook log"),
			h.P(h.Str("Each line is one hook call, numbered by the instance it ran on. The log is kept per session, "+
				"so lines from instances that are gone stay visible. Until you have a session each instance shows only its own lines; "+
				"the first action here mints one.")),
			via.Child(p.Hooks), "compositions_hooks.go",
			demo.Try(h.Str("Run an action: the action and OnReload run on the stream's instance, with no OnInit."),
				h.Str("Reload the page: the old stream's OnDispose, and OnInit on two new instances, one for the GET and one for the stream."),
				h.Str("Click a button in another card, then run an action here: that POST ran this OnInit too, on a throwaway instance."))),
		table([]string{"Hook", "Use it to", "Rules"},
			[]h.H{Code("OnInit"), h.Str("Load request, session and store data into fields. Register the unit's timers and subscriptions."),
				h.Span(API("via.Ctx.Tick"), h.Str(", "), API("via.Ctx.Listen"), h.Str(", OnConnect, OnDispose and "),
					API("via.State.Track"), h.Str(" register only here; anywhere else they log and do nothing. Return "),
					API("via.ErrNotFound"), h.Str(" for a 404, "), API("via.ErrForbidden"), h.Str(" for a 403."))},
			[]h.H{Code("OnReload"), h.Str("Re-read what the action changed, before the render that answers it."),
				h.Str("Runs on the acted unit only, and not after a Redirect. Tick and Listen are ignored here.")},
			[]h.H{Code("View"), h.Str("Render."), h.Str("No ctx, no I/O, no side effects. It can run more than once per request.")},
			[]h.H{Code("PageMeta"), h.Span(h.Str("Name the document: title, description, "), API("via.Meta"), h.Str(" assets.")),
				h.Str("Read on the mounted root only. Pure: it is read at Mount and on every document render.")},
			[]h.H{APIText("via.Ctx.OnConnect", "OnConnect"), h.Str("Acquire: join a room, claim a slot."),
				h.Str("Runs once when the stream opens, after every Listen has subscribed. A Set in fn is pushed.")},
			[]h.H{APIText("via.Ctx.OnDispose", "OnDispose"), h.Str("Release what OnConnect took."),
				h.Str("Runs when the stream closes. Only a live unit has a stream to close.")},
		),
		Callout(Caveat, "OnInit runs more often than a mount effect",
			h.P(h.Str("It runs on every request that renders the unit, and an action on a plain unit renders the whole page to find it. "+
				"A plain child of a live root runs it again on every push. Keep it to reads, and put anything you must release in "),
				API("via.Ctx.OnConnect"), h.Str(", which only a real connection reaches."))),

		d.H2("Conditionals and lists"),
		snippet.Region("compositions/lists.go", "list", snippet.Mark("h.ID(", "on.WithArg")),
		h.P(APIText("via.When", "via.When(cond, build)"), h.Str(" calls build only when cond holds. "), API("via.Each"),
			h.Str(" and "), API("via.List.Each"), h.Str(" call a row method per item. A row is markup, not a unit: "+
				"its action is a method on the list's owner, and "), API("on.WithArg"),
			h.Str(" carries the row's id with the click. A Signal inside a slice element has no field to be named by, and rendering it panics.")),
		h.P(h.Str("Give each row a stable "), API("h.ID"), h.Str(". Datastar morphs a re-rendered list by position, "+
			"which is right for an append-only log and wrong after a delete or a reorder: without ids, the rows after the removed one "+
			"are patched in place from their neighbours.")),
		h.P(h.Str("The render that handles an action decides what it may do. An action answers 410 Gone when that render "+
			"does not contain it: its When branch is closed, its row is gone, its "), API("on.WithArg"),
			h.Str(" value is one no row rendered, or its child key holds nothing or a unit of another type. "+
				"The check runs before the handler, so a handler only sees arguments the render offered.")),
		Callout(Warning, "Gate on server data",
			h.P(h.Str("A "), API("via.Signal.Bind"), h.Str(" value is whatever the browser last posted. Use a bound signal for a "+
				"disclosure the user controls, never as a When condition that guards an action. Read the session or the store in "),
				Code("OnInit"), h.Str(" instead."))),

		d.H2("Layouts and slots"),
		snippet.Region("compositions/layout.go", "shell"),
		snippet.Region("compositions/layout.go", "mount"),
		h.P(h.Str("A layout is a generic struct whose type parameter is its body. "), Code("Shell[Stepper]"), h.Str(" and "),
			Code("Shell[TwoColumn[Profile, Inbox]]"), h.Str(" are distinct types, and each "), API("via.Mount"),
			h.Str(" gets its own. The body renders as a child, so its signals are named "), Code("body__…"),
			h.Str(". Only the mounted root's "), Code("PageMeta"),
			h.Str(" counts, so the shell names the page; a PageMeta on the body is ignored and logged.")),
		snippet.Region("compositions/layout.go", "slots"),
		h.P(h.Str("More slots are more type parameters. "), Code("C any"), h.Str(" does not require a View, so a body type "+
			"without one compiles and panics at Mount, when via renders the page once.")),

		d.H2("Coming from React, Vue or Svelte"),
		h.P(h.Str("Each pattern below is a JS framework idiom on the left and the via version on the right. "+
			"The JavaScript is for comparison only; the Go compiles as shown.")),

		d.H3("Components and props"),
		Compare("React", snippet.Text("", reactProps), "via", snippet.Region("compositions/component.go", "props", snippet.Title(""))),
		h.P(h.Str("Props are fields. Constants go in the literal the parent is built from; request data goes in the parent's "),
			Code("OnInit"), h.Str(", which runs before Child copies the child.")),

		d.H3("Lists with keys"),
		Compare("React", snippet.Text("", reactList), "via", snippet.Region("compositions/lists.go", "row", snippet.Title(""))),
		h.P(Code("key"), h.Str(" becomes the row's id, and the closure over "), Code("t.id"), h.Str(" becomes "),
			API("on.WithArg"), h.Str(".")),

		d.H3("Events up"),
		Compare("React", snippet.Text("", reactEvents), "via", snippet.Region("demos/compositions_relay.go", "relay", snippet.Title(""))),
		h.P(h.Str("A child's action re-renders only that child, so a callback into the parent would run and leave the parent's "+
			"markup as it was. If the parent owns the state, put the button in the parent's View. If two units must talk, one publishes on a "),
			API("topic.Topic"), h.Str(" and the other subscribes with "), API("via.Ctx.Listen"),
			h.Str(". Here the parent makes the topic in OnInit, so each tab's stream gets its own.")),
		demo.Card(d.H3("Relay"),
			h.P(h.Str("The picker and the summary are siblings that share one topic and nothing else.")),
			via.Child(p.Relay), "compositions_relay.go",
			demo.Try(h.Str("Pick a language. The Wire pane shows one POST for the picker, then an SSE patch for the summary."))),
		Callout(Caveat, "Both ends must be live",
			h.P(h.Str("The picker renders a State, which keeps its instance, and the topic it holds, for the connection. "+
				"A plain picker's action would run on a fresh instance holding a fresh topic that nothing listens to."))),

		d.H3("Lifting state"),
		Compare("React", snippet.Text("", reactLift), "via", snippet.Region("compositions/recipes.go", "lift", snippet.Title(""))),
		h.P(h.Str("The owner holds the state and sets the child's field in its action; the push that follows copies the child "+
			"with the new value. Cart must be the mounted root: a live child may not render children of its own. "+
			"For state that many tabs share, keep it in a store and give each unit a "), API("via.StateTrack"),
			h.Str(" on its topic; see "), link("/live", "Live state"), h.Str(".")),

		d.H3("Context and providers"),
		Compare("React", snippet.Text("", reactContext), "via", snippet.Region("compositions/recipes.go", "context", snippet.Title(""))),
		h.P(h.Str("via has no provider tree. What a provider carries lives in one of three places: the request, whose context "+
			"middleware can extend and any unit reads through "), API("via.Ctx.Request"), h.Str("; the session, which "),
			API("via.Ctx.Session"), h.Str(" resolves to the same one in every unit of a request; or a pointer in the literal you pass to "),
			API("via.Mount"), h.Str(", handed down field by field. Nothing propagates on its own: a child sees what its parent's "+
				"literal or OnInit gave it.")),

		d.H3("Slots and children"),
		Compare("React", snippet.Text("", reactSlots), "via", snippet.Region("compositions/layout.go", "settings", snippet.Title(""))),
		h.P(Code("children"), h.Str(" is a type parameter; see "), h.A(h.Href("#layouts-and-slots"), h.Str("Layouts and slots")), h.Str(".")),

		d.H3("Conditional rendering"),
		Compare("React", snippet.Text("", reactCond), "via", snippet.Region("compositions/recipes.go", "cond", snippet.Title(""))),
		h.P(h.Str("What the user toggles lives in the browser: a "), API("via.SignalCS"), h.Str(" and "), API("h.DataShow"),
			h.Str(", with no request. What decides access is server data read in OnInit, in a When: a closed branch binds nothing, "+
				"so its actions answer 410.")),

		d.H3("Derived values"),
		Compare("React / Vue", snippet.Text("", reactDerived), "via", snippet.Region("compositions/recipes.go", "derived", snippet.Title(""))),
		h.P(h.Str("A method is a computed value: View calls it at render, and it can reach anything Go can. "+
			"For a value that follows typing with no request, build an "), API("expr.Expr"),
			h.Str(" over the signals; the browser re-evaluates it. See "), link("/signals", "Signals"), h.Str(".")),

		d.H3("Effects"),
		Compare("React", snippet.Text("", reactEffect), "via", snippet.Region("compositions/recipes.go", "effect", snippet.Title(""))),
		h.P(Code("OnInit"), h.Str(" is where the effect is registered: "), API("via.Ctx.Tick"), h.Str(" and "),
			API("via.Ctx.Listen"), h.Str(" stop on their own when the stream closes. For a resource of your own, acquire it in "),
			API("via.Ctx.OnConnect"), h.Str(" and release it in "), API("via.Ctx.OnDispose"), h.Str(".")),

		d.H2("Limits"),
		h.Ul(
			h.Li(h.Str("Client-side state is signals only: "), API("via.Signal"), h.Str(" and "), API("via.SignalCS"),
				h.Str(". There is no component instance in the browser, and no hook runs there.")),
			h.Li(h.Str("Anything that needs Go is a round trip: every action is a POST, and the answer is a patch. "+
				"Typing, toggling and derived text can stay in the browser; validation that needs the database cannot.")),
			h.Li(h.Str("A child's action re-renders that child only. Units that must react to each other share a topic.")),
			h.Li(h.Str("A list row is markup, not a unit: no per-row signals, and per-row actions go through "), API("on.WithArg"), h.Str(".")),
			h.Li(h.Str("Keys are positions, so a Child's presence must not change while the page is open.")),
			h.Li(h.Str("A live unit may not contain another live unit, and a live child may not call Child at all. "+
				"Keep the live units side by side under a plain parent. Both rules panic at render:")),
		),
		snippet.Text("panic", liveNesting),
		h.P(h.Str("For UI whose state is mostly in the browser (a map, a chart you pan, an editor), mount a JavaScript "+
			"island and talk to it through signals; see "), link("/islands", "Islands"), h.Str(".")),
	)
}

const liveNesting = `via: via.Child: a live unit cannot sit inside another live unit —
  embed it directly from a plain ancestor instead

via: via.Child: a live child's View must not call Child —
  keep a live child's View flat`

// The JS samples are kept under 39 columns: each sits in one half of a
// Compare, beside Go of the same width.

const reactProps = `function Avatar({ name, size }) {
  return (
    <span>
      {name}<small>{size}px</small>
    </span>
  );
}

// a constant prop, and request data
<Avatar size={48} name={user.name} />`

const reactList = `todos.map(t => (
  <li key={t.id}>
    {t.title}
    <button
      onClick={() => remove(t.id)}>
      ×
    </button>
  </li>
))`

const reactEvents = `function Picker({ onPick }) {
  return (
    <button
      onClick={() => onPick("Go")}>
      Go
    </button>
  );
}

function Relay() {
  const [last, setLast] =
    useState("");
  return (
    <>
      <Picker onPick={setLast} />
      <p>last pick: {last}</p>
    </>
  );
}`

const reactLift = `function Cart() {
  const [items, setItems] =
    useState([]);
  const add = () =>
    setItems([...items, "apple"]);
  return (
    <>
      <button onClick={add}>
        add
      </button>
      <ul>{items.map(row)}</ul>
      <Total n={items.length} />
    </>
  );
}`

const reactContext = `const User = createContext(null);

<User.Provider value={user}>
  <App />
</User.Provider>

function Badge() {
  const user = useContext(User);
  return <span>{user}</span>;
}`

const reactSlots = `function Shell({ title, children }) {
  return (
    <>
      <header>
        <h1>{title}</h1>
      </header>
      <main>{children}</main>
    </>
  );
}

<Shell title="Settings">
  <Stepper label="volume" />
</Shell>`

const reactCond = `<nav>
  <button
    onClick={() => setOpen(!open)}>
    menu
  </button>
  {open && (
    <ul>
      <li>profile</li>
      {isAdmin && <PurgeItem />}
    </ul>
  )}
</nav>`

const reactDerived = `// React
const subtotal = useMemo(
  () => qty * price, [qty, price]);

// Vue
const subtotal = computed(
  () => qty.value * price);`

const reactEffect = `useEffect(() => {
  const id = setInterval(
    () => setNow(new Date()), 1000);
  return () => clearInterval(id);
}, []);`
