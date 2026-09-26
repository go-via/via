# Migrating from v0.7 to v0.8

v0.8 is a rebuild. The module never left v0.x, where Go permits breaking
changes without a module-path change — so `go get -u` walks v0.7.0 → v0.8.1 as
an ordinary bump and hands you a tree that shares almost no identifiers with
the one you were using. No tooling will warn you. There is no compatibility
shim and no deprecation window: v0.7 is preserved on the `v1` branch, and you
can pin it indefinitely.

So this document is not a rename list you can apply mechanically. Most v0.7
code does not port line by line, because the things that changed are the
ideas, not the spellings. Read the four shifts below first; the mapping table
after them assumes them.

**Short version:** replace `h.Text` with `h.Str`; delete
your `View(ctx)` parameter and load what the view reads in `OnInit`;
`StateTab` becomes `State`, so drop `.Op(ctx)` and replace `Read`/`Write` with
`Get`/`Set`; `via.New()` and `via.Mount[Page](app, …)` become
`via.NewRouter()` and `via.Mount(r, …, Page{})`; move `path:"…"` tags to
`ctx.Param` in `OnInit`, because nothing flags a leftover tag; keep the `on`
package (`on.Click(p.Inc)` reads the same). The compiler finds most of the
rest. [What the compiler won't catch](#what-the-compiler-wont-catch) lists the
changes it cannot see.

## The four shifts

### 1. State is bare; `ctx` is for the request

v0.7 threaded a `ctx` through every state operation: `p.Hits.Op(ctx).Inc()`,
`c.Step.Read(ctx)`, `c.Hits.Write(ctx, 0)`. The argument was load-bearing
plumbing: it carried the tab identity the value was scoped to.

v0.8 makes state carry its own scope, so mutators are bare:

```go
// v0.7
func (c *Counter) Inc(ctx *via.Ctx) { c.Hits.Op(ctx).Add(c.Step.Read(ctx)) }

// v0.8
func (c *Counter) Inc(ctx *via.Ctx) { c.hits.Set(c.hits.Get() + c.step.Get()) }
```

`ctx` still exists and is still passed to actions; it is the *request*, and you
use it for sessions, path params and subscriptions. It is no longer how state
finds itself.

The knock-on effect is the one to plan for: `via.State[T]` is per-connection
state, and **rendering one makes its unit live**, so the page streams over SSE.
v0.7's per-tab `StateTab` was a plain value; v0.8 asks you to say where the
value lives. For a value that is server state, inject a plain store
as a pointer field and let the re-render read it. That is a design change, not
a syntax change.

The numeric shapes are gone with the `ctx`: there is no `SignalNum`,
`StateTabNum`, `StateSessNum`, `StateAppNum`, no `.Op(ctx)` and no
`Add`/`Sub`/`Inc`/`Dec`/`Clamp`. Write the arithmetic in Go.

### 2. `View` is pure and takes no context

v0.7: `View(ctx *via.CtxR) h.H`. v0.8: `View() h.H`. `CtxR` is gone entirely.

This is the load-bearing constraint of the rewrite: **anything your view
needs must be a field on the composition before `View` is called.** The hook
for that is `OnInit(*Ctx) error`, which runs per-request before `View`. v0.7
logged an `OnInit` error and rendered the page anyway; v0.8 stops: return
`via.ErrNotFound` for a 404, anything else for a 500.

```go
// v0.7: a path:"id" tag filled the field before OnInit
type Page struct {
    ID int `path:"id"`
}

// v0.8: OnInit reads the param and loads what View renders
func (p *Page) OnInit(ctx *via.Ctx) error {
    u, err := p.users.Find(ctx.Param[int]("id"))
    if err != nil { return via.ErrNotFound }
    p.user = u
    return nil
}
func (p *Page) View() h.H { return h.H1(h.Str(p.user.Name)) }
```

A leftover `path:"id"` tag compiles and leaves the field at its zero value.
`query:"q"` tags go the same way; read `ctx.Request().URL.Query()` in `OnInit`
instead. The query string is empty on actions, so list state
(filter, page, sort) belongs in the path or the session.

The v0.7 `Composition`, `Initializer`, `Connector` and `Disposer` interfaces are
gone as named types. What replaced them: a composition is anything with
`View() h.H`, and `OnInit(*Ctx) error` is the lifecycle hook,
on a page and on every embedded child. There is no `Connector` and no `Live`
interface: a composition is a live child when it *acts* like one, meaning its
`OnInit` registered a `ctx.Tick`/`ctx.Listen` or its `View` rendered a
`State[T]`/`List[E]`. `via.Stream(ctx, d, fn)` is `ctx.Tick(d, fn)` in
`OnInit`. `OnDispose(ctx)` becomes `ctx.OnDispose(fn)`, registered in `OnInit`;
`ctx.Listen` disposes itself with the child.

Both hooks are duck-typed. That is the one place in this migration where
getting a port wrong does not fail to compile: a method with the wrong name or
the wrong signature is not the hook, so it never runs. There is nothing to
assert against — the hook interfaces are unexported — so the boot check is
the safety net: `Mount`/`Child` walk the composition type at startup and
catch two of the three ways to get this wrong:

- **Panics**: a method literally named `OnInit` or `OnReload` whose signature is
  not `func(*via.Ctx) error`, or one named `PageMeta` that is not
  `func() via.Meta`.
- **Warns**: a near-miss name that carries the exact hook signature while
  the correctly-named method is absent. A near miss is a hook name one typing
  slip away (a letter added, dropped or changed, two adjacent letters swapped,
  or the wrong case: `OnRelaod`, `Oninit`), or one of these aliases in any
  case: `Init`, `Initialize`, `Initialise`, `OnInitialize`, `OnInitialise`,
  `OnStart` and `Connect` for `OnInit`, and `Reload`, `OnReloaded`,
  `Refresh`, `OnRefresh`, `Reinit`, `OnReInit` for `OnReload`, and `Meta`,
  `Metadata`, `PageMetadata`, `GetPageMeta`, `DocumentMeta`, `PageInfo` for
  `PageMeta`. A v0.7 `OnConnect(ctx *via.Ctx) error` is warned about with or
  without an `OnInit` next to it, and the warning names what replaced it.
  Each warning is logged once per type.
- **Silent**: everything else. A v0.7 `OnDispose(ctx *via.Ctx)` has an
  action's shape, so it is an ordinary method nothing calls, and the type walk
  says nothing. A `Signal` behind an interface field is likewise invisible to
  the walk and only panics on the first render that binds it.

`OnInit` runs per request: on the GET, on every
action, and on the SSE connect. Pair a connection-scoped acquire with
`ctx.OnConnect(fn)` and its release with `ctx.OnDispose(fn)`; both run only
when a stream opens.

### 3. Composition is `via.Child`, and roots are taken by value

v0.7 rendered a child by calling its `View` by hand, `p.A.View(ctx, …)`,
passing whatever the child needed as extra arguments. In v0.8 a child
composition is a plain struct field, rendered explicitly, with its own
`OnInit`; what used to be arguments are fields:

```go
// v0.8
type Page struct{ Sidebar Sidebar }
func (p *Page) View() h.H { return h.Div(via.Child(p.Sidebar), ...) }
```

`via.Handler` and `via.Mount` take the root **by value** (`Counter{...}`, not
`&Counter{...}`); via passes a pointer to the per-request instance, which is why
action method values like `c.Inc` need no `&` at the call site. Generic layouts
are ordinary generic structs: `Shell[C]{Body C}`.

A live child may be embedded directly by a plain root, or by a
further plain `via.Child` under one. Each streams and patches independently
over the page's one connection. **Known limitation:** a live child cannot be
embedded inside another live composition, and a live child's own `View`
cannot itself call `via.Child`; either panics at render. The second rule is a
GET/discovery-render check only — a live child's own push re-checks
live-under-live alone, so a hydrated signal may still legally grow a *plain*
grandchild there; growing a *live* one still panics. Nested live composition
(a dynamic set of live children keyed by identity) is a deferred feature;
plain composition still nests to any depth.

### 4. Fan-out is scoped to a topic

v0.7 had process-wide broadcast: `app.Broadcast(script)`,
`via.BroadcastSignal(app, sig, val)`, `app.BroadcastSignals(map)`,
`app.BroadcastNotify(msg)`. All removed. v0.8 fans out through a typed topic
that children subscribe to:

```go
var Posts = topic.New[Post]()          // package topic

func (p *Feed) OnInit(ctx *via.Ctx) error {
    ctx.Listen(Posts, p.onPost)
    return nil
}
func (p *Feed) onPost(ctx *via.Ctx, post Post) { p.items.Append(post) }
```

`ctx.Listen` is subscribe + pump + auto-dispose in one line, scoped to the
child's lifetime. Publishing is a topic send from anywhere in your app. The
difference that matters: nothing can now push to a page that did not ask.

v0.7's `StateApp[T]` — one value shared by every connection — is your own
store plus a `topic.Topic[T]` and `via.StateTrack(room, load)` on the field:
the store holds the value, the topic announces each change, and the tracked
`State` seeds this connection's copy and follows.

`StateSess[T]` is a topic per user, keyed by `ctx.Session().ID()` — the stable
identity behind the cookie — and `State.Track` in `OnInit` (the method, not the
literal: the topic depends on the request). Data that only has to be read, not
pushed, still belongs in the typed session.

`ctx.Redirect` navigates from anywhere: `OnInit`, `OnReload`, a native form
submit (303 before the View ever renders) and a Datastar `@post` action alike.
A `@post` answers with a one-line `location.assign` script, which Datastar
executes through its `text/javascript` response branch; the target rides in the
`datastar-script-attributes` header, so the script bytes are constant and the
strict CSP admits them by SHA-256. An unsafe target is still dropped loudly and
never reaches the client.

## Mapping

Ordered by how early a port hits each change. "Caught by" says what tells you:
the compiler, a panic or a warning when `Mount` walks the type at startup, or
nothing.

| v0.7 | v0.8 | Caught by |
| --- | --- | --- |
| `h.Text(s)`, `h.T(s)`, `h.Textf(f, …)` | `h.Str(s)`, `h.Str(fmt.Sprintf(f, …))` | compiler |
| `View(ctx *via.CtxR) h.H` | `View() h.H`; load what it reads into fields in `OnInit` | compiler |
| `via.New()`, `via.Mount[Page](app, "/")` | `via.Handler(Page{})`, or `via.NewRouter()` and `via.Mount(r, "/", Page{})` | compiler |
| `app.Start()`, `app.Run()`, `WithAddr`, the timeout options | `http.ListenAndServe(addr, r)`, or your own `http.Server` | compiler |
| `StateTab[T]` and its `Num`, `Str`, `Bool`, `Slice`, `Map` shapes | `State[T]`; rendering one makes the unit live | compiler |
| `.Read(ctx)`, `.Write(ctx, v)`, `.Update(ctx, fn)` | `.Get()`, `.Set(v)` | compiler |
| `.Op(ctx).Inc()`, `.Add(n)`, `.Toggle()`, … | Go on the value: `s.Set(s.Get() + n)` | compiler |
| `s.Text(ctx)`, `sig.Text()`, `sig.TextSpan()` | `s.Display()` | compiler |
| `SignalNum[T]` and the other `Signal` shapes | `Signal[T]` | compiler |
| `via:"name,init=v"` field tag | `via:"init=<json>"`; the wire name is the field name, and a string seed is JSON: `init="all"` | panic at Mount |
| a child rendered by hand: `p.A.View(ctx, …)` | `via.Child(p.A)`; what the child's `View` took as arguments becomes its fields | compiler |
| an action `func(*via.Ctx) error`, `WithActionErrorHandler` | `func(*via.Ctx)`; handle the error inside | compiler |
| an `OnInit` error, logged while the page renders anyway | an `OnInit` error aborts: `via.ErrNotFound` answers 404, any other 500 | silent |
| `path:"id"` field tag | `ctx.Param[T]("id")` in `OnInit`, stored in a field | silent |
| `query:"q"` field tag | `ctx.Request().URL.Query()` in `OnInit`; it is empty on actions, so list state belongs in the path or the session | silent |
| `h.If(cond, node)` | `via.When(cond, p.part)`: the node becomes a method returning `h.H`, called only when cond holds | compiler |
| `h.When(cond, fn)` | `via.When(cond, fn)` | compiler |
| `h.IfElse`, `h.WhenElse`, `h.Switch`, `h.Maybe` | a Go `if` or `switch` in a method returning `h.H` | compiler |
| `h.Each(items, fn)`, `h.EachIndexed`, `h.EachSeq` | `via.Each(items, p.row)`, or a loop | compiler |
| `h.Fragment(…)` | pass the nodes to the parent element, or collect a `[]h.H` and spread it | compiler |
| `on.Debounce("250ms")`, `on.Throttle("1s")` | a `time.Duration`: `on.Debounce(250*time.Millisecond)` | compiler |
| `on.Key("Enter", fn)` | `on.Keydown(fn)`, which has no key filter | compiler |
| `on.Indicator(sig)`, `on.Confirm`, `on.SetSignal` | `h.DataIndicator(sig.Ref())`; `Confirm` and `SetSignal` are gone | compiler |
| `sig.Show()`, `sig.ShowUnless()` | `h.DataShow(sig.Ref())`, `h.DataShow(sig.Ref().Not())` | compiler |
| `sig.Class(n)`, `sig.Style(p)`, `sig.Attr(n)` | `h.DataClass(n, sig.Ref())`, `h.DataStyle(p, sig.Ref())`, `h.DataAttr(n, sig.Ref())`; `expr.Class` in a `CS` handler when no signal is needed | compiler |
| `h.DataShow(f, args…)`, `h.DataClass(n, f, args…)`, `h.DataOnClick(f, …)` | an `expr.Expr`: `h.DataShow(e)`, `h.DataClass(n, e)`, `on.ClickCS(e)` | compiler |
| `via.Local("name")` | a `via.SignalCS[T]` field; `Toggle()` is `on.ClickCS(s.Ref().Toggle())` | compiler |
| `via.Computed(k, e)`, `via.Effect(e)` | `h.DataComputed(k, e)`, `h.DataEffect(e)` | compiler |
| `h.Attr(n, v)`, `h.AttrNum(n, v)` | `h.RawAttr(n, v)` | compiler |
| `h.Checked()`, `h.Disabled()`, `h.Required()`, `h.Selected()` | a bool argument: `h.Checked(true)` | compiler |
| `h.ColSpan("2")`, `h.TabIndex("0")`, `h.MinNum(n)`, `h.ValueNum(n)` | `h.ColSpan(2)`, `h.TabIndex(0)`, and the generic `h.Min(n)`, `h.Value(n)` | compiler |
| `h.Classes(…)`, `h.ClassMap(m)`, `h.Styles(…)` | `h.Class(names…)` and `h.Style(css)`, with the names worked out in Go | compiler |
| `h.Tag`, `h.NewTag`, `h.VoidTag` | `h.El(tag, …)` | compiler |
| `h.Raw(html)`, `h.Static`, `h.With` | gone; there is no unescaped HTML node | compiler |
| `h.Title(s)`, the `<title>` element | `PageMeta()` returning `via.Meta{Title: s}`; `h.Title` is now the `title` attribute | silent |
| `sess.Put(ctx, v)`, `sess.Get[T](ctx)`, `sess.Clear[T](ctx)`, `sess.Rotate(ctx)` | `ctx.Session().Put(v)`, `.Get[T]()`, `.Delete()`, `.Rotate()` | compiler |
| one session value per type | one value per session: a second `Put` replaces the first, so put one struct | silent |
| `StateSess[T]` | a topic per `ctx.Session().ID()`, followed with `State.Track` in `OnInit` | compiler |
| `StateApp[T]` | your own store, injected; a `topic.Topic[T]` and `via.StateTrack` to push its changes | compiler |
| `app.Broadcast`, `BroadcastSignals`, `BroadcastNotify`, `via.BroadcastSignal` | `topic.New[T]()`, subscribed with `ctx.Listen` in `OnInit` | compiler |
| `OnConnect(ctx) error` | `ctx.Tick` or `ctx.Listen` in `OnInit`; `ctx.OnConnect(fn)` for work on stream open | warning at Mount |
| `OnDispose(ctx)` | `ctx.OnDispose(fn)`, registered in `OnInit` | silent |
| `via.Stream(ctx, d, fn)` | `ctx.Tick(d, fn)` in `OnInit` | compiler |
| `ctx.Notify`, `ctx.ExecScript`, `ctx.Reload`, `ctx.SyncNow`, `ctx.Patch` | gone | compiler |
| `ctx.Cookie`, `ctx.SetCookie`, `ctx.Writer()` | `ctx.Request().Cookie(name)`; there is no response access, so keep the value in the session | compiler |
| `ctx.Done()` | `ctx.Context().Done()` | compiler |
| `via.File` and `via.Files` fields, `ctx.MultipartReader()` | `via.PostForm` and `ctx.Request().FormFile(name)` | compiler |
| `via.DecodeForm(ctx, &dst)` | a bound `Signal` per field, read with `Get()`; or `via.PostForm` and `ctx.Request().FormValue` | compiler |
| `WithTitle`, `WithDescription` | `PageMeta() via.Meta` on the mounted page | compiler |
| `WithLang`, `app.AppendToHead`, `app.AppendToFoot` | `via.WithHead(via.Head{Lang, Raw, Assets})`; scripts and styles go in `Assets` | compiler |
| `WithPlugins(picocss.…)` | your own CSS in `Head.Assets.Styles` | compiler |
| `WithPlugins(echarts.…)`, `maplibre` | an island: `h.DataIgnoreMorph` and `h.DataEffect` around a script of yours | compiler |
| a Secure session cookie unless `WithInsecureCookies` | Secure only over TLS or `X-Forwarded-Proto: https`; behind a proxy that sends neither set `WithSecureCookies` | silent |
| `app.Use`, `app.Group`, `app.Handle`, `app.HandleStatic` | your own `http.ServeMux` and middleware around the `*via.Router` | compiler |
| `WithLogger(via.Logger)`, `WithMaxRequestBody`, `WithMaxUploadSize` | `WithLogger(*slog.Logger)`, `WithMaxBody`, `WithMaxUpload` | compiler |
| `WithNotFound` | `WithErrorPage` | compiler |
| `WithBackplane`, `StateAppEvents`, `vianats` | gone; state lives in one process | compiler |

## What the compiler won't catch

These compile and start; the first sign is behaviour:

- an `OnInit` error, logged while the page renders anyway → an `OnInit` error aborts: `via.ErrNotFound` answers 404, any other 500
- `path:"id"` field tag → `ctx.Param[T]("id")` in `OnInit`, stored in a field
- `query:"q"` field tag → `ctx.Request().URL.Query()` in `OnInit`; it is empty on actions, so list state belongs in the path or the session
- `h.Title(s)`, the `<title>` element → `PageMeta()` returning `via.Meta{Title: s}`; `h.Title` is now the `title` attribute
- one session value per type → one value per session: a second `Put` replaces the first, so put one struct
- `OnDispose(ctx)` → `ctx.OnDispose(fn)`, registered in `OnInit`
- a Secure session cookie unless `WithInsecureCookies` → Secure only over TLS or `X-Forwarded-Proto: https`; behind a proxy that sends neither set `WithSecureCookies`
- `ctx.Redirect("https://other.example/…")` → dropped and logged; leave the site with `ctx.RedirectExternal`

A leftover `OnConnect(ctx) error` is warned about at `Mount`, with or without
an `OnInit` next to it. A leftover `OnDispose(ctx)` has an action's shape, so
nothing reports it.

## Names deprecated inside v0.8

Port straight to package `on` and `expr.Val`. `via.On` and `via.OnArg` still
compile but are deprecated in favour of `on.Click`, `on.Event` and
`on.WithArg`, and `expr.Lit` is a deprecated alias of `expr.Val`; all three
are removed in v0.9. Package `on` and `expr.Val` are newer than the v0.8.1
tag: on v0.8.1 itself, a click is `via.On("click", p.Inc)`.

## Removed outright

- **Plugins.** picocss and its themes become your own CSS, delivered through
  `WithHead`. echarts and maplibre become an island: a container marked
  `h.DataIgnoreMorph()` that a script of yours drives through `h.DataEffect`.
  There is no plugin interface to reimplement against.
- **`WithoutSSEReconnect`.** The reconnect manager is always on, because an
  app that silently stops updating is worse than one that says so.
- **The `sess` subpackage.** Its functions are methods on `ctx.Session()`, and
  a session holds one value instead of one per type.
- **`StateSess`, `StateApp`, `StateAppEvents`, the numeric shapes and
  `.Op(ctx)`** (shifts 1 and 4).
- **`Broadcast*`** (shift 4).
- **`via.File`, `via.Files`, `ctx.MultipartReader` and `via.DecodeForm`.**
  `via.PostForm` is always multipart, so a file `<input>` needs no separate
  upload type. Read it with stdlib's `ctx.Request().FormFile(name)`, and a
  field with `ctx.Request().FormValue(name)`.
- **`app.Group` and its `Use` middleware.** There is no per-group guard: the
  check moves into `OnInit`, on the page itself. See the worked example below.
- **`WithBackplane`, `vianats` and the key store.** State lives in one process.
- **Most SSE knobs are constants**: keepalive cadence (25s) and the per-frame
  write deadline (10s) are fixed, where v0.7 had `WithSSEHeartbeat` and
  `WithSSEWriteTimeout`. The connection cap (`WithMaxSSEConn`, default 10,000)
  and the pinned deadline (`WithPinnedDeadline`, default 5s) are the two SSE
  options. Open an issue if a deployment needs another one tunable.

## Worked example: protecting a page

```go
// v0.7: middleware on a group
account := app.Group("/account")
account.Use(requireLogin) // func(w, r, next) checking sess.Get[User](r)
via.Mount[Profile](account, "/profile")

// v0.8: the page checks in OnInit
func (p *Profile) OnInit(ctx *via.Ctx) error {
	if _, ok := ctx.Session().Get[User](); !ok {
		ctx.Redirect("/login")
		return nil
	}
	return nil
}
```

`OnInit` runs on every transport a mount answers but a live action over an
already-open stream: a session revoked after connect still passes `OnInit`
(it never runs again on that stream), so `Tick` and `Listen` keep pushing on
it until the tab next acts, closes, or the router shuts down — that gap is
unmitigated. A `Redirect` set inside `OnInit` answers the GET with a 303.

## Worked example: the counter, both ways

v0.7, with per-tab state and the numeric shape:

```go
type Counter struct{ N via.StateTabNum[int] }

func (c *Counter) Inc(ctx *via.Ctx) { c.N.Op(ctx).Inc() }
func (c *Counter) Dec(ctx *via.Ctx) { c.N.Op(ctx).Dec() }

func (c *Counter) View(ctx *via.CtxR) h.H {
	return h.Div(h.Class("row"),
		h.Button(on.Click(c.Dec), h.Text("−")),
		h.Span(c.N.Text(ctx)),
		h.Button(on.Click(c.Inc), h.Text("+")),
	)
}

func main() {
	app := via.New()
	via.Mount[Counter](app, "/")
	app.Start()
}
```

v0.8, the same counter as a `State[int]` (this is the source of the live demo
on go-via.dev):

```go
type Counter struct{ N via.State[int] }

func (c *Counter) Inc(ctx *via.Ctx) { c.N.Set(c.N.Get() + 1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.N.Set(c.N.Get() - 1) }

func (c *Counter) View() h.H {
	return h.Div(h.Class("row"),
		h.Button(on.Click(c.Dec), h.Str("−")),
		h.Output(c.N.Display()),
		h.Button(on.Click(c.Inc), h.Str("+")),
	)
}

func main() {
	http.ListenAndServe(":3000", via.Handler(Counter{}))
}
```

The field tag survives in one form: `via:"init=<json>"` seeds a `Signal`,
`SignalCS`, `State` or `List` at its declared value. The name half of v0.7's
`via:"step,init=1"` is gone — wire names are minted from the field path, never
spelled — and a leftover one panics at `Mount`. The seed is JSON, so v0.7's
`init=all` for a string is `init="all"`.

Reach for `State[T]` and `Signal[T]` when you need per-tab server state or a
client-owned input value. For state every visitor shares, inject your own
store and let the re-render read it.

## Security defaults moved

These are the entries most likely to matter in production. The CHANGELOG has
the full reasoning; the short form:

- **The session cookie is Secure only when via can tell the browser is on
  https.** v0.7 set `Secure` unless `WithInsecureCookies` cleared it. v0.8
  sets it when the request arrived over TLS (`req.TLS != nil`) or a proxy
  says `X-Forwarded-Proto: https` or `Forwarded: proto=https`. Caddy sends
  `X-Forwarded-Proto` by default; nginx needs `proxy_set_header
  X-Forwarded-Proto $scheme`. Behind a proxy that sends neither, set
  `WithSecureCookies`. `WithInsecureCookies` is gone.
- **The origin check is new, and off until you configure it.** v0.7 had no
  origin check; its CSRF defence was the per-tab `via_tab` token. v0.8 adds an
  origin floor, read off `Origin`/`Sec-Fetch-Site`, but accepts every action
  and the SSE connect from any origin, including a request with no origin
  signal, until `WithTrustedOrigin` names one, which switches enforcement on
  for the whole endpoint. The per-tab id is the CSRF token on a live page
  only: a plain action carries an empty `viatab`/`_viatab`, so with the floor
  open a cross-origin `PostForm` submit is accepted. Set `WithTrustedOrigin`
  in production. via logs a warning at startup while none is set.
- **`Ctx.Redirect` follows only on-site targets.** A relative path, or an
  absolute URL on the request's host or a `WithTrustedOrigin` origin. v0.7 and
  v0.8.3 followed any http(s) URL. A redirect to an OAuth provider or a
  payment page now needs `ctx.RedirectExternal(url)`; left as `Redirect`, it
  is dropped and logged, and an `OnInit` answers 500.
- **A response tied to a session is `Cache-Control: private, no-store`**:
  any page, action or stream connect that resolved a session or set the
  cookie, and any live page, which carries its tab id. A `Cache-Control`
  your middleware sets before calling the router is replaced on those
  responses. A plain page with none gets `no-cache`; one you set is kept,
  though a cached copy shares its CSP nonce with every viewer. Action
  responses with no session are left alone; the SSE connect is always
  `no-cache` at least.
- **Sessions are always on** and mint a random per-process key if you configure
  none, warning once. The key signs the cookie; the data lives in a
  `SessionStore` whose default is this process's memory, so surviving a restart
  or spanning pods takes both `WithSessionKey` (or `VIA_SESSION_KEY`) and
  `WithSessionStore`. Session values are now stored as JSON, one value per
  session, so a `Session.Put` value must round-trip through `encoding/json`.
  The idle TTL slides on **every** request that carries a valid session
  cookie — `OnInit` resolves the session eagerly whether or not the page
  reads it — so a session expires only after a full TTL with no request at
  all, rather than after a TTL with no `Get`/`Put`.

## The CSP forbids eval

via's `Content-Security-Policy` no longer carries `'unsafe-eval'`. Each
document gets a fresh nonce, in `script-src` and on `<html data-nonce>`, and
the bundled Datastar (v1.0.4) compiles its expressions under it. Three things
can break:

- **A script of yours that evaluates strings**: `eval`, `new Function`,
  `setTimeout("…")`, or a library that does (some template engines and
  older chart builds). It now throws `EvalError: Refused to evaluate a
  string as JavaScript`. Use a build of the library that does not, or move
  the code off strings. If neither is possible, `via.WithUnsafeEval()` puts
  `'unsafe-eval'` back on every page, next to the nonce; via warns at
  startup while it is set.
- **A second policy from middleware or a proxy** (nginx `add_header`, Caddy
  `header`). Browsers enforce every policy they receive, and one without
  the page's nonce blocks every Datastar expression: the console shows
  `Error: Blocked by CSP.` and no binding works. Remove the second
  `script-src`, or build it from via's header so it carries the same nonce.
  Adding `'unsafe-eval'` to it does not help: with `data-nonce` present,
  Datastar never calls `Function`.
- **A `trusted-types` directive** that does not list `datastar`. Datastar
  creates a Trusted Types policy of that name and fails to start if the
  browser refuses it. Add `datastar` to the allowed names.

## Wire break: action URLs

v0.7 posted every action to `POST /_action/{id}`. v0.8's action endpoint is
`{path}/_via/a/{child}/{id}` with an optional `?a=` row datum. `{child}` is `r`
for the page root, or the acting child's key: its ordinal among its parent's
`Child` calls, composed onto the parent's, so the second `Child` inside the
first is `0-1`. `id` is a hash of the handler method's own Go name
(`main.(*Poll).Vote-fm`) and, for a method of a struct field, that field's
path (`Stats.A`), stable across renders, instances, rebuilds and field
reorders.

Nothing in your code calls this URL, so there is nothing to port. But a tab
left open across the upgrade still posts to `/_action/…`, which v0.8 does not
route: its first click answers 404, and the page comes back correct on reload.

## Wire break: the tab id signal

The tab id — the CSRF token in via's threat model, minted with the page and
adopted by its stream — was the signal `via_tab` in v0.7 and is `viatab` now.
Datastar sends the whole signal store with every `@post`, filtering only names
matching `/(^|\.)_/`, and the server reads the id out of the inbound signals.
`PostForm` is the one exception: a native browser form submit carries neither
Datastar's signals nor its headers, so it carries a hidden `_viatab` field,
bound to `$viatab`, under the same per-mount ownership check.

Nothing in your code touches either, so there is nothing to port. A tab open
across the upgrade posts the old name to the old URL and fails as above.

## Wire break: signal slot names

A `Signal[T]`'s wire name is its Go field path, first rune lowercased —
`count`, `chat__draft` for a signal inside an embedded `Chat`,
`outer__mid__kid__step` for a deeper path. v0.7 used the lower-cased field
name too, but a `via:"name"` tag could override it; v0.8 has no override. The
field offset is the internal key, and the name is resolved once per
composition type at `Mount`/`Child`, never per render.

A plain nested struct joins its path with one underscore, a child boundary
with two — so a parent that binds `p.C.S` in its own View (`c_s`) and also
children `p.C` (`c__s`) keeps the two copies apart, as it must: they are
different structs. A parent holding two fields of the child's type is
ambiguous (`Child`'s argument order need not match declaration
order), so those children fall back to the positional key: `i0__s`, `i1__s`,
and `i0_0__s` for a nested one. The key's own depth separator is `-`
(`via-i0-0`, `/_via/a/0-0/…`), but `-` is not a JS identifier character and
`Ref()` hands slot names straight to Datastar expressions, so the slot spells
it `_`.

`Signal[T].Ref()` is the companion: it returns `"$count"` as an `expr.Expr`
(`h.DataShow(p.Open.Ref())`), so a name you need in markup comes off the struct
instead of out of the rendered HTML. It is also what replaces v0.7's
`sig.Show()`, `sig.Class(…)` and friends.

Nothing in your code writes a slot name either, so again there is nothing to
port; a tab left open across the upgrade holds the old names, posts them, and
the server ignores signals it does not recognise, so the page comes back
correct on reload.

A plain action's patch also declares any slot the pre-action render did
not carry, alongside the ones the action wrote. That is what seeds an input
appearing for the first time in the response instead of leaving it on whatever
the client store already held.

## New startup panics

Each fails loudly rather than misbehaving at runtime, and each can surface on
an upgrade in code that compiled fine before.

- A `via` tag that is not `init=<json>`, or whose value is not JSON for the
  field's type, panics at `Mount`, and so does a `via` tag on a field that is
  not a `Signal`, `SignalCS`, `State` or `List`.
- A `Signal` that is **not a plain field of its composition** panics at
  `Mount`/`Child` — at startup, not once per request. A signal reached through
  a pointer, slice, array or map field, or held by a composition whose `View`
  has a value receiver, has no field offset: its writes would land on memory
  the render discards. via walks the composition type where the app is wired
  and refuses it there. The one shape the type walk cannot see is a Signal
  behind an interface field, which still panics on the first render that binds
  it. The remedy is one line — make the `Signal` (and any child composition
  holding one) a direct struct field, and give `View` a pointer receiver.
  Keyed per-row signal slots remain future work.
- Two fields minting the same slot name panic. A nested `A.B` joins with one
  underscore (`a_b`) and collides with a sibling field `A_b`; an embedded field
  `A`'s own signal `B` joins with two (`a__b`) and collides with a sibling
  `A__b`. Rename one of them.
- `Mount` renders the mounted value once, without running `OnInit`, and
  panics on a wiring mistake that render reaches: a func literal bound once
  per row, a method taken through an interface field or an ambiguous value
  receiver, `h.El("script")`, a Signal with no slot, a child without a
  `View` or with a wrong hook signature. The message starts `via: found at
  Mount`. Any other panic there (a nil dereference of data `OnInit` would
  load) is ignored. The check is best-effort: a mistake behind a branch the
  empty value does not take still panics at the first render that takes it.
- A `Mount` path with a `{name...}` or `{$}` wildcard panics, since a page's
  action and stream routes live under its path, and so does one naming a
  wildcard `{child}` or `{act}`, which the action route reserves. Mounting
  both `/docs` and `/docs/` panics too: they share one action route. A page
  serves its own path only, never a subtree: `Mount(r, "/docs/", …)` does not
  answer `/docs/intro`, and `ServeMux` redirects `/docs` to `/docs/`.

## The page's metadata is a method on the root

v0.7 named the document with the router-wide `WithTitle`, `WithDescription`
and `WithLang` options. In v0.8 `via.WithHead` is router-wide and carries only
`Lang`, raw head markup and shared assets; a mounted page declares its own
document with `PageMeta`:

```go
func (p *ThreadPage) PageMeta() via.Meta {
	return via.Meta{
		Title:       p.subject + " — Forum",
		Description: "Discussion: " + p.subject,
		OG:          map[string]string{"title": p.subject, "type": "article"},
	}
}
```

Four rules:

- It is a **method, not a field**, because real metadata is data-dependent. It
  runs after `OnInit` and after `OnReload`, so the data is loaded. A
  composition that already has a `PageMeta` *field* must rename the field — Go
  forbids a method and a field sharing a name.
- Only the **mounted root's** counts. An embedded child's `PageMeta` is ignored;
  `Child` logs one line when it sees one.
- It shapes the **document**, so it lands on the GET and on a native form
  submit. An SSE push patches inside `<body>` and never rewrites the head.
- `Assets` is the one field that is **not** inert: it decides the mount's CSP,
  is read once at `Mount` off the literal you mounted, and must be a constant of
  the type. Making it depend on request data panics on the first GET.

## Gating on a signal

A `Signal` is client state. It is hydrated from a request only when the render
put it under client control, and only `Bind()` does that. A `Display()`-only
signal, or one the `View` never rendered, does not accept an inbound value on
any path, and the plain action path applies the body after its discovery render
rather than during it. If you were gating a branch on a signal and depending on
the client's value round-tripping, `Bind()` it; if the gate is an authorization
decision, move it to session or database state, where it belonged already.

## A volatile `on.WithArg` arg 410s

A value-carrying action authorizes its `?a=` against the latest render — the
discovery render for a plain action, the last push for a live one. An arg that
render did not bind answers 410 before the handler runs. Apps that bind a
volatile value as an arg (a pagination cursor, a count) are affected: the value
goes stale the moment a render moves it, and the in-flight click 410s. Bind a
stable identity (a row's primary key) and read changing state off the
composition in an argless handler.

## A live root re-inits its plain children every frame

A live root re-renders its whole tree on every pushed frame, and each plain
embedded child is a fresh copy, so its `OnInit` runs once per frame. That is
what keeps such a child from coming back zero-valued, but any side effect in
that `OnInit` repeats at the frame rate. Keep it cheap and pure, or hold the
data on the live root and pass it down the field.

## Known rough edges in v0.8

- **`h.Data` and `<data>` collide**: the `data-*` helper owns the name.
- **A click 410s only when the current render no longer binds its handler.**
  Actions are addressed by handler identity, so a shifting `View()` shape no
  longer invalidates unrelated buttons. What does still 410: a branch that
  stopped rendering that handler at all. The common cause is an `OnInit` that
  does not restore the session/UI state the `View` branches on, so the
  dispatch-time render takes a different branch than the client's — the
  server log names the handlers the render did bind, which is the fastest way
  to see it (the response body names only the id that was asked for: the bound
  list is your Go type and method names, and the client is not entitled to
  them). Datastar resolves a non-2xx response silently; a streaming page
  heals on the next push, a plain one stays dead until reload.
- **Per-connection `State` does not survive a native form submit**: it is a
  navigation and opens a new connection, and the returned page no longer
  pretends it does. Persist across it through the session or a shared
  pointer dep, or `Redirect` instead of returning a page.
- **A native form submit whose action mints the session renders its own
  returned page anonymously**: `OnInit` resolves the session from the
  request's cookie, which predates the `Set-Cookie` the same action
  wrote. `Redirect` after a session-establishing submit instead of returning
  a page directly.
- **A `Child`'s child key — its ordinal among its own parent's `Child`
  calls, composed onto the parent's key — is its identity** (container id,
  signal prefix, dispatch address). A `When` around a `Child` renumbers that
  parent's later siblings when it flips, so it must depend only on data fixed
  by `OnInit` or the field literal, never on time, a client signal, or shared
  state that changes while the page is open.

## Staying on v0.7

The `v1` branch is preserved and its tags still resolve. If you are running v0.7
in production and none of the above buys you anything, pinning is a legitimate
answer:

```
go get github.com/go-via/via@v0.7.0
```

v0.7 is frozen. It will not receive features, and there is no commitment to
backport security fixes — as of this release its `golang.org/x/crypto` is behind
and is not being bumped. Pinning v0.7 means taking on its dependency maintenance
yourself; the affected code is the auth example rather than the library, but
check that against your own build before relying on it.

If you need a specific fix on v0.7, open an issue and ask. That is a
request, not a support guarantee.

v0.8 builds only with Go 1.27+.

## Upgrading from a v0.8 pre-release

Everything above is v0.7 → v0.8. If you are already on v0.8 — pinned to a commit
on this branch before it was tagged — the API kept moving under you during the
last stretch of the rebuild. This section is the diff for that jump: what
renamed, whether the compiler will find it for you, and what a silent one
looks like at runtime.

- **`via.Embed(child)`** → `via.Child(child)`. **Compiler** — `Embed` is gone.
- **`vt.EmbedAction`** → `vt.ChildAction`. **Compiler** — `EmbedAction` is gone.
- **The `{embed}` action-URL segment** → `{child}`. Wire-only; nothing in
  your code names it — see "Wire break: action URLs" above.
- **`via.Handler(...)` returned `http.Handler`** → now returns `*via.Router`.
  **Compiler** for a typed variable (`var h http.Handler = via.Handler(...)`);
  **Source-compatible** for `http.Handle("/", via.Handler(...))`, since
  `*Router` implements `ServeHTTP` — this is what makes `Router.Close()`
  reachable.
- **`Title() string` hook** (briefly `via.Titler`) → `PageMeta() via.Meta`.
  **Warned, not silent** — a leftover `Title() string` is named at boot on
  stderr, once per type, and never called; the build still succeeds.
- **`Head{Title, InlineStyle, ScriptOrigins, StyleOrigins, FontOrigins}`** →
  `Head{Lang, Raw, Assets}`, `Assets{Scripts, Styles, Preload, FontOrigins}`.
  **Compiler** — the old fields don't exist; also new: `Head.Raw` now panics at
  boot if it contains `<script` or `<style` (declare it in `Assets`
  instead).
- **`OnClick`/`OnSubmit`/`OnChange`/`OnClickArg`** → `on.Click(fn)` /
  `on.Submit(fn)` / `on.Change(fn)` / `on.Click(on.WithArg(fn, arg))`.
  **Compiler** — the old names are gone. (`via.On` and `via.OnArg` also work,
  but are deprecated and removed in v0.9.)
- **`via.Live` interface, `OnConnect(*via.Ctx) error`** → one
  `OnInit(*via.Ctx) error` hook, plus `ctx.OnConnect(fn)` for a stream-open
  acquire. **Silent** — `via.Live` no longer exists to assert against, so a
  leftover `OnConnect(ctx *via.Ctx) error` method compiles as dead code
  nothing calls. Symptom: the unit never becomes live from that hook (no
  `Tick`/`Listen` runs, whatever the old `OnConnect` acquired never
  happens), and nothing logs it.
- **`Reloader.Reload(*via.Ctx) error`** → an `OnReload(*via.Ctx) error`
  method. **Warned, not silent** — `Mount`/`Child` recognise `Reload` as a
  near-miss name and log it once at boot; the build still succeeds and the
  method still never runs.
- **`List.Update`** → `List.Append` / `List.Remove`. **Compiler** — `Update` is
  gone.
- **`SignalClientOnly[T]`** → removed, `Signal[T]` is the one client-value
  type. **Compiler** — the type is gone.
- **`Session.Clear[T]()`** → `Session.Delete()`. **Compiler** — `Clear` is
  gone, and a session now holds one untyped value instead of one per `T`.
- **`MemorySessionStore() SessionStore`** →
  `NewMemorySessionStore() *MemorySessionStore`. **Compiler** for the rename;
  the return-type narrowing also matters if you assigned the old call to a
  `SessionStore`-typed var expecting `VersionedSessionStore` behavior
  underneath — that was **silent** (a wrapper built around the interface
  silently took the lossy merge instead of CAS) and the concrete return
  type now makes it impossible.
- **`topic.Sub.C()`** → `Sub.Ready()` + `Sub.Drain()`. **Compiler** — `C` is
  gone.
- **`topic.Sub.Notify(ch)`** → `Sub.WakeOn(ch)`. **Compiler** — `Notify` is
  gone.
- **`topic.Topic.Subs()`** → `Topic.NumSubs()`. **Compiler** — `Subs` is gone.
- **Action ids**: flat page-wide counter + `?v=` shape digest →
  content-addressed id (hash of the handler's Go name), keyed by child path
  (`0-1`, not a flat `n`). Wire-only; nothing in your code builds these
  URLs. A tab left open across the upgrade holds the old shape and gets
  `410` on its first click, then comes back correct on reload — see "Wire
  break: action URLs" above.
- **Signal slot names**: render order (`s0`), then byte offset (`f0`,
  `i0_f0`) → Go field name (`count`, `chat__draft`). Wire-only; nothing in
  your code writes these. Same as above: old names are ignored, not
  matched, so a stale tab's post is silently dropped and heals on reload —
  see "Wire break: signal slot names" above.
- **A `Signal` behind a pointer/slice/array/map field**, or held by a
  composition whose `View` has a value receiver, must now be a plain field
  of a pointer-receiver composition. **Compiler catches nothing here — it's a
  new boot-time panic**, not a rename: `Mount`/`Child` now walk the type and
  panic naming the field. The one shape the walk cannot see is a `Signal`
  behind an interface field, which still only panics on the first render
  that binds it.
- **`via.WithDocumentHead(...)`** → `via.WithHead(...)`. **Compiler** — old
  name is gone.
- **`h.Colspan` / `h.Rowspan`** → `h.ColSpan` / `h.RowSpan`. **Compiler** — old
  names are gone.

Two more from the same stretch. Neither renames anything, so the compiler says
nothing:

- **`Ctx.OnConnect`/`Ctx.OnDispose` called after `OnInit` returns.** Both used
  to append silently to a snapshot nobody reads again — the fn never ran,
  with no log line, while the sibling `ctx.Tick`/`ctx.Listen` already
  warned in the same situation. All four now warn. If you were relying on the
  old silence, you'll see a new stderr line naming the call site; nothing about
  your code needs to change unless the call was too late, in which case
  move it earlier in `OnInit`.
- **A GET of an action URL now answers `405`,** not `400`. If you were
  switching on the raw status code instead of `PageError.Reason` /
  `ReasonMethodNotAllowed`, update the check; anything reading `Reason` is
  unaffected.

If a build breaks and nothing above explains it, the fastest path is `git log
--oneline -- <file>` on the identifier the compiler names — this branch's
history is one rename per commit, so the commit message usually says the new
spelling outright.

## Upgrading from v0.8.3

- **Under `WithTrustedOrigin`, an http `Origin` answers 403 when a proxy
  reports https** (`X-Forwarded-Proto` or `Forwarded`), as over direct TLS.
  An http page of yours that posts to the https host must move to https.
- **`WithTrustedOrigin` panics on a value that is not a bare origin** (a
  path, query, fragment or userinfo; a trailing `/` is fine). Pass
  `https://host[:port]`; such a value never matched an `Origin` anyway.
- **`Ctx.Redirect` to another host is dropped and logged.** Use
  `ctx.RedirectExternal(url)` for OAuth and payment hand-offs. See
  "Security defaults moved".
- **Session-bearing responses and live pages are `Cache-Control: private,
  no-store`**, replacing what your middleware set. See "Security defaults
  moved".
- **A live tab idle past the session's idle TTL reloads at its next
  keepalive**, since an open tab does not slide the window. Raise
  `WithSessionTTL` if tabs stay open longer.
- **A method taken through an interface field, or a value receiver whose
  type the unit holds at two or more fields or at none, panics**: a click
  could run another field's handler. Use a pointer receiver with the
  concrete type in the field. See "New startup panics".
- **Action ids of methods on fields, promoted methods and value-receiver
  methods changed.** A tab open across the upgrade 410s its first click on
  one and reloads; there is nothing to port.
- **`Session.Rotate` reloads the session's other open tabs.** Rotate on
  login, logout and privilege changes, not on every request.
- **`r.Close()` waits without a deadline**, so one blocked handler holds the
  process. Shut down with `errors.Join(r.Shutdown(ctx), srv.Shutdown(ctx))`.
- **A custom `SessionStore` sees one `Load` per open stream every 25s.** Size
  a database store's pool for open streams / 25 queries a second.
- **The pinned-tab log line reads `via: tab pinned`**, not `via: live action
  queue not drained`. Update alerts that match the old text.
- **The CSP forbids eval, and `h.El("script", …)` panics.** See "The CSP
  forbids eval" for what breaks; declare scripts in `Meta.Assets`.
- **An action's query sits in a `data-via-q-<event>` attribute**, not in its
  `@post('…')`. A test that scrapes action URLs from the HTML must join the
  two; `vt` does.
- **`viatab` is set before the stream connects**, in `<body
  data-signals='{"viatab":…}'>`. A client that expected `""` until the first
  frame must read the document; a click before connect waits up to 2s.
