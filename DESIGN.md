# via — reimagined core

Branch: `experiment/bare-core`. A from-scratch reimagining: bare essentials —
a thin layer over `net/http` that adds **effortless composition** and
**Datastar sugar**. Builds on today's Go (1.24+), no experiment flags.

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

Each handle **is an `h.H`** — dropping it in the tree displays its value, with
the render strategy chosen by its type:

- `Signal[T]` → emits a static `<span data-text="$s0">…</span>`. Skeleton is
  sent once; value changes are tiny `{s0: …}` signal patches applied
  client-side. The server never re-renders it.
- `State[T]` → emits the literal server value; on change, that fragment is
  element-patched (server re-renders, Datastar morphs by id).

Variants are explicit methods (`c.Name.Bind()` two-way input, `c.Count.Display()`
when you want it spelled out). Mutation is always through the handle with `ctx`:
`c.Count.Add(ctx, 1)`.

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
        h.H1(c.Count),                            // handle is an h.H — display
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
structural nodes (`h.Div`, `h.Button`, `h.Str("−")`) interleaved with **dynamic slots** — the
only varying points. via compiles the static skeleton once and replays it,
resolving slots per render; the static 95% is precomputed bytes, not a tree
walked each time. A pure, `ctx`-free `View` with stable structure is what makes
this caching sound.

Slot kinds:

- **Display** — a handle dropped in the tree (`h.H1(c.Count)`); strategy by
  handle type (see above).
- **Event** — `via.OnClick(c.Inc)` (and friends). Method value, no string.
- **Bind** — `c.Name.Bind()` two-way input binding.
- **Conditional** — `via.If(c.LoggedIn, h.Str("welcome"))`. Explicit, *not* native `if`, so
  the skeleton stays static and cacheable.
- **List** — `via.Each(c.Items, c.Row)` where `c.Row` is `func(Item) h.H` (a
  method value, also `ctx`-free). Diffing via stable ids + Datastar morph.

## Actions

Wired with **method values**, never strings or closures. `via.OnClick(c.Inc)`
appends the method to a per-render action table and emits
`data-on-click="@post('/_via/a/<id>')"`; the POST dispatches by index. Re-renders
rebuild the table; ids stay consistent with the DOM they were emitted into.

- Stateless page action: POST renders the fragment from request signals + deps,
  responds with the patch. No connection.
- Live island action: mutates server `State`, pushed over the island's SSE.

## Live islands — lifecycle

- **First paint:** the page server-renders the island's initial `View`
  (zero-value/constructor state) so there is no empty flash. The container
  carries `data-on-load="@get('/_via/sse/<island>')"`.
- **Connect:** the SSE opens → via builds the island's per-tab state, runs
  `OnConnect`, starts its streams, and patches the live view in.
- **One SSE per island.** Each island has its own connection and lifecycle.
  (Multiplexing onto one socket is a later optimization, not the model.)
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

sessions, `StateApp`/event-sourcing, backplane/NATS, cross-pod `Broadcast`,
plugins, file uploads, metrics. Apps that need shared data bring their own and
pipe it into an island via `Subscribe`.
