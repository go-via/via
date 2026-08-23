# Migrating from v1 to v0.8

v0.8 is a rebuild, not a release. The module path is unchanged
(`github.com/go-via/via`, no `/v2` suffix), so `go get -u` will hand you a tree
that shares almost no identifiers with the one you were using. There is no
compatibility shim and no deprecation window: v1 is preserved on the `v1`
branch, and you can pin it indefinitely.

So this document is not a rename table you can apply mechanically. Most v1 code
does not port line by line, because the things that changed are the ideas, not
the spellings. Read the four shifts below first; the mapping table after them
will make sense only in their light. Budget a re-read of the README, not an
afternoon of find-and-replace.

**If you only want the short version:** delete your `View(ctx)` parameter, drop
`.Op(ctx)`, replace `Read`/`Write` with `Get`/`Set`, replace the `on` package
with `via.On*`, replace `h.Text` with `h.Str`, replace every typed attribute
except `Href`/`Src`/`Action` with `h.RawAttr`, and expect the compiler to find
the rest. Then read shift 1, because that is the one that will actually change
your design.

## The four shifts

### 1. State is bare; `ctx` is for the request

v1 threaded a `ctx` through every state operation: `p.Hits.Op(ctx).Inc()`,
`c.Step.Read(ctx)`, `c.Hits.Write(ctx, 0)`. The argument was load-bearing
plumbing — it carried the tab identity the value was scoped to.

v2 makes state carry its own scope, so mutators are bare:

```go
// v1
func (c *Counter) Inc(ctx *via.Ctx) { c.Hits.Op(ctx).Add(c.Step.Read(ctx)) }

// v2
func (c *Counter) Inc(ctx *via.Ctx) { c.hits.Set(c.hits.Get() + c.step.Get()) }
```

`ctx` still exists and is still passed to actions — it is the *request*, and you
use it for sessions, path params and subscriptions. It is no longer how state
finds itself.

The knock-on effect is the one to plan for: `via.State[T]` is **island-only**.
Reading or writing it outside a live island panics with a message naming the
fix. v1's per-tab `StateTab` worked anywhere; v2 asks you to say where the value
lives. For a value that is genuinely just server state, the v2 counter example
does not use `State` at all — it injects a plain `*Store` dependency and lets
the re-render read it. That is the idiomatic answer and it is a design change,
not a syntax change.

The numeric shapes are gone with the `ctx`: there is no `SignalNum`,
`StateTabNum`, `StateSessNum`, `StateAppNum`, no `.Op(ctx)` and no
`Add`/`Sub`/`Inc`/`Dec`/`Clamp`. Write the arithmetic in Go.

| v1 | v2 |
| --- | --- |
| `StateTab[T]` | `State[T]` (live islands only) |
| `StateSess[T]` | `via.SessGet` / `via.SessPut` |
| `StateApp[T]` | your own dependency, injected — via does not own it |
| `Signal[T]` | `Signal[T]` (client-owned) or `SignalClientOnly[T]` |
| `*Num` shapes, `.Op(ctx)` | plain Go arithmetic on `Get()` |
| `Read` / `Write` / `Update` | `Get` / `Set` |

### 2. `View` is pure and takes no context

v1: `View(ctx *via.CtxR) h.H`. v2: `View() h.H`. `CtxR` is gone entirely.

This is the load-bearing constraint of the rewrite, so it is worth stating
plainly: **anything your view needs must be a field on the composition before
`View` is called.** The hook for that is `OnInit(*Ctx) error`, which runs
per-request before `View` and can now fail honestly — return `via.ErrNotFound`
for a 404, anything else for a 500. A view can no longer render a lie about
data it failed to load.

```go
// v1: the view reaches for what it needs
func (p *Page) View(ctx *via.CtxR) h.H { return h.H1(h.Text(p.name(ctx))) }

// v2: OnInit loads it, View renders it
func (p *Page) OnInit(ctx *via.Ctx) error {
    u, err := p.users.Find(via.Param[int](ctx, 0))
    if err != nil { return via.ErrNotFound }
    p.user = u
    return nil
}
func (p *Page) View() h.H { return h.H1(h.Str(p.user.Name)) }
```

The v1 `Composition`, `Initializer`, `Connector` and `Disposer` interfaces are
gone as named types. What replaced them: a composition is anything with
`View() h.H`; `OnInit(*Ctx) error` is the per-request hook; `via.Live` is the
one interface, `OnConnect(*Ctx) error`, and it is what makes a composition a
live island. There is no `Dispose` — `via.Listen` auto-disposes with the island.

### 3. Composition is `via.Embed`, and roots are taken by value

v1's `Slot`, `Child[C]`, `NewChild`, `Fill` and the `.Embed` method are all
gone. A child composition is a plain struct field, rendered explicitly:

```go
// v2
type Page struct{ Sidebar Sidebar }
func (p *Page) View() h.H { return h.Div(via.Embed(p.Sidebar), ...) }
```

`via.Register` and `via.Mount` take the root **by value** (`Counter{...}`, not
`&Counter{...}`); via passes a pointer to the per-request instance, which is why
action method values like `c.Inc` need no `&` at the call site. Generic layouts
are ordinary generic structs: `Shell[C]{Body C}`.

One rule to know before you nest: a live island may not be embedded inside
another live island, nor under a live page. The refusal is loud.

### 4. Fan-out is scoped, not global

v1 had process-wide broadcast: `app.Broadcast(script)`,
`BroadcastSignal(app, sig, val)`, `BroadcastSignals(map)`, `BroadcastNotify`.
All removed. v2 fans out through a typed topic that islands subscribe to:

```go
var Posts = topic.New[Post]()          // package topic

func (p *Feed) OnConnect(ctx *via.Ctx) error {
    return via.Listen(ctx, Posts, func(post Post) { p.items.Append(post) })
}
```

`via.Listen` is subscribe + pump + auto-dispose in one line, scoped to the
island's lifetime. Publishing is a topic send from anywhere in your app. The
difference that matters: nothing can now push to a page that did not ask.

## Mapping table

Entries marked **gone** have no replacement — see "Removed outright" below.

| Area | v1 | v0.8 |
| --- | --- | --- |
| Serve | `via.New()`, `via.Mount[Page]` | `via.Register(Page{})` or `via.NewRouter()` + `via.Mount(r, "/p", Page{}, guards...)` |
| Render | `View(ctx *via.CtxR) h.H` | `View() h.H` |
| Per-request hook | `Initializer.OnInit(*Ctx) error` | same signature, now the primary hook |
| Live island | `Connector.OnConnect` + `Disposer.Dispose` | `via.Live` — `OnConnect(*Ctx) error`; disposal is automatic |
| Events | `on.Click(p.Inc)` (package `on`) | `via.OnClick(p.Inc)`; typed data via `via.OnClickArg` |
| Text node | `h.Text("x")` | `h.Str("x")` — and it is generic over `Stringish` |
| Attributes | `h.Class`, `h.Type`, `h.Style`, `h.Min`, … | `h.RawAttr("class", "…")`; only `Href`, `Src`, `Action` stay typed |
| Signal rendering | `sig.Bind()`, `.Text()`, `.TextSpan()`, `.Show()`, `.Class()` | `Bind` remains; the rest are gone — render the value in Go |
| Conditionals | `h.If` | `via.When` |
| Groups | `h.Group` | pass the children directly; every element is variadic |
| Growing lists | `StateTab[[]E]` + `Update` | `via.List[E]` with `Append` |
| Sessions | `sess.Put/Get/Clear/Rotate` (subpackage) | `via.SessPut/SessGet/SessClear/SessRotate` |
| Fan-out | `app.Broadcast*` | `topic.New[T]` + `via.Listen` |
| Path params | — | `via.Param[T](ctx, n)` |
| Forms | — | `via.PostForm` (303), `via.OnUpload` + `via.File` |
| Document shell | theme options, `plugins/picocss` | `via.WithDocumentHead(via.Head{…})` |
| Origin policy | `WithInsecureOrigin` | open by default; `WithTrustedOrigin` enables enforcement |
| Render plumbing | `h.Dyn`, `h.DynAttr`, `h.NewRenderer`, `h.Binder` | **gone** — behind `internal/hcore` |

## Removed outright

- **Plugins**, including `plugins/picocss`. Styling is your own CSS, delivered
  through `WithDocumentHead`. There is no plugin interface to reimplement
  against.
- **Theme options** and `WithoutSSEReconnect`. Themes are CSS; the reconnect
  manager is always on, because an app that silently stops updating is worse
  than one that says so.
- **`Session.Rotate`** as a method — it is `via.SessRotate(ctx)`.
- **The `sess` subpackage** and its `internal/sessbridge` shim.
- **The numeric shapes and `.Op(ctx)`** (shift 1).
- **Signal rendering helpers** beyond `Bind` (shift 1's table).
- **`Broadcast*`** (shift 4).
- **The `h` render plumbing**: `Dyn`, `DynAttr`, `NewRenderer`, `Renderer`,
  `Binder`. If you were building markup dynamically through these, build it
  with the element constructors instead — `h` now has the full HTML5 vocabulary
  (~105 constructors), minus the page-shell tags via owns.

## Worked example: the counter, both ways

v1 — reactive per-tab state, a bound signal, and the `on` package:

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

v0.8 — the count is an injected dependency, the view is pure, and the re-render
is the update mechanism:

```go
type Counter struct{ count *Store } // your type, not via's

func (c *Counter) Inc(ctx *via.Ctx) { c.count.Add(1) }

func (c *Counter) View() h.H {
    return h.Main(h.RawAttr("class", "container"),
        h.P(h.Str("Count: "), h.Str(c.count.Value())),
        h.Button(via.OnClick(c.Inc), h.Str("+")),
    )
}

func main() {
    http.Handle("/", via.Register(Counter{count: &Store{}}))
}
```

Note what is *not* there: no reactive wrapper around the count, no `ctx` in the
view, no signal for a value the server owns. A click POSTs the action, the
action mutates the store, via re-renders the fragment and patches it into the
DOM. Reach for `State[T]` and `Signal[T]` when you need per-tab server state or
a client-owned input value — not as the default container for everything.

## Known rough edges in v0.8

Stated plainly so you can decide whether to wait:

- **Attributes are stringly-typed.** v1's `h.Class`/`h.Type`/`h.Min` are gone
  and `h.RawAttr("class", …)` is the replacement, which is a genuine step back
  in a package whose premise is that markup should not be hand-written. Typed
  helpers are the next ergonomic item; boolean attributes (`disabled`) have no
  good spelling until they land.
- **`h.Data` and `<data>` collide** — the `data-*` helper owns the name.
- **Path params are positional** — `via.Param[T](ctx, 0)`, not by name.

## Staying on v1

The `v1` branch is preserved and its tags still resolve. If you are running v1
in production and none of the above buys you anything, pinning is a legitimate
answer:

```
go get github.com/go-via/via@v0.7.0
```

v1 will not receive features. Security fixes will be considered on request.
