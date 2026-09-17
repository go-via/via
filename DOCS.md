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
share one wire name (the Go FIELD name), so the text tracks the input entirely
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

**3. `State` + `Topic` + `Listen` — server state shared across tabs.** `State`
is per connection, so the shared number lives in your own store. A `Topic`
publish fans the change out, and each tab's `Listen` copies it into that tab's
`State`, which element-patches over SSE. Registering the `Listen` in `OnInit` is
what makes the page live.

```go
type Shared struct {
	n     *atomic.Int64      // the real shared value
	room  *topic.Topic[int64]
	Count via.State[int64]   // this connection's view of it
}

var _ via.Initer = (*Shared)(nil)

func (s *Shared) OnInit(ctx *via.Ctx) error {
	s.Count.Set(s.n.Load())
	ctx.Listen(s.room, s.recv)
	return nil
}

func (s *Shared) recv(ctx *via.Ctx, v int64) { s.Count.Set(v) }
func (s *Shared) Inc(ctx *via.Ctx)           { s.room.Publish(s.n.Add(1)) }

func (s *Shared) View() h.H {
	return h.Div(
		h.H1(s.Count.Display()),
		h.Button(via.On("click", s.Inc), h.Str("+")),
	)
}

func main() {
	http.Handle("/", via.Handler(Shared{n: new(atomic.Int64), room: topic.New[int64]()}))
}
```

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
	ctx.Session().Put(User{Name: name}) // Get[User]() / Delete[User]() read it back
	ctx.Session().Rotate()              // fixation defense on an auth-state change
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

var _ via.Initer = (*Thread)(nil)

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
	// already in use otherwise looks like a page that simply does not respond
}
```

## A page is plain until a composition makes it live

**`State` is per connection, not per app.** Each tab gets its own copy, so a
counter two tabs are supposed to share does not live in a `State`. It lives in a
store you own, changes are announced on a `topic.Topic`, and each tab's
`ctx.Listen` handler copies the new value into that tab's `State`. That is the
whole shared-live-state recipe (step 3 of the [Tour](#tour), and
`example/feed`); reach for it before anything else here.

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

**`OnReload` is the other half.** `OnInit` runs BEFORE the handler, so anything
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

**Pin your hooks.** `OnInit` and `OnReload` are duck-typed: a composition opts
in by having the method, so a rename or a signature change opts it silently
*out* — it still compiles, and the hook just stops running. One line per hook
next to the type turns that into a compile error:

```go
var _ via.Initer = (*Front)(nil)
var _ via.Reloader = (*Front)(nil)
```

Mount and Child catch the two commonest slips on their own — an `OnInit` or
`OnReload` with the wrong signature panics at boot, and a method that has the
hook's exact signature under a near-miss name (`Reload`, `OnInitialize`, …) on
a type implementing neither interface is logged — but only the assertions above
are airtight.

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

var _ via.PageMetaer = (*Ticket)(nil) // duck-typed like OnInit — pin it
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
  an unsafe target from `OnInit` answers 500. A SAFE target navigates from
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
  `data-bind`), and the plain action path applies the body AFTER its discovery
  render. So a `Display()`-only or unrendered signal cannot be set by a client,
  and a POST cannot open the branch that authorizes it.
- **Never gate on a signal.** A `Bind()`ed `Signal` is client state by
  definition — whatever the client last set it to. It is a fine switch for a
  disclosure the user controls; an authorization gate belongs on session or
  database state, read in `OnInit`.
- **`Guard` is the protected-page mechanism**, not an `OnInit` check:

  ```go
  type Guard func(*Ctx) error

  func Protect(g ...Guard) MountOption // per Mount
  ```

  A `Guard` runs before `OnInit`, on all four transports a mount answers — the
  page GET, a plain action, a live action over an open stream, and the SSE
  connect. `OnInit` runs on every one but the live action, so a `Guard` is
  what re-authorizes that one: a session revoked after connect still passes
  `OnInit` (it never runs again on that stream) but is caught by the `Guard`
  on the next click. A mount's guards run in the order passed to `Protect`,
  stopping at the first denial.

  A `Guard` denies by returning `via.ErrForbidden` for "you may not do this"
  (403, `via.ReasonForbidden` through `WithErrorPage`) or by queuing
  `ctx.Redirect` for "please sign in" (303 on a page GET, a navigation script
  on a Datastar action) — the same two shapes `OnInit` uses, and answers the
  same way. On the SSE connect a denial is always a plain 403, whether from a
  `Guard` or from `OnInit`, because `fetch` follows a 303 and would deliver
  the target page's HTML as the stream body.

  A guard gates requests, not open streams. A guard denial does not tear down
  an already-open stream: `Tick` and `Listen` keep pushing until the tab next
  acts and is denied, closes, or the router shuts down. The session a `Guard`
  sees is writable, but there is no header setter — a live action's answer may
  be an SSE frame on a connection the guard did not open, so a response header
  belongs in a `func(http.Handler) http.Handler` wrapping the `*Router`.

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
serves HTTP, so it goes straight into `http.Handle`. `example/chat` runs the
whole path end to end: SIGINT, `r.Close()`, `srv.Shutdown`.

## The feature map

Each capability, the example that demonstrates it, and the traps that come with
it.

- **Plain core** (`example/counter`): an action's response self-classifies —
  element-patch when the render changed, `204` when it didn't.

- **Reactive handles** (`example/greeting`): client-resident `Signal[T]` with
  handle-identity wire names. `Bind()` and `Display()` share one name, so the
  greeting updates live as you type, entirely client-side. `When`/`Each` render
  conditionals and lists.
  - A signal's wire name is its Go FIELD name — `count`, and `chat__draft` for
    one inside a `Chat` child. Not its render order, so a `Bind()` behind a
    `When` (a wizard step, a branch that only sometimes renders its input)
    keeps its own slot instead of inheriting one from whatever rendered first.
  - `sig.Ref()` returns that name as a Datastar expression (`"$count"`) for
    hand-written attributes: `h.Data("show", p.Open.Ref())`.
  - A `Signal` must be a plain field of the composition. One reached through a
    pointer, slice, array or map field, or held by a composition whose `View`
    has a value receiver, has no field offset to name itself by. via walks the
    composition type at `Mount`/`Child`, so those **panic at startup**, not
    once per request.
  - The one case the type walk cannot see is a Signal behind an `interface`
    field; that still panics on the first render that binds it.

- **Live children + `State[T]`** (`example/pulse`): render a `State[T]` or
  register a `Tick` and a composition becomes a live child with a per-tab SSE
  stream. `State[T]` is server-authoritative, read from the pure View and
  element-patched on change; `Tick` drives the push.
  - `Tick`/`Listen`/action handlers all run on the connection's one goroutine.
    A handler that blocks (I/O, an unbounded loop) stalls every other tick,
    action, and push on that same connection, and delays that connection's
    shutdown until it returns.

- **Interactive live actions** (`example/chat`): a live-child action routes to
  *this* connection's child — via the `via_tab` handshake, an unguessable
  per-connection id echoed in the `viatab` signal every `@post` already carries
  — mutates its state, and the result is pushed over its SSE.
  - The element push omits `data-signals`, and deliberate signal changes ride a
    signal-patch, so a fan-out never clobbers what a user is typing.

- **Multi-user fan-out** (`example/feed`, `example/chat`): an in-process
  `via/topic.Topic[T]` broker + `ctx.Listen` / `ctx.OnDispose`: one publish
  fans out to every connected child.

- **Sessions** (always available): `ctx.Session().Put[T]`/`Get[T]`/`Delete[T]`,
  a typed per-browser store keyed by Go type (no tags, no reflection — a
  typed-nil sentinel), behind a signed-HMAC cookie issued lazily on the first
  write. Apps that never store anything stay cookieless.
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
    random per-process key (warned on first use). The key signs the COOKIE. The
    DATA lives in a `SessionStore`, and the default store is this process's
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
    stream and reloads to re-bootstrap when Datastar gives up.

- **Live-child multiplexing** (`example/dashboard`): child sub-compositions as
  plain struct fields: `via.Child(p.Clock)` in the parent's `View`. Each child
  gets its own `OnInit`. A child that neither ticks nor holds `State` is a plain
  in-place component; one that does is a live child.
  - All the live children on a page share the tab's *one* SSE stream on one
    goroutine. Each re-renders and patches only its own region (`#via-i{n}`,
    pushed in place — its container is never morphed by a parent's patch), its
    actions route by child id + the tab handshake, and its signals are
    slot-scoped so siblings never collide.
  - Ownership is by value: the parent's field literal seeds the child's
    dependencies (a shared `*Topic`, a store) at registration, each connection
    gets its own copy (value state stays per-tab), and pointer deps are the
    deliberate sharing channel. Generic layouts (`Shell[C]{Body C}`) compose
    one shell with any page.
  - **Known limitation:** a live child cannot itself embed a further live
    child. Nesting is one level deep (the root, or a live child directly under
    a plain root, or through further plain `via.Child`s); it panics at render,
    loud and early, rather than misroute an action. Plain composition still
    nests to any depth. Nested live composition (a dynamic set of live children
    addressed by identity) is a deferred feature.
  - `State` is per connection: a native `PostForm` submit is a navigation,
    opens a new connection, and reseeds it. Persist through the session or a
    shared pointer dep, or `Redirect` instead of returning a page.
  - A `Child`'s identity is its **child key**: its ordinal among its own
    parent's `Child` calls, composed onto the parent's key. The root's children
    are `0`, `1`, …; a child of `0` is `0-0`. One key drives the container id
    (`via-i0-0`), the signal prefix (`i0-0_`) and the dispatch address
    (`/_via/a/0-0/…`), so re-rendering any subtree on its own numbers its
    descendants exactly as the whole-page render did.
  - A child's key must be the same on every render for the life of a
    connection. A `When` around a `Child` shifts its later **siblings**'
    ordinals, so such a `When` must depend only on data fixed by `OnInit` or
    the field literal — never on time, a client signal, or shared state that
    changes while the page is open.

- **Per-row list actions** (`example/poll`): a row's button carries the row's own
  datum — `via.OnArg("click", l.Delete, item.ID)` — and the handler receives it as a
  typed parameter, `func(*via.Ctx, int)`. Identity rides with the click, so a list
  that grows, shrinks, and **reorders** never misroutes: the value (not the
  positional slot) picks the row. Still a named method value — no `&`, no closure.

- **Multi-page apps + auth + uploads** (`example/forum`): `via.NewRouter()` with
  `via.Mount(r, "/path", Page{})` serves a whole app behind one handler, each
  page's actions namespaced under its mount.
  - `OnInit(*Ctx) error` is the per-request hook that loads session/path data
    into a plain page before its ctx-free `View`. Return `via.ErrNotFound` for
    a vanished record (404); any other error answers 500, and the View never
    renders a lie.
  - `Profile`, `Forum` and `ThreadPage` each redirect an anonymous visitor
    from their own `OnInit`; a shared `via.Protect(…)` `Guard` would express
    the same check once, mount- or router-wide.
  - `via.PostForm(handler, …)` renders a **native**, always-multipart form
    whose submit runs server-side, and `ctx.Redirect("/…")` issues a 303 — the
    server-rendered auth flow the bundled Datastar can't do. The same form
    handles the avatar upload: a file `<input>` just works, read with stdlib's
    `ctx.Request().FormFile("avatar")`.
  - `ctx.Param[int]("id")` reads the named `{id}` segment of `"/thread/{id}"`.

`example/chat` is the most complete live example: a multi-user chat room with a
presence count, in ~60 lines.

**Restarts and deploys.** Two separate things have to survive: the cookie and
the data behind it. A stable key (`WithSessionKey` / `VIA_SESSION_KEY`) keeps
the COOKIE valid across restarts and pods; it is signed rather than stored. The
DATA lives in a `SessionStore`, and the default one is a map in this process's
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
TTL support is still correct. Values go through `encoding/json`, keyed by the
Go type `Session.Put` stored them under, so a type a pod cannot decode reads
back as absent rather than as someone else's value.

The CSP is a pure function of the Head, so pods with different keys still serve
identical policies. Live-child state is in-memory and per-connection: a deploy
drops the stream, and the client reconnect manager shows "Reconnecting…" and
reloads to re-bootstrap, so the page comes back from server truth rather than
replayed frames.

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
have sent, logged once. See `example/forum`.

**Action URLs address a handler, not a render position.** `child` is `r` for the
page root and the acting child's key otherwise, and `id` in
`/_via/a/{child}/{id}` is a hash of the handler method's own Go name, so it is
the same across renders, instances and builds. A row's datum rides along in
`?a=`. A list that grew or shrank since a tab painted therefore keeps every URL
that tab is holding valid. Dispatch answers `410 Gone` — on both the plain and
live paths — for an id the current render does not bind (a closed branch, or an
`OnInit` that failed to restore the state the `View` branches on; the 410 names
the handlers that ARE bound), an unknown child, or a live action with no
connection for its tab. Datastar resolves a non-2xx response silently and moves
on; a native `<form>` submit shows the browser's own error page.

**Stream caps.** The SSE GET stream applies the same origin check as the action
POST and is capped at a fixed number of concurrent connections (10,000; over the
cap returns 503). The cap is router-wide rather than per IP, so an anonymous
client can open enough connections on its own to fill the cap and 503 everyone
else. A per-IP cap is deferred past v0.8.

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
what re-bootstraps the page from server truth. See **Live-child multiplexing**
for the one-level-deep nesting limit.
