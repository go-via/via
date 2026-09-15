# Migrating from v1 to v0.8

v0.8 is a rebuild. The module path is unchanged
(`github.com/go-via/via`, no `/v2` suffix), so `go get -u` will hand you a tree
that shares almost no identifiers with the one you were using. There is no
compatibility shim and no deprecation window: v1 is preserved on the `v1`
branch, and you can pin it indefinitely.

So this document is not a rename table you can apply mechanically. Most v1 code
does not port line by line, because the things that changed are the ideas, not
the spellings. Read the four shifts below first; the mapping table after them
will make sense only in their light. Budget a re-read of the README rather
than an afternoon of find-and-replace.

**If you only want the short version:** delete your `View(ctx)` parameter, drop
`.Op(ctx)`, replace `Read`/`Write` with `Get`/`Set`, replace the `on` package
with `via.On*`, replace `h.Text` with `h.Str`, keep your typed attributes
(`h.Class`, `h.Type`, `h.Style`, `h.Min`, … all still exist, plus 40+ more)
and reach for `h.RawAttr` only when no typed helper covers the attribute you
need; every `via.X(ctx, …)` is now `ctx.X(…)`: `Param`,
`Redirect`, `Listen`, `Session.Put`/`Get`/`Delete`/`Rotate` are
all `Ctx`/`Session` methods now (and `via.Mount(r, …)`
is `r.Mount(…)`, a `Router` method). Expect the compiler to find the rest.
Then read shift 1, because that is the one that will actually change your
design.

## The four shifts

### 1. State is bare; `ctx` is for the request

v1 threaded a `ctx` through every state operation: `p.Hits.Op(ctx).Inc()`,
`c.Step.Read(ctx)`, `c.Hits.Write(ctx, 0)`. The argument was load-bearing
plumbing: it carried the tab identity the value was scoped to.

v2 makes state carry its own scope, so mutators are bare:

```go
// v1
func (c *Counter) Inc(ctx *via.Ctx) { c.Hits.Op(ctx).Add(c.Step.Read(ctx)) }

// v2
func (c *Counter) Inc(ctx *via.Ctx) { c.hits.Set(c.hits.Get() + c.step.Get()) }
```

`ctx` still exists and is still passed to actions; it is the *request*, and you
use it for sessions, path params and subscriptions. It is no longer how state
finds itself.

The knock-on effect is the one to plan for: `via.State[T]` is **child-only**.
Reading or writing it outside a live child panics with a message naming the
fix. v1's per-tab `StateTab` worked anywhere; v2 asks you to say where the value
lives. For a value that is genuinely just server state, the v2 counter example
does not use `State` at all. It injects a plain `*Store` dependency and lets
the re-render read it. That is the idiomatic answer and it is a design change,
not a syntax change.

The numeric shapes are gone with the `ctx`: there is no `SignalNum`,
`StateTabNum`, `StateSessNum`, `StateAppNum`, no `.Op(ctx)` and no
`Add`/`Sub`/`Inc`/`Dec`/`Clamp`. Write the arithmetic in Go.

| v1 | v2 |
| --- | --- |
| `StateTab[T]` | `State[T]` (live children only) |
| `StateSess[T]` | `ctx.Session().Get` / `.Put` |
| `StateApp[T]` | your own dependency, injected; via does not own it |
| `Signal[T]` | `Signal[T]`, client-side reactivity with zero round-trips |
| `*Num` shapes, `.Op(ctx)` | plain Go arithmetic on `Get()` |
| `Read` / `Write` / `Update` | `Get` / `Set` |

### 2. `View` is pure and takes no context

v1: `View(ctx *via.CtxR) h.H`. v2: `View() h.H`. `CtxR` is gone entirely.

This is the load-bearing constraint of the rewrite, so it is worth stating
plainly: **anything your view needs must be a field on the composition before
`View` is called.** The hook for that is `OnInit(*Ctx) error`, which runs
per-request before `View` and can now fail honestly: return `via.ErrNotFound`
for a 404, anything else for a 500. A view can no longer render a lie about
data it failed to load.

```go
// v1: the view reaches for what it needs
func (p *Page) View(ctx *via.CtxR) h.H { return h.H1(h.Text(p.name(ctx))) }

// v2: OnInit loads it, View renders it
func (p *Page) OnInit(ctx *via.Ctx) error {
    u, err := p.users.Find(ctx.Param[int]("id"))
    if err != nil { return via.ErrNotFound }
    p.user = u
    return nil
}
func (p *Page) View() h.H { return h.H1(h.Str(p.user.Name)) }
```

The v1 `Composition`, `Initializer`, `Connector` and `Disposer` interfaces are
gone as named types. What replaced them: a composition is anything with
`View() h.H`, and `Initer` (`OnInit(*Ctx) error`) is the one lifecycle hook,
on a page and on every embedded child. There is no `Connector` and no `Live`
interface: a composition is a live child when it *acts* like one, meaning its
`OnInit` registered a `ctx.Tick`/`ctx.Listen` or its `View` rendered a
`State[T]`/`List[E]`. There is no `Dispose`; `ctx.Listen` auto-disposes with
the child.

Both hooks are duck-typed. That is the one place in this migration where
getting a port wrong does NOT fail to compile: a method with the wrong name or
the wrong signature is simply not the interface, so the hook never runs and
nothing says so. Pin each one you port:

```go
var _ via.Initer = (*Page)(nil)
var _ via.Reloader = (*Page)(nil)
```

`Mount`/`Child` walk the composition type at startup and catch two of the three
ways to get this wrong:

- **Panics**: a method literally named `OnInit` or `OnReload` whose signature is
  not `func(*via.Ctx) error`, or one named `PageMeta` that is not
  `func() via.Meta`. A leftover v1-era `Title() string` is warned about: it is
  no longer a hook and nothing calls it.
- **Warns**: a near-miss NAME that carries the exact hook signature while the
  real interface is unsatisfied. The names it knows are `Init`, `Initialize`,
  `Initialise`, `OnInitialize`, `OnInitialise`, `OnStart` for `OnInit`, and
  `Reload`, `OnReloaded`, `Refresh`, `OnRefresh`, `Reinit`, `OnReInit` for
  `OnReload`, and `Meta`, `Metadata`, `PageMetadata`, `GetPageMeta`,
  `DocumentMeta`, `PageInfo` for `PageMeta`. A v1 `Reloader.Reload` left unrenamed
  is in this set, so it is warned about — and only warned about, on stderr,
  once per type.
- **Silent**: everything else. A leftover `Connector.OnConnect` or
  `Disposer.Dispose` from v1 is now an ordinary method nothing calls; the type
  walk has no name to match it against, so it says nothing at all. A `Signal`
  behind an INTERFACE field is likewise invisible to the walk and only panics
  on the first render that binds it.

The interface assertions above are the only airtight check. Every other
rename in the table below is a removed identifier, so the compiler finds it.

`OnInit` runs per request: on the GET, on every
action, and on the SSE connect. Pair a connection-scoped acquire with
`ctx.OnConnect(fn)` and its release with `ctx.OnDispose(fn)`; both run only
when a stream actually opens.

### 3. Composition is `via.Child`, and roots are taken by value

v1's `Slot`, `Child[C]`, `NewChild`, `Fill` and the `.Embed` method are all
gone. A child composition is a plain struct field, rendered explicitly:

```go
// v2
type Page struct{ Sidebar Sidebar }
func (p *Page) View() h.H { return h.Div(via.Child(p.Sidebar), ...) }
```

`via.Handler` and `Router.Mount` take the root **by value** (`Counter{...}`, not
`&Counter{...}`); via passes a pointer to the per-request instance, which is why
action method values like `c.Inc` need no `&` at the call site. Generic layouts
are ordinary generic structs: `Shell[C]{Body C}`.

A live child may be embedded directly by a plain root, or by a
further plain `via.Child` under one. Each streams and patches independently
over the page's one connection. **Known limitation:** a live child cannot be
embedded inside another live composition, and a live child's own `View`
cannot itself call `via.Child`; either panics at render. Nested live
composition (a dynamic set of live children keyed by identity) is a deferred
feature; plain composition still nests to any depth.

### 4. Fan-out is scoped to a topic

v1 had process-wide broadcast: `app.Broadcast(script)`,
`BroadcastSignal(app, sig, val)`, `BroadcastSignals(map)`, `BroadcastNotify`.
All removed. v2 fans out through a typed topic that children subscribe to:

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

`ctx.Redirect` navigates from anywhere: `OnInit`, `OnReload`, a native form
submit (303 before the View ever renders) and a Datastar `@post` action alike.
A `@post` answers with a one-line `location.assign` script, which Datastar
v1.0.2 executes through its `text/javascript` response branch; the target rides
in the `datastar-script-attributes` header, so the script bytes are constant
and the strict CSP admits them by SHA-256 with no per-response nonce. An unsafe
target is still dropped loudly and never reaches the client.

## Mapping table

Entries marked **gone** have no replacement; see "Removed outright" below.

| Area | v1 | v0.8 |
| --- | --- | --- |
| Serve | `via.New()`, `via.Mount[Page]` | `via.Handler(Page{})` or `via.NewRouter()` + `r.Mount("/p", Page{})` |
| Render | `View(ctx *via.CtxR) h.H` | `View() h.H` |
| Per-request hook | `Initializer.OnInit(*Ctx) error` | same signature, now the ONLY hook, on the page and on every embedded child |
| Live child | `Connector.OnConnect` + `Disposer.Dispose` | no interface: a `Tick`/`Listen` in `OnInit`, or a rendered `State`/`List`; disposal is automatic |
| Events | `on.Click(p.Inc)` (package `on`) | `via.On("click"/"submit"/"change", p.Inc)`; typed data via `via.OnArg(event, fn, arg)` (no `OnInput` or an arg-carrying submit/change — per-keystroke work is a `Signal.Bind` + `On("change"/"submit", ...)`, a per-row toggle is `OnArg`) |
| Text node | `h.Text("x")` | `h.Str("x")`, generic over `Stringish` |
| Attributes | `h.Class`, `h.Type`, `h.Style`, `h.Min`, … | same typed helpers, expanded to ~49 (`h.ColSpan`/`h.RowSpan` carry the Go-style casing); `h.RawAttr` covers the rest |
| Signal rendering | `sig.Bind()`, `.Text()`, `.TextSpan()`, `.Show()`, `.Class()` | `Bind` remains; the rest are gone, so render the value in Go |
| Conditionals | `h.If` | `via.When` |
| Groups | `h.Group` | pass the children directly; every element is variadic |
| Growing lists | `StateTab[[]E]` + `Update` | `via.List[E]` with `Append` |
| Sessions | `sess.Put/Get/Clear/Rotate` (subpackage) | `ctx.Session().Put/Get[T]/Delete[T]/Rotate` |
| Fan-out | `app.Broadcast*` | `topic.New[T]` + `ctx.Listen`; a hand-rolled reader uses `Sub.WakeOn(ch)` and `Topic.NumSubs()` |
| Post-action reload | — | `OnReload(*via.Ctx) error` (interface `Reloader`), run after every action on the unit |
| Session storage | — | `via.SessionStore`, default `via.NewMemorySessionStore()`; implement `via.VersionedSessionStore` for a compare-and-set backend |
| Path params | — | `ctx.Param[T]("name")` |
| Protected pages | — | a session check + `ctx.Redirect` inside `OnInit` (no separate guard mechanism) |
| Forms | — | `via.PostForm` (always multipart, 303), `ctx.Redirect`, `ctx.Request().FormFile` for uploads |
| Document shell | theme options, `plugins/picocss` | `via.WithHead(via.Head{…})` |
| Per-page metadata | — | a `PageMeta() via.Meta` method on the MOUNTED root (`via.PageMetaer`) — title, description, canonical, robots, OG/Twitter, and the page's own assets |
| Per-page assets & CSP | — | `Meta.Assets` (`Script`/`Style`/`Preload`/`FontOrigins`); the CSP is built per mount from `Head.Assets` + the page's own |
| `WithHead{Title}` | — | **gone** — `PageMeta().Title` |
| `WithHead{InlineStyle}` | — | **gone** — `Head.Assets.Styles: []via.Style{{Inline: css}}` |
| `WithHead{ScriptOrigins/StyleOrigins}` | — | **gone** — declare the `Script`/`Style` itself in `Assets`; via derives the origin |
| `WithHead{FontOrigins}` | — | `Head.Assets.FontOrigins` |
| Origin policy | `WithInsecureOrigin` | open by default; `WithTrustedOrigin` enables enforcement |
| Render plumbing | `h.Dyn`, `h.DynAttr`, `h.NewRenderer`, `h.Binder` | **gone** — behind `internal/hcore` |

## Removed outright

- **Plugins**, including `plugins/picocss`. Styling is your own CSS, delivered
  through `WithHead`. There is no plugin interface to reimplement
  against.
- **Theme options** and `WithoutSSEReconnect`. Themes are CSS; the reconnect
  manager is always on, because an app that silently stops updating is worse
  than one that says so.
- **The `sess` subpackage** and its `internal/sessbridge` shim.
- **The numeric shapes and `.Op(ctx)`** (shift 1).
- **Signal rendering helpers** beyond `Bind` (shift 1's table).
- **`Broadcast*`** (shift 4).
- **The `h` render plumbing**: `Dyn`, `DynAttr`, `NewRenderer`, `Renderer`,
  `Binder`. If you were building markup dynamically through these, build it
  with the element constructors instead; `h` now has the full HTML5 vocabulary
  (~105 constructors), minus the page-shell tags via owns.
- **`via.OnUpload` and `via.File`**. `via.PostForm` is now always multipart, so
  a file `<input>` just works. Read it with stdlib's
  `ctx.Request().FormFile(name)`.
- **The SSE knobs are constants**: keepalive cadence (25s), per-frame write
  deadline (10s), and the concurrent-connection cap (10,000) are fixed;
  `WithSessionCookieName` is the only SSE/session option that remains. Open an
  issue if a deployment needs one of these tunable.
- **`Guard`, `RequireSession`, and `Mount`'s `guards ...Guard` parameter**.
  Put the check in `OnInit` and call `ctx.Redirect`; see the worked example
  below. This also fixes a latent bug: a Redirect set inside `OnInit` used to
  be silently dropped; it now issues the 303.

## Worked example: protecting a page (Guard is gone)

```go
// Before
guard := via.RequireSession[User]("/login")
app.Mount("/profile", Profile{}, guard)

// After: the check moves into the page's own OnInit
func (p *Profile) OnInit(ctx *via.Ctx) error {
	user, ok := ctx.Session().Get[User]()
	if !ok {
		ctx.Redirect("/login")
		return nil
	}
	p.user = user
	return nil
}

app.Mount("/profile", Profile{})
```

The check and the data load now live in one function instead of a shared
value passed to every protected `Mount` call — more to type per page, but
one less concept, and a `Redirect` set here is honoured (before, it was
dropped if it came from anywhere but a `Guard`).

## Worked example: the counter, both ways

v1, with reactive per-tab state, a bound signal, and the `on` package:

```go
type Counter struct {
    Hits via.StateTabNum[int]
    Step via.SignalNum[int] `via:"step,init=1"`
}

func (c *Counter) Inc(ctx *via.Ctx)   { c.Hits.Op(ctx).Add(c.Step.Read(ctx)) }
func (c *Counter) Reset(ctx *via.Ctx) { c.Hits.Write(ctx, 0); c.Step.Write(ctx, 1) }

func (c *Counter) View(ctx *via.CtxR) h.H {
    return h.Main(h.Class("container"),
        h.P(h.Text("Count: "), c.Hits.Text(ctx)),
        h.Input(h.Type("number"), c.Step.Bind()),
        h.Button(h.Text("+"), on.Click(c.Inc)),
    )
}
```

v0.8, where the count is an injected dependency, the view is pure, and the
re-render is the update mechanism:

```go
type Counter struct{ count *Store } // your type, not via's

func (c *Counter) Inc(ctx *via.Ctx) { c.count.Add(1) }

func (c *Counter) View() h.H {
    return h.Main(h.Class("container"),
        h.P(h.Str("Count: "), h.Str(c.count.Value())),
        h.Button(via.On("click", c.Inc), h.Str("+")),
    )
}

func main() {
    http.Handle("/", via.Handler(Counter{count: &Store{}}))
}
```

Note what is *not* there: no reactive wrapper around the count, no `ctx` in the
view, no signal for a value the server owns. A click POSTs the action, the
action mutates the store, via re-renders the fragment and patches it into the
DOM. Reach for `State[T]` and `Signal[T]` when you need per-tab server state or
a client-owned input value — not as the default container for everything.

## Security defaults moved

Two defaults are more permissive than v1's, and they are the entries most
likely to matter in production. The CHANGELOG has the full reasoning; the short
form:

- **The origin floor is open by default.** The origin floor is via's check
  that a state-changing request comes from a host you trust, read off
  `Origin`/`Sec-Fetch-Site`. v1 enforced it; v0.8 accepts an action
  from any origin until `WithTrustedOrigin` names one, which switches
  enforcement on for the whole endpoint. `WithInsecureOrigin` is gone; there is
  no secure default left to opt out of. The per-tab id is the CSRF token on a
  LIVE page only: a plain page carries an empty `viatab`/`_viatab`, so with the
  floor open a cross-origin `PostForm` submit is accepted and what actually
  defends it is the session cookie's `SameSite=Lax` (the request arrives
  unauthenticated). If you deployed v1 without thinking about origins,
  **v0.8 needs you to think about them.** via logs one line at startup when the
  floor is open.
- **Sessions are always on** and mint a random per-process key if you configure
  none, warning once. The key signs the COOKIE; the DATA lives in a
  `SessionStore` whose default is this process's memory, so surviving a restart
  or spanning pods takes BOTH `WithSessionKey` (or `VIA_SESSION_KEY`) and
  `WithSessionStore`. Session values are now stored as JSON keyed by the Go
  type, so a `Session.Put` value must round-trip through `encoding/json`. The idle TTL slides on **every** request that carries
  a valid session cookie — `OnInit` resolves the session eagerly whether or not
  the page reads it — so a session expires only after a full TTL with no
  request at all, rather than after a TTL with no `Get`/`Put`.

## Wire break: action URLs

The action endpoint is `/_via/a/{child}/{id}` with an optional `?a=` row
datum. `{child}` is `r` for the page root, or the acting child's key: its
ordinal among its parent's `Child` calls, composed onto the parent's, so the
second `Child` inside the first is `0-1`. Earlier v0.8 builds used a flat
page-wide counter with the root at `0` and children at `n+1`. There is no `?v=`
shape digest and no positional `{n}`: `id` is a hash
of the handler method's own Go name (`main.(*Poll).Vote-fm`), stable across
renders, instances and rebuilds.

Nothing in your code calls this URL, so there is nothing to port. But a tab
left open across the upgrade is holding the OLD URL shape. Its first click
answers `410 Gone` (the old `{n}` segment binds no handler), and the page
comes back correct on reload. Deploy-time impact is one dead click per stale
tab, not a permanently broken page.

The upside is the bug this replaces: the digest folded in the action COUNT, so
on a page backed by a shared store (a poll, a feed, any list with per-row
actions) another user adding or removing a row changed every other open tab's
digest and silently 410'd ALL of its buttons, including untouched ones, until
a reload. Handler-addressed URLs cannot do that.

## Wire break: the tab id is a signal, not a header

The per-connection tab id — the CSRF token in via's threat model — used to
ride as the `X-Via-Tab` request header, spelled out on every single action
binding (`{headers:{'X-Via-Tab':$_viatab}}`, 33 bytes each). Datastar builds
request headers per CALL and offers no ancestor inheritance or config hook, so
there was no way to set it once per page. It DOES send the whole signal store
with every `@post`, filtering only names matching `/(^|\.)_/`, so the
underscore in `_viatab` was the only reason the id wasn't already going along.

It is now the ordinary signal `viatab`, and the server reads it out of the
inbound signals. The header is no longer read at all.

Nothing in your code touches either, so there is nothing to port. A tab open
across the upgrade posts the old header, is not recognised, gets a `410`, and
its reconnect manager reloads it.

Security is unchanged, and deliberately so: as a signal the id sits in the
request body, is set by same-origin JS, and is never auto-attached by the
browser: a synchronizer token, which is what a CSRF token must be. The
`Datastar-Request` header check stays as belt-and-braces and the origin floor
is untouched. `PostForm` is the one exception: a native browser form submit
carries neither Datastar's signals nor its headers, so it keeps its hidden
`_viatab` field, now bound to `$viatab`, under the same per-mount ownership
check.

## Wire break: signal slot names

A `Signal[T]`'s wire name is now its Go FIELD name, first rune lowercased —
`count`, `chat__draft` for a signal inside an embedded `Chat`,
`outer__mid__kid__step` for a deeper path — where v0.8's earlier builds named
it by byte offset (`f0`, `f48`, `i0_f0`) and v1 by render order (`s0`, `s1`).
The offset is still the internal key, so hydration is unchanged; the name is
resolved once per composition TYPE at `Mount`/`Child`, never per render.

A plain nested struct joins its path with ONE underscore, a child boundary
with TWO — so a parent that binds `p.C.S` in its own View (`c_s`) and also
children `p.C` (`c__s`) keeps the two copies apart, as it must: they are
different structs. A parent holding two fields of the child's type is
genuinely ambiguous (`Child`'s argument order need not match declaration
order), so those children fall back to the positional key: `i0__s`, `i1__s`,
and `i0_0__s` for a nested one. The key's own depth separator is `-`
(`via-i0-0`, `/_via/a/0-0/…`), but `-` is not a JS identifier character and
`Ref()` hands slot names straight to Datastar expressions, so the SLOT spells
it `_`. Earlier v0.8 builds spelled it `i0-0__s` and produced an expression
Datastar could not parse.

`Signal[T].Ref()` is the companion: it returns `"$count"` for use in a
hand-written Datastar expression (`h.Data("show", p.Open.Ref())`), so a name
you need in raw markup comes off the struct instead of out of the rendered
HTML.

Nothing in your code writes a slot name either, so again there is nothing to
port; a tab left open across the upgrade holds the old names, posts them, and
the server ignores signals it does not recognise, so the page comes back
correct on reload.

The bug this fixes: slots were claimed in first-render order, so a `Bind()`
inside a `When` (a wizard step, a branch that only sometimes renders an input)
could claim a slot another signal already owned. The input was then wired to
the wrong field: on a streaming page the post wrote the wrong signal, on a
plain page the new input came up holding the previous occupant's value. An
offset is a property of the struct, not of what this render happened to draw,
so a conditional `Bind()` is now safe.

One carve-out:

- `via.Child`'s signature is unchanged: the child copy `Child` already takes by
  value is the offset base, so call sites need no edit.

A plain action's patch also now declares any slot the pre-action render did
not carry, alongside the ones the action wrote. That is what seeds an input
appearing for the first time in the response instead of leaving it on whatever
the client store already held.

## New startup panics

Both fail loudly rather than writing the wrong field at runtime, and both can
surface on an upgrade in code that compiled fine before.

- A `Signal` that is **not a plain field of its composition** panics at
  `Mount`/`Child` — at startup, not once per request. A signal reached through
  a pointer, slice, array or map field, or held by a composition whose `View`
  has a VALUE receiver, has no field offset: its writes land on memory the
  render discards, and the render-order fallback that used to name it aliased
  one signal's slot onto another under a conditional `Bind()`. via walks the
  composition type where the app is wired and refuses it there. The one shape
  the type walk cannot see is a Signal behind an INTERFACE field, which still
  panics on the first render that binds it. The remedy is one line — make the
  `Signal` (and any child composition holding one) a direct struct field, and
  give `View` a pointer receiver. Keyed per-row signal slots remain future
  work.
- Two fields minting the SAME slot name panic. A nested `A.B` joins with one
  underscore (`a_b`) and collides with a sibling field `A_b`; an embedded field
  `A`'s own signal `B` joins with two (`a__b`) and collides with a sibling
  `A__b`. Rename one of them.

## The page's metadata is a method on the root

`via.WithHead` is router-wide, so a multi-page app would serve one `<title>`
everywhere. A mounted page declares its own document with `PageMeta`:

```go
func (p *ThreadPage) PageMeta() via.Meta {
	return via.Meta{
		Title:       p.subject + " — Forum",
		Description: "Discussion: " + p.subject,
		OG:          map[string]string{"title": p.subject, "type": "article"},
	}
}

var _ via.PageMetaer = (*ThreadPage)(nil)
```

Four rules worth knowing before porting:

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
signal, or one the `View` never rendered, no longer accepts an inbound value on
any path, and the plain action path applies the body after its discovery render
rather than during it. If you were gating a branch on a signal and depending on
the client's value round-tripping, `Bind()` it; if the gate is an authorization
decision, move it to session or database state, where it belonged already.

## A volatile `OnArg` arg now 410s

A value-carrying action authorizes its `?a=` against the latest render — the
discovery render for a plain action, the last push for a live one. An arg that
render did not bind answers 410 before the handler runs. Apps that bound a
VOLATILE value as an arg (a pagination cursor, a count) are affected: the value
goes stale the moment a render moves it, and the in-flight click 410s. Bind a
stable identity (a row's primary key) and read changing state off the
composition in an argless `On` handler.

## A live root re-inits its plain children every frame

A live root re-renders its whole tree on every pushed frame, and each plain
embedded child is a fresh copy, so its `OnInit` runs once per frame. That is
what keeps such a child from coming back zero-valued, but any side effect in
that `OnInit` repeats at the frame rate. Keep it cheap and pure, or hold the
data on the live root and pass it down the field.

## Known rough edges in v0.8

Stated plainly so you can decide whether to wait:

- **`h.Data` and `<data>` collide**: the `data-*` helper owns the name.
- **A click 410s only when the current render no longer binds its handler.**
  Actions are addressed by handler identity, so a shifting `View()` shape no
  longer invalidates unrelated buttons. What does still 410: a branch that
  stopped rendering that handler at all. The common cause is an `OnInit` that
  does not restore the session/UI state the `View` branches on, so the
  dispatch-time render takes a different branch than the client's — the
  SERVER LOG names the handlers the render DID bind, which is the fastest way
  to see it (the response body names only the id that was asked for: the bound
  list is your Go type and method names, and the client is not entitled to
  them). Datastar resolves a non-2xx response silently; a streaming page heals on
  the next push, a plain one stays dead until reload.
- **Per-connection `State` does not survive a native form submit**: it is a
  navigation and opens a new connection, and the returned page no longer
  pretends it does. Persist across it through the session or a shared
  pointer dep, or `Redirect` instead of returning a page.
- **A native form submit whose action mints the session renders its own
  returned page anonymously**: `OnInit` resolves the session from the
  request's cookie, which predates the `Set-Cookie` the same action just
  wrote. `Redirect` after a session-establishing submit instead of returning
  a page directly.
- **A `Child`'s child key — its ordinal among its own parent's `Child`
  calls, composed onto the parent's key — is its identity** (container id,
  signal prefix, dispatch address). A `When` around a `Child` renumbers that
  parent's later siblings when it flips, so it must depend only on data fixed
  by `OnInit` or the field literal, never on time, a client signal, or shared
  state that changes while the page is open.

## Staying on v1

The `v1` branch is preserved and its tags still resolve. If you are running v1
in production and none of the above buys you anything, pinning is a legitimate
answer:

```
go get github.com/go-via/via@v0.7.0
```

v1 is frozen. It will not receive features, and there is no commitment to
backport security fixes — as of this release its `golang.org/x/crypto` is behind
and is not being bumped. Pinning v1 means taking on its dependency maintenance
yourself; the affected code is the auth example rather than the library, but
check that against your own build before relying on it.

If you need a specific fix on v1, open an issue and ask. That is a request, not
a support guarantee.

v0.8 builds only with Go 1.27+.
