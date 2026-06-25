# via — reimagined core

Shipping as the parallel module `github.com/go-via/via/v2`. A from-scratch
reimagining: bare essentials — a thin layer over `net/http` that adds
**effortless composition** and **Datastar sugar**. Builds on today's Go (1.24+),
no experiment flags.

> Status: slice 1 (the stateless counter) is built and hardened — origin floor,
> nonce'd CSP, body cap, panic-recover, compile-time `View` constraint,
> attribute-name allowlist. Everything below describing islands / SSE /
> `State` / `Stream` / `If` / `Each` / `Embed` is the ratified design, not yet
> code. See `ROADMAP.md` for the sequenced slices and the four keystone
> mechanisms (structural-key descent, pre-connect `State`, same-type `Embed`
> id, browser tier) that gate them.

## Principles

- **Feels like a normal Go HTTP framework.** You register handlers on a mux and
  write structs with methods. via adds composition and the Datastar wiring; it
  does not impose a runtime you have to think about.
- **Server-rendered hypermedia.** The server renders HTML; the browser is a
  rendering surface, not the owner of truth.
- **Stateless by default, live by opt-in.** A page is stateless
  request/response. Server-held state + server push (SSE) is opted into per
  region, as a **live island**.
- **The composition is the single source of truth**; via derives the wiring
  (endpoints, patches, persistence) from it.
- **No reflection. No user-facing identifier strings. No closures** — named
  *method values* are allowed; inline `func` literals are not.
- **The handle type declares the reactivity contract** — and the render
  strategy (signal-bind vs server re-render) follows from it.
- **`View` is pure and `ctx`-free.** A composition is *data + a pure `View`*;
  `ctx` is the imperative edge, confined to actions and lifecycle.

## One mount, one model: stateless page + live islands

There is a single entry point: `via.Register(comp)` returns an `http.Handler`,
and `via.Embed(child)` embeds a sub-composition in a `View`. Both take the
composition **by value** — via copies it into an addressable instance it owns,
so callers never write `&`. **A composition that implements `OnConnect` is a
connection-scoped live island** (own state, own SSE); everything else is a
stateless fragment.

```go
type Live interface{ OnConnect(*via.Ctx) error }   // implementing this ⟺ live

func (d *Dashboard) View() h.H {
    return h.Body(
        h.H1(h.Str("Dashboard")),        // stateless, server-rendered once
        via.Embed(Clock{}),              // implements OnConnect → live island, own SSE
        via.Embed(Feed{Room: d.room}),   // another island, deps via fields
        h.Footer(h.Str("© 2026")),       // stateless
    )
}

func main() {
    http.Handle("/", via.Register(Dashboard{room: room}))
    http.ListenAndServe(":8080", nil)
}
```

- `Register[T](T) http.Handler` and `Embed[T](T) h.H` are generic and
  value-taking; methods stay pointer-receiver, via calls them on its own copy.
  Zero `&` at any call site.
- Liveness is detected by interface assertion (`comp.(Live)`) — *not* reflection.
  The opt-in stays explicit: you write `OnConnect`.
- Fully-static page = no child implements `OnConnect`.
- Fully-live app = the root implements `OnConnect` (the whole page is one island).
- The same model spans the spectrum.

`State[T]` simply *is* "the state of the island you're inside" — which is why it
needs no key and is only valid within a live composition.

## Reactive handles

Declared as fields on a composition struct. Identity = the field's address,
lazy-registered on first use; signal wire-keys and action ids are auto-generated
internally. No keys, no tags, no reflection.

| Handle      | Lives                         | Reaches DOM via           | Valid in        |
|-------------|-------------------------------|---------------------------|-----------------|
| `Local[T]`  | browser only                  | datastar signal, in-page  | anywhere        |
| `Signal[T]` | browser, round-trips per req  | signal-patch (`$autoKey`) | anywhere        |
| `State[T]`  | server memory, per tab/island | element-patch (re-render) | **Live only**   |

The stateless default's state is `Signal`/`Local` (client-resident). Reach for
`State[T]` + `Live` when state must be server-authoritative or pushed.

A handle reaches the DOM through an explicit method — `c.Count.Display()` to
show its value, `c.Name.Bind()` for a two-way input. The render strategy follows
the handle type:

- `Signal[T].Display()` → emits a static `<span data-text="$s0">…</span>`.
  Skeleton is sent once; value changes are tiny `{s0: …}` signal patches applied
  client-side. The server never re-renders it.
- `State[T].Display()` → emits the literal server value; on change, that
  fragment is element-patched (server re-renders, Datastar morphs by id).

`.Display()` is the permanent spelling: the bare `h.H1(c.Count)` form is
unreachable without reflection or a `&` at the call site (the sealed `h.H` tree
plus the per-request by-value copy give the handle no addressable identity in a
pure `View`), so the design commits to the explicit method. Mutation is always
through the handle with `ctx`: `c.Count.Op(ctx).Add(1)`.

> Slice-1 status: the shipped method is `.Node()`; the rename to `.Display()`
> and the `Op(ctx)` mutator land with the reactive-handles slice.

## Compositions

`struct { handle fields; child compositions; deps } + View() h.H + action
methods + optional lifecycle`. `View` is pure and `ctx`-free. Nesting is just
fields:

```go
type Counter struct{ Count via.Signal[int] }     // stateless counter
func (c *Counter) Inc(ctx *via.Ctx) { c.Count.Add(ctx, 1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.Count.Add(ctx, -1) }
func (c *Counter) View() h.H {
    return h.Div(
        h.H1(c.Count.Display()),                  // explicit display method
        h.Button(via.OnClick(c.Dec), h.Str("−")), // literal text via h.Str (escaped)
        h.Button(via.OnClick(c.Inc), h.Str("+")),
    )
}

type Dashboard struct{ Hits, Visits Counter }     // composition = fields
func (d *Dashboard) View() h.H {
    return h.Div(via.Embed(d.Hits), via.Embed(d.Visits))
}
```

`via.Embed(child)` turns a composition into an `h.H` (value-taking, no `&`) and
auto-islands it if it implements `OnConnect`. `via.OnClick(d.Hits.Inc)` carries
the specific child instance as receiver, so it routes and patches the right
subtree automatically.

## Views — the `h` DSL

`h.H` is the single tree type: elements, text, attributes, slots, and handles
are all `h.H`. Element constructors are typed `...h.H` (compile-safe). Literal
text is wrapped with `h.Str` — generic over the `string`/number union, escaped,
emitted as **static** content; handles and attrs are already `h.H` and need no
wrapper; attributes hoist into the tag. A `View` builds a tree of **static**
structural nodes (`h.Div`, `h.Button`, `h.Str("−")`) interleaved with **dynamic
slots** — the only varying points. The performance model is honest: a
value-only `View` is cheap because each `Signal[T]` renders as a one-time
signal-bound span the server never re-renders, and static chrome can be banked
with `h.Static`; a `View` with dynamic shape (`If`/`Each`/`State`) is walked per
render by design. A pure, `ctx`-free `View` with stable structure is what makes
the static caching sound.

Slot kinds:

- **Display** — a handle's `.Display()` dropped in the tree
  (`h.H1(c.Count.Display())`); strategy by handle type (see above).
- **Event** — `via.OnClick(c.Inc)` (and friends). Method value, no string.
- **Bind** — `c.Name.Bind()` two-way input binding.
- **Conditional** — `via.If(c.LoggedIn, h.Str("welcome"))`. Explicit, *not* native `if`, so
  the skeleton stays static and the structural key is reserved whether the
  condition is true or false (see `ROADMAP.md` K1).
- **List** — `via.Each(c.Items, c.Row)` where `c.Row` is `func(Item) h.H` (a
  method value, also `ctx`-free) and each item carries a stable key folded into
  its structural id. Diffing via Datastar morph.

## Actions

Wired with **method values**, never strings or closures. `via.OnClick(c.Inc)`
appends the method to a per-render action table and emits
`data-on:click="@post('/_via/a/<id>')"` — Datastar v1's **colon** key syntax.
The old dash form (`data-on-click`) parses as a nonexistent plugin and is
silently dropped, so the click is dead in the browser while every server test
passes; the colon form is the one true spelling. The POST dispatches by id.
Re-renders rebuild the table; ids stay consistent with the DOM they were emitted
into.

The action endpoint enforces an origin floor (same-origin or an explicitly
trusted origin), a request-body cap, and a panic recover, and the page and patch
responses ship a nonce'd CSP — see slice 1.

- Stateless page action: POST runs the action on a fresh per-request instance,
  re-renders, and the response classifies itself by comparing the post-action
  render to the pre-action one:
  - render changed → **element-patch** (`text/html`, morphed into `#root`);
  - render identical → **`204 No Content`** — the action changed nothing the
    View reads, so there is nothing to send. This is inferred from the rendered
    output (not from via-handle writes, which can't see a mutated dependency),
    so a pure side-effect action costs no wasted patch and needs no annotation.
  No connection.
- Live island action: mutates server `State`, pushed over the tab's SSE.

## Live islands — lifecycle

- **First paint:** the page server-renders each island's initial `View`
  (constructor/zero-value state) so there is no empty flash. The page `<head>`
  carries a single `data-init="@get('/_via/sse')"` that opens **one** stream for
  the tab. (`data-on-load` on a div is dead in Datastar v1 — the on-plugin does
  a literal `addEventListener('load')` and a div never fires load.)
- **Connect:** the SSE opens → via builds each island's per-tab state, runs
  `OnConnect`, starts its streams, and patches the live views in.
- **One multiplexed SSE per tab.** A page with N islands shares ONE connection
  (the HTTP/1.1 ~6-connection cap is a correctness ceiling, not a tunable);
  per-island *lifecycle* is preserved, only the transport is shared.
- **Disconnect:** `OnDispose` fires; streams stop; producers are torn down.
- **Reconnect:** **grace-window resume** — a brief reconnection keeps the
  island's server state; after the window, the island rebuilds from its
  constructor. A blip doesn't wipe a chat scroll.

## Stream — data-intense flows (inside a Live island)

via owns no shared state, so streams pipe the app's own source into an island.

```go
type Chat struct{ Log via.State[[]Message]; room *Room }
func (c *Chat) OnConnect(ctx *via.Ctx) error {
    sub := c.room.Join()
    ctx.OnDispose(sub)                   // sub is a Disposer (Stop), not a func
    ctx.Subscribe(sub.C(), c.OnMessage)  // c.OnMessage is a method value
    return nil
}
func (c *Chat) OnMessage(ctx *via.Ctx, m Message) { c.Log.Append(ctx, m) }
func (c *Chat) View() h.H                          { /* renders c.Log */ }
```

- `Subscribe(src <-chan T, handler)` — drives the island from an external
  source; holds the island's action lock so writes are race-free; **coalesces**
  a burst and flushes once.
- `Tick(d, handler)` — timer-driven updates.
- `OnDispose(Disposer)` — tears down app producers on disconnect.

## Out of core (cut from old via)

`StateApp`/event-sourcing, backplane/NATS, cross-pod `Broadcast`, plugins, file
uploads, metrics. Apps that need durable or cross-pod shared data bring their
own bus/DB and pipe it into an island via `Subscribe`.

Table-stakes return as **blessed, no-reflection sub-packages**, never as core
bloat:

- `via/topic` — an in-process `Topic[T]` fan-out broker for the multi-user
  case (chat, presence). The broker is a sub-package so core keeps its "owns no
  shared state" invariant CI-testable; the island seam (`ctx.Subscribe`,
  `ctx.Tick`, `ctx.OnDispose`) stays in core. Durability/replay/multipod are out
  — put a real bus *behind* the `Topic`.
- `via/sess` — sessions, with the `via_tab` id doubling as the CSRF token.
- `via/router` — typed path params and guarded middleware groups.

Core also keeps a render-only `RenderState[T]` for server-authoritative,
never-client-tamperable values on a non-live page (e.g. a login error).
