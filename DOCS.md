# via — documentation

The tour, the plain/live model, the security floor, shutdown, and the full
feature map with every trap. [`README.md`](./README.md) is the pitch and the
smallest working example; this is the manual.

## Tour

Five steps, each one a whole `main.go`. Everything after the tour is detail on
one of them.

**1. An action.** A `View` method, a handler method, `via.On` to wire them. No
signals, no stream — a POST and a morph.

```go
type Counter struct{ n *atomic.Int64 }

func (c *Counter) Inc(ctx *via.Ctx) { c.n.Add(1) }

func (c *Counter) View() h.H {
	return h.Div(
		h.H1(h.Str(c.n.Load())),
		h.Button(via.On("click", c.Inc), h.Str("+")),
	)
}

func main() { http.Handle("/", via.Handler(Counter{n: new(atomic.Int64)})) }
```

**2. `Signal` — client state.** `Bind()` on an input and `Display()` elsewhere
share one wire name (the Go field name), so the text tracks the input entirely
in the browser, with no request at all.

```go
type Greeting struct{ Name via.Signal[string] }

func (g *Greeting) View() h.H {
	return h.Div(
		h.Input(g.Name.Bind(), h.Placeholder("your name")),
		h.P(h.Str("Hello, "), g.Name.Display()),
	)
}
```

**3. `via.StateTrack` — server state shared across tabs.** `State` is per
connection, so the shared number stays in a store you own — here an
`atomic.Int64` — and each bump is announced on a `topic.Topic`. A tracked
`State` seeds this connection's copy from the store and then applies every
publish, which element-patches over SSE; tracking is also what makes the page
live. It exists because the subscribe only happens when the tab connects, after
the first read: a tracked `State` re-reads at connect too, so a publish landing
in that window is not missed.

```go
type Counter struct {
	n    *atomic.Int64       // the shared value, app-owned
	room *topic.Topic[int64] // announces each bump
	Hits via.State[int64]    // this connection's view of it
}

func (c *Counter) Inc(ctx *via.Ctx) { c.room.Publish(c.n.Add(1)) }

func (c *Counter) View() h.H {
	return h.Div(
		h.H1(c.Hits.Display()),
		h.Button(via.On("click", c.Inc), h.Str("+")),
	)
}

func main() {
	n, room := new(atomic.Int64), topic.New[int64]()
	http.Handle("/", via.Handler(
		Counter{n: n, room: room, Hits: via.StateTrack(room, n.Load)}))
}
```

When the source depends on the request — a topic picked by the path param or
the session — call `s.Hits.Track(ctx, t, load)` in `OnInit` instead; a literal
is fixed at mount time and cannot read one.

The store need not be the whole picture: `load` may return one field of a
larger struct, and the topic then carries that field's type.

**4. `PostForm` + `Session` + `Redirect`.** A native, always-multipart form
whose submit runs server-side. Read fields with stdlib, keep the result in the
typed session, redirect on success and re-render on failure.

```go
type User struct{ Name string }

type Login struct{ err string }

func (l *Login) Submit(ctx *via.Ctx) {
	name := strings.TrimSpace(ctx.Request().FormValue("name"))
	if name == "" {
		l.err = "name is required" // no Redirect: the page re-renders with the error
		return
	}
	ctx.Session().Put(User{Name: name}) // Get[User]() reads it back
	ctx.Session().Rotate()              // fixation defense on auth change
	ctx.Redirect("/")
}

func (l *Login) View() h.H {
	return via.PostForm(l.Submit,
		h.Input(h.Name("name"), h.Placeholder("name")),
		h.Button(h.Type("submit"), h.Str("Log in")),
		via.When(l.err != "", func() h.H { return h.P(h.Str(l.err)) }),
	)
}
```

**5. A multi-page app.** Self-contained, like every step before it. `NewRouter`
+ `Mount` namespaces each page's actions under its mount. `OnInit` loads
path/session data before the ctx-free `View`, and `WithErrorPage` renders
failures as documents. Set `WithTrustedOrigin` in production, and `Close` the
router before the server.

```go
type Home struct{}

func (p *Home) View() h.H { return h.A(h.Href("/thread/1"), h.Str("thread 1")) }

type Thread struct{ id int }

func (t *Thread) OnInit(ctx *via.Ctx) error {
	t.id = ctx.Param[int]("id")
	if t.id > 99 {
		return via.ErrNotFound // 404
	}
	return nil
}

func (t *Thread) View() h.H { return h.H1(h.Str(t.id)) }

func errorPage(ctx *via.Ctx, e via.PageError) h.H {
	return h.Div(h.H1(h.Str(e.Status)), h.P(h.Str(string(e.Reason))))
}

func main() {
	app := via.NewRouter(
		via.WithTrustedOrigin("https://example.com"),
		via.WithErrorPage(errorPage),
	)
	via.Mount(app, "/", Home{})
	via.Mount(app, "/thread/{id}", Thread{})
	defer app.Close()
	log.Fatal(http.ListenAndServe(":8080", app)) // never drop this error: a port
	// already in use otherwise looks like a page that does not respond
}
```

## A page is plain until a composition makes it live

**`State` is per connection, not per app.** Each tab gets its own copy, so a
counter two tabs are supposed to share does not live in a `State`. It lives in
a store you own, changes are announced on a `topic.Topic`, and each tab's
`State.Track` seeds from the store and follows the topic — including a re-read
at connect, so a publish that beat the subscribe is not lost. That is the whole
shared-live-state recipe (step 3 of the [Tour](#tour), and `internal/example/shared`);
reach for it before anything else here. A plain `ctx.Listen` is the right shape
when the tab does not mirror a value but has to see every message —
`internal/example/feed`, and `internal/example/chat`'s message bus appended to a `List`.

A page is served **plain**: request/response, with actions and a morph on
POST. It **streams** — an SSE connection scoped to that one tab, its own server
state pushed over it — the moment a composition on it *acts* live: its `OnInit`
registered a `ctx.Tick` or a `ctx.Listen`, or its `View` rendered a
`State[T]`/`List[E]`. There is no marker interface: `OnInit` is the hook that
starts a unit, on a page and on every child. The same model covers a
fully plain page and a fully live app.

The whole rule is one line:

> A page streams iff it contains a live unit; a live child may sit under any
> plain ancestor; a live unit may not contain another live unit.

That verdict is taken from the render that serves the page, and only a
streaming page bootstraps the SSE stream, so **liveness has to be
render-invariant**. A `State.Display` behind a branch that is closed at GET
wires the page plain. An action that later opens the branch would leave the tab
demanding a connection it never opened, and every action after it would 410. via
fails that action loudly instead. Render the `State` unconditionally (put the
`When` *inside* the row, not around the `Display`), or register a `Tick`/`Listen`
in `OnInit` so the page is live from the first paint.

`OnInit` runs on every request that renders the unit: the GET, each action,
the SSE connect. Register connection-scoped side effects rather than performing
them: `ctx.OnConnect(fn)` runs once when the stream opens, `ctx.OnDispose(fn)`
when it closes.

**`OnReload` is the other half.** `OnInit` runs before the handler, so anything
it loaded is stale the moment the handler mutates the store:

```go
func (p *Front) OnInit(ctx *via.Ctx) error { return p.OnReload(ctx) }

func (p *Front) OnReload(ctx *via.Ctx) error {
	p.links = p.store.Front()
	return nil
}

func (p *Front) Vote(ctx *via.Ctx, id int) { p.store.Vote(id) } // no refetch
```

`OnReload(*via.Ctx) error` runs after every action on that unit and before the
response render, on the plain path and the live path alike. Without it a
handler that mutates and does not re-read answers `204` with an unchanged UI;
via logs one line naming `OnReload` when that happens, so it is never silent.
It is skipped behind a `Redirect` (nothing from that render ships), and
`ctx.Tick`/`ctx.Listen` are no-ops inside it, so liveness stays the GET/connect
verdict. It is a second hook rather than a second `OnInit` run on purpose:
`OnInit` also mints the session and registers timers, neither of which is safe
to repeat once a handler has committed a mutation.

**The hooks are duck-typed.** A composition opts in by having the method, so
a rename or a signature change opts it silently *out* — it still compiles,
and the hook stops running. `Mount` and `Child` are the safety net: an
`OnInit` or `OnReload` with the wrong signature panics at boot, and a method
that has the hook's exact signature under a near-miss name (`Reload`,
`OnInitialize`, …) is logged once, naming the method it was surely meant to
be.

**Page state goes in the path or the session — never the query string.** An
action POSTs to `{mount}/_via/a/{child}/{id}`, built from the mount pattern with
its `{name}` segments filled in and *nothing else*. The page's `?q=urgent&page=2`
is not on that URL, so the render that decides what is dispatchable runs
unfiltered: a row that only exists under the filter binds no action there, and
clicking it answers `410`. `ctx.Request().URL.Query()` reads on the GET and is
empty on every action.

```go
via.Mount(r, "/tickets/{status}/{page}", TicketList{}) // survives an action
// /tickets?status=open&page=2                    // does NOT
```

**A page describes itself.** `via.WithHead` is router-wide. A page declares its
own title, description, social cards and assets with one `PageMeta` method — a
method, not a field, because real metadata is data-dependent. It is read after
`OnInit` (and `OnReload`), so the data is already loaded:

```go
func (p *Ticket) PageMeta() via.Meta {
	return via.Meta{Title: "#" + strconv.Itoa(p.t.ID) + " " + p.t.Subject}
}
```

Only the **mounted root's** `PageMeta` counts: a nested unit may not rename the
page it happens to sit in, and `Child` logs one line when it sees a child
declaring one. It shapes the *document*, so it lands on a render that writes one
— the GET and a native form submit — not on an SSE push, which only patches
elements inside `<body>`.

Every field but one is inert: escaped text, free to vary with the request.
`Assets` is not — it is the page's scripts, styles and preloads, and it decides
that mount's `Content-Security-Policy`:

```go
func (p *Charts) PageMeta() via.Meta {
	return via.Meta{
		Title: "Charts",
		Assets: via.Assets{
			Scripts: []via.Script{{Src: "https://cdn.example/c.js", Module: true}},
			Styles:  []via.Style{{Inline: "svg{display:block}"}},
		},
	}
}
```

The policy is built **once per mount**, off the literal you mounted, so it costs
nothing per request — one page's CDN never widens another page's policy. That
is also why `Assets` must be a **constant of the type**: via fingerprints it at
`Mount` from the zero-data literal, re-reads it on every document render, and
panics if the two differ. That panic is on the request path, so what you
actually see is **every GET of that page answering `500 render failed`** — the
sentence naming the type sits in the server log, above the stack. A relative URL
is covered by `'self'`, an absolute one contributes its origin, and an inline
script or style is admitted by the sha256 of its exact bytes. `Head.Raw` refuses
a `<script>` or `<style>` outright — via never parses `Raw`, so the CSP could
not admit it and the browser would block it with no signal at all.

**There is no response writer.** `Ctx` can read the request and `Redirect`;
it cannot set a header, a status or a body, because a live action's answer is
an SSE frame on a connection the POST does not own. Anything that streams bytes
— a file download, a CSV export — is a sibling `net/http` handler next to the
via one.

## Composition

Four terms. Two describe a unit, two describe a subtree, and the axes are
independent.

- **Unit**: a struct with a `View`. The mounted root is one; every
  `via.Child(p.Field)` in a `View` starts another, with its own `OnInit`.
- **Plain / live**: *who pushes*. A unit is live when it acts live — its
  `OnInit` registered a `Tick` or `Listen`, or its `View` rendered a
  `State`/`List`. Nothing else makes it live; there is no interface to assert.
- **Child**: a unit embedded by value in its parent and rendered with
  `via.Child`. Plain or live like any unit. A child gives a region its own
  hooks, state and patch boundary; it is not a component abstraction.
- **Island**: a subtree the *client* owns — a chart or map library renders it,
  via only feeds it signals. See [Client-side islands](#client-side-islands).

Live and island do not overlap. A plain page can hold an island seeded once by
`Set` in `OnInit`; a live unit can re-feed one on every `Tick`; most live units
have none. Live is the server pushing, island is the browser drawing.

### The nesting rule

> A page streams iff it contains a live unit; a live child may sit under any
> plain ancestor; a live unit may not contain another live unit.

What that permits and refuses:

| Parent | Child | |
|---|---|---|
| plain | plain | any depth |
| plain | live | any depth, as long as every ancestor on the path is plain |
| live root | plain | allowed |
| live root | live | **panics at render** |
| live child | anything | **panics at render** — a live child's `View` is flat, no `Child` calls at all |

Both refusals happen on the render, before an action can misroute. A dynamic
set of live children, or a live child inside a live child, is deferred. The
workaround: make the outer unit plain and turn the pieces that push into live
siblings under it, sharing through a pointer dep or a `topic.Topic`.

### What a child gets

- **Its own hooks.** `OnInit` and `OnReload` run per child, on the GET, on each
  action and on the SSE connect, as for the root.
- **Its own patch region.** A live child re-renders and pushes only its
  container, `#via-i{n}`, in place. A parent's patch never morphs it, and it
  never touches a sibling.
- **Its own address.** Actions route by child key plus the tab handshake;
  signals are slot-scoped, so two children of the same type never collide.
- **One stream per tab.** Every live child on a page shares the tab's single
  SSE connection and goroutine. A handler that blocks stalls all of them.
- **Ownership by value.** The parent's field literal seeds the child's deps at
  registration and each connection gets its own copy, so value state stays
  per tab. A pointer dep (a `*Topic`, a store) is the deliberate sharing
  channel. A generic layout — `type Shell[C any] struct{ Body C }` — composes
  one shell with any page for free.
- **A stable key.** A child's key is its position among the parent's `Child`
  calls, composed with the parent's own (`0`, `0-0`). Container id, signal
  prefix and dispatch address all derive from it, so it must be identical on
  every render for the life of the connection. A `When` around a `Child`
  shifts its later siblings' keys — that `When` may depend on data fixed by
  `OnInit` or the literal, never on time, a client signal or shared state.
- **No page metadata.** Only the root's `PageMeta` counts; `Child` logs once
  when a child declares one.

`internal/example/dashboard` is the reference: a plain root holding a plain
header, a ticking uptime child that also feeds a canvas island, and a queue
child, the two live ones as siblings.

## Client-side islands

via never generates or evaluates JavaScript. To hand a subtree to a chart or a
map library: declare the script in `PageMeta`'s `Assets.Scripts`, put the
container under `h.DataIgnoreMorph()` so a live patch leaves the subtree alone, and
have the script react to a signal.

```go
h.Div(h.ID("chart"), h.DataIgnoreMorph(),
	h.DataEffect(expr.Call("drawChart", expr.El, p.Series.Ref())))
```

`expr.Raw` takes JavaScript `expr` cannot spell.

Update it from Go with `sig.Set(…)`. `Set` declares the signal, so the island
needs no `Bind()` or `Display()`. Seed the first-paint value with a `Set` in
`OnInit`.

Write the init so it can run twice: a `data-effect` re-runs on every change of
every signal it reads, so build once (`el._map ??= new Map({container: el})`)
and let later runs only move what already exists. Teardown is still yours — a
patch that drops the container frees nothing the library holds — but via tells
you when to do it: it dispatches `via:remove` on `document` for every element
that leaves the document, with the removed root in `detail.el`. Mark each
container with a data attribute (`h.Data("island-map", "")`), and on
`via:remove` find the marked elements in `detail.el` (the root itself, or
`querySelectorAll` inside it) and call the library's own `remove()`, so its
worker, WebGL context and timers go with the node.

### Client events

Every via page ships two plain `CustomEvent`s on `document`, on live and plain
pages alike:

- `via:patch` fires after each applied patch, with
  `detail.{kind, el, selector, mode, elements, signals}` — `kind` is
  `"elements"` or `"signals"`, `el` is the element whose fetch carried the
  patch (the clicked control, or `body` for the stream), and the DOM is
  already patched when it fires.
- `via:remove` fires for every element that leaves the document, with the
  removed root in `detail.el`.

They are the supported way to observe via's DOM work; nothing about Datastar's
own events is part of via's API.

## Security floor (built in)

The action endpoint and rendered pages are hardened by default:

- **Origin floor** on `POST` actions, open by default, so dev and
  non-browser clients just work; set `WithTrustedOrigin` in production to
  enforce same-origin (plus the listed origins), failing closed. Be precise
  about what the per-tab id does and does not cover:

  - On a **live** page the tab id is a synchronizer token: it is minted per
    connection, set by same-origin JS into the request body, and never
    auto-attached by the browser, so a cross-origin POST cannot produce one.
    There it does the CSRF work.
  - On a **plain** page there is no connection and no id: `viatab` is `""`
    and the `_viatab` form field is empty. A cross-origin multipart `PostForm`
    submit with an empty id is accepted with the floor open — verified. What
    defends a plain page is the session cookie's `SameSite=Lax`, which a
    browser will not attach to a cross-site POST, so the request arrives
    unauthenticated. Anything a logged-out visitor may do, a cross-origin
    page may also do.
  - `WithTrustedOrigin` is what closes that: with at least one origin named,
    every action POST must prove its source (an allow-listed `Origin`,
    `Sec-Fetch-Site: same-origin`/`none`, or an `Origin` whose host matches
    the request Host) and a request that proves nothing is refused 403. That
    also refuses `curl` and other clients that send no origin signal at all.
    Set it in production.
- A stream's tab id is a bearer credential for that connection's
  actions. If it was opened under a session, a dispatch is also checked
  against that same session, so a leaked tab id alone is no longer enough
  once the connecting browser was logged in. An anonymous connection (no
  session) has nothing to check against. Never render, log, or leak a tab
  id outside its own client, and set `WithTrustedOrigin` to close the
  cross-origin leg too.
- **Session fixation defense is opt-in**: sessions never
  rotate their id on their own, so call `Session.Rotate()` at an auth-state
  change (login, logout, privilege elevation) to invalidate a pre-auth id an
  attacker may have planted.
- **Request-body cap** (413) + a capped JSON decode (400 on malformed/oversized
  input; unknown signal keys and trailing bytes after the JSON value are
  ignored, not rejected — it is not a strict decode), and a **panic recover**.
- **`nosniff` + a hash-admitted CSP** on the page and patch responses. The CSP
  includes `'unsafe-eval'` because Datastar compiles `data-*` expressions with
  the `Function` constructor. Without it every action is silently dead in the
  browser. The one inline script via emits (the reconnect manager) is
  library-controlled, so it's admitted by its own SHA-256 hash: no nonce, no
  per-key state, nothing for an injected `<script>` to borrow. Honest posture:
  the CSP is a seatbelt against *injected* inline script. The load-bearing
  defenses are output escaping, the attribute-name allowlist, and the same URL
  gate on every `Redirect` target. `javascript:`/`data:`/`//` targets are
  dropped loudly: a Datastar action falls back to its normal element-patch
  response, a native `PostForm` submit falls back to a full-page re-render, and
  an unsafe target from `OnInit` answers 500. A safe target navigates from
  anywhere, including a Datastar `@post`. The response is a one-line
  `location.assign` script the CSP admits by hash, with the target carried in
  a `datastar-script-attributes` header rather than in the script bytes.
- **HTML/attribute escaping** with an attribute-name allowlist (`h.RawAttr` /
  `h.Data` and the typed helpers reject injectable names).
- **Dispatchable iff rendered.** A handler id — and, for `via.OnArg`, the
  `(handler, arg)` pair — is dispatchable only if the current render bound it;
  anything else answers 410 before the handler runs. The render that decides
  this is server state alone: a `Signal` is hydrated from a request only for a
  slot the render put under client control (`Bind()`, which emits
  `data-bind`), and the plain action path applies the body after its discovery
  render. So a `Display()`-only or unrendered signal cannot be set by a client,
  and a POST cannot open the branch that authorizes it.
- **Never gate on a signal.** A `Bind()`ed `Signal` is client state by
  definition — whatever the client last set it to. It is a fine switch for a
  disclosure the user controls; an authorization gate belongs on session or
  database state, read in `OnInit`.
- **A protected page checks its session in `OnInit`:**

  ```go
  func (p *Profile) OnInit(ctx *via.Ctx) error {
  	if _, ok := ctx.Session().Get[User](); !ok {
  		ctx.Redirect("/login")
  		return nil
  	}
  	return nil
  }
  ```

  `OnInit` denies by returning `via.ErrForbidden` for "you may not do this"
  (403, `via.ReasonForbidden` through `WithErrorPage`) or by queuing
  `ctx.Redirect` for "please sign in" (303 on a page GET, a navigation script
  on a Datastar action). On the SSE connect a denial is always a plain 403,
  because `fetch` follows a 303 and would deliver the target page's HTML as
  the stream body.

  `OnInit` runs on every transport but a live action over an already-open
  stream, so a session revoked after connect keeps `Tick` and `Listen`
  pushing on that stream until the tab next acts and is denied, closes, or
  the router shuts down — that gap is unmitigated.

## Shutdown

A `Router` owns goroutines — one per live tab, plus its `Tick` timers and
`Listen` subscriptions. They hang off a context of the router's own, which
`http.Server.Shutdown` does not cancel, so close the router first:

```go
<-stop
r.Close()                 // ends every stream cleanly, runs each OnDispose
srv.Shutdown(ctx)         // then drain the plain requests
```

`Close` returns once the last stream goroutine is gone. Open streams end the
way a closed tab ends — a clean end of response, not a truncated one. An
action POST against a closing tab answers `410`, and a connect arriving after
`Close` is refused `503`. It is safe to call more than once.

`via.Handler` returns that `*Router`, so `Close` is reachable from the one-line
entry point too — `r := via.Handler(Page{})` then `defer r.Close()`. `*Router`
serves HTTP, so it goes straight into `http.Handle`. `internal/example/chat` runs the
whole path end to end: SIGINT, `r.Close()`, `srv.Shutdown`.

## The feature map

Each capability, the example that demonstrates it, and the traps that come with
it.

- **Plain core** (`internal/example/counter`): an action's response self-classifies —
  element-patch when the render changed, `204` when it didn't.

- **Reactive handles** (`internal/example/greeting`): client-resident `Signal[T]` with
  handle-identity wire names. `Bind()` and `Display()` share one name, so the
  greeting updates live as you type, entirely client-side. `When`/`Each` render
  conditionals and lists.
  - A signal's wire name is its Go field name — `count`, and `chat__draft` for
    one inside a `Chat` child; names join with `_` through plain nested
    structs, and the walk stops at a field with its own `View` — that unit
    names its own signals. Not its render order, so a `Bind()` behind a
    `When` (a wizard step, a branch that only sometimes renders its input)
    keeps its own slot instead of inheriting one from whatever rendered first.
  - `sig.Ref()` returns that name as an `expr.Expr` (`"$count"`), which the
    `h.Data*` attributes take: `h.DataShow(p.Open.Ref())`.
  - A `Signal` must be a plain field of the composition. One reached through a
    pointer, slice, array or map field, or held by a composition whose `View`
    has a value receiver, has no field offset to name itself by. via walks the
    composition type at `Mount`/`Child`, so those **panic at startup**, not
    once per request.
  - The one case the type walk cannot see is a Signal behind an `interface`
    field; that still panics on the first render that binds it.
  - `SignalCS[T]` is the client-only sibling: its wire name is `_`-prefixed,
    which Datastar's fetch filter drops, so it never reaches the server. It has
    no `Set` or `Get`. Use it for UI-only state — a panel open or closed, the
    active tab — and as an operand in `expr`.
  - A `via:"init=<json>"` field tag gives any handle — `Signal`, `SignalCS`,
    `State`, `List` — its start value, decoded once at Mount and applied per
    unit before `OnInit`. A tagged `Signal` reaches the client at first paint
    with no `Bind` or `Display`, which is how a JS island is fed a `[]` rather
    than a `null`:

    ```go
    Load    via.Signal[[]int]  `via:"init=[]"`
    Details via.SignalCS[bool] `via:"init=true"`
    Room    via.State[string]  `via:"init=\"lobby\""`
    ```

    A `StateOf`/`ListOf` literal wins over the tag, a `Set` in `OnInit` wins
    over both, and a posted value wins for a `Bind()`ed signal. Any other key,
    a value that is not JSON for its type, or a tag on a field that is no via
    handle **panics at Mount**.

- **Live children + `State[T]`** (`internal/example/pulse`): render a `State[T]` or
  register a `Tick` and a composition becomes a live child with a per-tab SSE
  stream. `State[T]` is server-authoritative, read from the pure View and
  element-patched on change; `Tick` drives the push.
  - `Tick`/`Listen`/action handlers all run on the connection's one goroutine.
    A handler that blocks (I/O, an unbounded loop) stalls every other tick,
    action, and push on that same connection, and delays that connection's
    shutdown until it returns.

- **Interactive live actions** (`internal/example/chat`): a live-child action routes to
  *this* connection's child — via the `via_tab` handshake, an unguessable
  per-connection id echoed in the `viatab` signal every `@post` already carries
  — mutates its state, and the result is pushed over its SSE.
  - The element push omits `data-signals`, and deliberate signal changes ride a
    signal-patch, so a fan-out never clobbers what a user is typing.

- **Multi-user fan-out** (`internal/example/feed`, `internal/example/chat`): an in-process
  `via/topic.Topic[T]` broker: `via.StateTrack` (or `State.Track` in `OnInit`,
  when the topic depends on the request) when every tab mirrors one shared
  value, `ctx.Listen` / `ctx.OnDispose` when every message has to be seen. One
  publish fans out to every connected child.

- **Sessions** (always available):
  `ctx.Session().Put(v)`/`Get[T]()`/`Delete()`, a per-browser JSON value behind
  a signed-HMAC cookie issued lazily on the first write. A session holds one
  value; nest what you need in a struct. Apps that never store anything stay
  cookieless.
  - `Session.ID()` is the stable identity behind the cookie — unchanged by
    `Rotate`, `""` before the first write. Key per-user state by it; it grants
    nothing on its own.
  - `Session.Ensure()` mints the id and issues the cookie with nothing
    stored, for keying per-user state before there is a value to `Put`.
  - Sessions do not rotate their id on their own: call `Session.Rotate` at an
    auth-state change (login, logout, privilege elevation) to invalidate a
    session id an attacker may have planted beforehand (fixation defense).
  - Idle sessions expire lazily on access — past the TTL, the next read/write
    treats them as gone. There is no background sweep, so a session that is
    never touched again is not proactively evicted.
  - A session that idles past its TTL while a stream is still open (the stream
    itself does nothing to keep it warm) turns every later dispatch on that tab
    into a 403 "session mismatch" until the page is reloaded.
  - The signing key resolves `WithSessionKey` → `VIA_SESSION_KEY` env → a
    random per-process key (warned on first use). The key signs the cookie. The
    data lives in a `SessionStore`, and the default store is this process's
    memory, so a restart or a second pod needs `WithSessionStore` as well — see
    **Restarts and deploys** at the end of this section.
  - `WithSessionTTL`/`WithSessionCookieName` tune it. The cookie is `Secure`
    automatically over TLS (so `http://localhost` dev still works);
    `WithSecureCookies` forces it on behind a TLS-terminating proxy.

- **Resilience floor + reconnect** — what a live stream guarantees on a flaky
  network, with no knobs to set:
  - A server-side keepalive comment frame (fixed 25s) and a per-frame write
    deadline (fixed 10s), both riding the child's single goroutine.
  - A failed frame write tears the child down (runs disposers, stops ticks), so
    a half-open peer — gone without a FIN — can't leak its goroutine and timers.
  - A client reconnect manager surfaces a "Reconnecting…" banner on a dropped
    stream. On a give-up it reads "Disconnected." with a Reconnect button and
    probes the server until it answers, then reloads; after two such reloads
    probing stops and the button is the way back. It is amber while
    reconnecting and red once disconnected. Style it with a plain
    `#via-reconnect-banner` rule — via's own rules carry zero specificity, so
    yours wins — or drive your own UI from `data-via-connection` on `<html>`.

- **Live-child multiplexing** (`internal/example/dashboard`): child sub-compositions as
  plain struct fields, `via.Child(p.Clock)` in the parent's `View`, each with
  its own hooks, patch region and address. The vocabulary, the nesting rule and
  what a child owns are in [Composition](#composition).
  - `State` is per connection: a native `PostForm` submit is a navigation,
    opens a new connection, and reseeds it. Persist through the session or a
    shared pointer dep, or `Redirect` instead of returning a page.

- **Per-row list actions** (`internal/example/poll`): a row's button carries the row's own
  datum — `via.OnArg("click", l.Delete, item.ID)` — and the handler receives it as a
  typed parameter, `func(*via.Ctx, int)`. Identity rides with the click, so a list
  that grows, shrinks, and **reorders** never misroutes: the value (not the
  positional slot) picks the row. Still a named method value — no `&`, no closure.

- **Multi-page apps + auth + uploads** (`internal/example/forum`): `via.NewRouter()` with
  `via.Mount(r, "/path", Page{})` serves a whole app behind one handler, each
  page's actions namespaced under its mount.
  - `OnInit(*Ctx) error` is the per-request hook that loads session/path data
    into a plain page before its ctx-free `View`. Return `via.ErrNotFound` for
    a vanished record (404); any other error answers 500, and the View never
    renders a lie.
  - `Profile`, `Forum` and `ThreadPage` each redirect an anonymous visitor
    from their own `OnInit`.
  - `via.PostForm(handler, …)` renders a **native**, always-multipart form
    whose submit runs server-side, and `ctx.Redirect("/…")` issues a 303 — the
    server-rendered auth flow the bundled Datastar can't do. The same form
    handles the avatar upload: a file `<input>` just works, read with stdlib's
    `ctx.Request().FormFile("avatar")`.
  - `ctx.Param[int]("id")` reads the named `{id}` segment of `"/thread/{id}"`.

`internal/example/chat` is the most complete live example: a multi-user chat room with a
presence count, in ~60 lines.

**Per-user fan-out.** `ctx.Session().ID()` is the session's stable identity —
minted once, unchanged by `Rotate`, `""` before the first write. It is not the
cookie and grants nothing; it is a key. Key a topic by it and every tab of one
user follows that user's own stream:

```go
// store holds map[string]*topic.Topic[Inbox] behind a mutex; topicFor mints
// one on first ask and load reads that user's inbox.
func (p *Page) OnInit(ctx *via.Ctx) error {
	id := ctx.Session().ID()
	p.Inbox.Track(ctx, p.store.topicFor(id), p.store.load(id))
	return nil
}
```

`Track` and not `via.StateTrack` because the topic depends on the request. Like
every `Topic` this is in-process: two pods are two fan-outs. Horizontal scaling
below shows the bridge.

**Restarts and deploys.** Two separate things have to survive: the cookie and
the data behind it. A stable key (`WithSessionKey` / `VIA_SESSION_KEY`) keeps
the cookie valid across restarts and pods; it is signed rather than stored. The
data lives in a `SessionStore`, and the default one is a map in this process's
memory, so with the key alone a restart still logs everyone out and a second
pod sees nothing. Pass `WithSessionStore` for a shared, durable store:

```go
type redisSessions struct{ c *redis.Client }

func (r redisSessions) Load(ctx context.Context, id string) ([]byte, bool, error) {
	b, err := r.c.Get(ctx, "via:"+id).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	return b, err == nil, err
}

func (r redisSessions) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	return r.c.Set(ctx, "via:"+id, data, ttl).Err()
}

func (r redisSessions) Delete(ctx context.Context, id string) error {
	return r.c.Del(ctx, "via:"+id).Err()
}

via.NewRouter(via.WithSessionKey(key), via.WithSessionStore(redisSessions{c}))
```

That is the whole interface. via hands a session over already serialized and
never asks a store to understand it: rotation is a Save under the new id then a
Delete of the old, and expiry is the `ttl` handed to Save. via stamps the same
deadline into the blob and refuses an expired Load anyway, so a backend with no
TTL support is still correct. The value goes through `encoding/json`, so bytes
that no longer decode into the `T` a `Get[T]` asks for read back as absent
rather than as a stale value.

The CSP is a pure function of the Head, so pods with different keys still serve
identical policies. Live-child state is in-memory and per-connection: a deploy
drops the stream, and the client reconnect manager shows "Reconnecting…" and
reloads to re-bootstrap, so the page comes back from server truth rather than
replayed frames.

**Horizontal scaling.** The session lives in the signed cookie and the
`SessionStore`, so with the key and store above it follows the user to any pod.
The tab does not: its stream, live children and topic subscriptions are memory
on the pod that opened it, and an action POST looks its tab id up only there.
Land on another pod and the answer is 410, which the client reads as "page out
of date" and reloads. A balancer needs affinity for the life of a tab, not for
correctness: a miss costs one reload.

- Pin on a balancer-owned cookie, not on `via_session`. That id changes on
  `Rotate`, which would move a user to another pod mid-login.
- Streams are long-lived SSE: proxy buffering off, a read timeout well past
  your idle time, HTTP/1.1 upstream. No path needs special casing.
- TLS ending at the balancer means via sees plain HTTP, so `WithSecureCookies`.
- Roll one pod at a time: fail your readiness check, `Router.Close()`, then
  `srv.Shutdown()`. Open streams end cleanly and their tabs reload onto pods
  still in rotation. Readiness fails first because a connect refused 503 stops
  the client on a red banner rather than reloading it. via ships no health
  endpoint; the app owns one.

Shared state across pods is not automatic. A `Topic` fans out inside one
process, so a `Publish` on pod A never reaches a `Track` on pod B. Invert the
write path: handlers publish to your bus, and each pod runs one goroutine
feeding what it hears into the local topic. Nothing in via changes.

```go
// per pod, at startup
go func() {
	sub := rdb.Subscribe(ctx, "room:"+room.id)
	for m := range sub.Channel() {
		var msg Message
		if json.Unmarshal([]byte(m.Payload), &msg) == nil {
			room.bus.Publish(msg)
		}
	}
}()

// the write path publishes outward, never to room.bus directly
func (r *Room) Post(ctx context.Context, msg Message) error {
	b, _ := json.Marshal(msg)
	return rdb.Publish(ctx, "room:"+r.id, b).Err()
}
```

Pub/sub is fire-and-forget, so a tab mid-reload misses the gap. `StateTrack`
calls `load` again at every connect, so tracked state converges from the
database; only `Listen` handlers on events can skip a beat. Derive presence from
state rather than counting events.

A lost pod costs this: its open tabs go amber and reload, an action in flight
on it is gone with no replay, and so are unsent client signal edits. Sessions
survive in the store. via does nothing more for high availability.

**Failure responses.** Failures answer as plain `http.Error` text by default
(404 for `via.ErrNotFound` / a decode-miss `Param`, 500 for the rest).
`WithErrorPage(func(*via.Ctx, via.PageError) h.H)` renders them as documents
instead, for the responses a browser will render as a page — a GET, a route
matching no mount, a native `<form>` submit. A Datastar `@post` and the SSE
connect keep their plain text: the client consumes those bodies and already
surfaces the failure itself. Switch on `PageError.Reason` (`ReasonNotFound`,
`ReasonForbidden`, `ReasonGone`, …) rather than on the body text. The page
renders under the ROUTER-WIDE CSP floor — it can run before any mount resolves,
so it never carries a mount's per-page assets and cannot widen one's policy —
and a handler that panics or returns nil falls back to the plain text via would
have sent, logged once. See `internal/example/forum`.

**Logging.** `WithLogger(l)` routes via's own diagnostics — failed hooks,
session-store errors, dropped writes, recovered panics — to an `*slog.Logger`;
default is `slog.Default()`. The `h` package and `via/topic` have no Router to
reach and always log to `slog.Default()`.

**Action URLs address a handler, not a render position.** `child` is `r` for the
page root and the acting child's key otherwise, and `id` in
`/_via/a/{child}/{id}` is a hash of the handler method's own Go name, so it is
the same across renders, instances and builds. A row's datum rides along in
`?a=`. A list that grew or shrank since a tab painted therefore keeps every URL
that tab is holding valid. Dispatch answers `410 Gone` — on both the plain and
live paths — for an id the current render does not bind (a closed branch, or an
`OnInit` that failed to restore the state the `View` branches on; the 410 names
the handlers that are bound), an unknown child, or a live action with no
connection for its tab. Datastar resolves a non-2xx response silently and moves
on; a native `<form>` submit shows the browser's own error page.

**Stream caps.** The SSE GET stream applies the same origin check as the action
POST and is capped at a fixed number of concurrent connections (10,000; over the
cap returns 503). The cap is router-wide rather than per IP, so an anonymous
client can open enough connections on its own to fill the cap and 503 everyone
else. A per-IP cap is deferred past v0.8.

**Body caps.** `WithMaxBody(bytes)` caps an action POST body and how much of a
native form submit stays in RAM (default 1 MiB); `WithMaxUpload(bytes)` caps
that submit's whole multipart body (default 8 MiB). Over either, the request
answers 413. Both panic on a value of 0 or less.

**Deferred** (correctly out of 1.0 scope):

- A keyed cursor for the narrow remaining dynamic-shape cases — per-row
  *signals/inputs* in a **reordering** list, and lists *of* live children.
  Per-row actions are done via `OnArg`; fixed children via `via.Child` are done.
- Nested live composition: a live child holding a further live child, or a
  live child's own `View` calling `Child` at all. v0.8 refuses both at
  render.
- At-least-once redelivery. A push onto a dropping socket fails the write and
  tears down rather than being buffered for replay.

**Reconnect.** A dropped stream and its reconnect build an entirely new
`liveConn`. Any action POST still in flight against the old tab id answers 410
once the old connection is gone, and the client's own reconnect manager is
what re-bootstraps the page from server truth. See [Composition](#composition)
for the nesting rule.
