<p align="center">
  <img src="logo.svg" alt="Via" width="80" height="180">
</p>

# Via

[![Go Reference](https://pkg.go.dev/badge/github.com/go-via/via.svg)](https://pkg.go.dev/github.com/go-via/via)
[![Go Report Card](https://goreportcard.com/badge/github.com/go-via/via)](https://goreportcard.com/report/github.com/go-via/via)
[![CI](https://github.com/go-via/via/actions/workflows/ci.yml/badge.svg)](https://github.com/go-via/via/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Reactive web apps in pure Go. A composition is a struct. Reactive state
is a typed field. Actions are methods. The compiler understands your UI.

Via is the only framework — in any language — that expresses the
client/server reactive split as a Go type. `Signal[T]` is a client
signal, mirrored to a fine-grained Alien Signals graph in the browser
via Datastar. `StateTab[T]`, `StateSess[T]`, `StateApp[T]` are
server-only. Whether a piece of UI state round-trips or doesn't is a
choice made at the field declaration, checked by the compiler, not by a
convention you can grep for. Transport is SSE only — one stream per
tab — so there are no WebSockets to wrestle with a corporate proxy.

![Two browsers, two scopes — `StateTabNum[int]` is per-tab,
`StateAppNum[int]` is shared across every session.](docs/counter-scope.gif)

Best fit: internal tools, admin dashboards, line-of-business apps, and
hobby projects — anywhere you would otherwise reach for Phoenix
LiveView, Hotwire, or htmx + hand-written JS, but want to stay in Go.
Not the right tool for offline-first PWAs, public-facing marketing
sites, or anything that needs to scale horizontally across processes
without sticky sessions.

## The thesis: the client/server split is a Go type

Reasoning: every server-rendered framework eventually faces the
question "is this state client-owned or server-owned?" In every other
ecosystem the answer is a convention. In Via it is the field's type.

Rule: declare client-owned state as `Signal[T]`. Declare server-owned
state as `StateTab[T]`, `StateSess[T]`, or `StateApp[T]`. The compiler
enforces which side owns what. View helpers, actions, and lifecycle
hooks all see the correct shape.

Example:

```go
type Page struct {
    // Client-owned. Lives in the browser's Alien Signals graph.
    // Bind to <input>; mutate without a round-trip.
    Theme via.Signal[string] `via:"theme,init=auto"`

    // Server-owned. Lives only in Go. Re-renders re-emit the value.
    Hits  via.StateTab[int]
}
```

`Theme` mutates inside the browser. Flipping it from an `<input>` does
not POST. `Hits` mutates only through an action handler; the next flush
diffs the View and ships targeted DOM patches over SSE.

## Quick start

```bash
go get github.com/go-via/via
```

```go
package main

import (
    "net/http"

    "github.com/go-via/via"
    "github.com/go-via/via/h"
    "github.com/go-via/via/on"
)

type Counter struct {
    Hits via.StateTabNum[int]
    Step via.SignalNum[int] `via:"step,init=1"`
}

func (c *Counter) Inc(ctx *via.Ctx) {
    _ = c.Hits.Update(ctx, func(n int) (int, error) {
        return n + c.Step.Read(ctx), nil
    })
}

func (c *Counter) View(ctx *via.CtxR) h.H {
    return h.Div(
        h.P(h.Text("Count: "), c.Hits.Text(ctx)),
        h.Input(h.Type("number"), c.Step.Bind()),
        h.Button(h.Text("+"), on.Click(c.Inc)),
    )
}

func main() {
    app := via.New()
    via.Mount[Counter](app, "/")
    _ = http.ListenAndServe(":3000", app)
}
```

No template files. No build step. No hand-written JavaScript. `on.Click(c.Inc)`
is a typed method reference — a typo is a compile error.

## What Via is NOT

Read this section before adopting. The non-goals are deliberate.

- Not an SPA framework. Routes are server-rendered pages. The browser
  receives HTML, not a JSON bundle.
- Not a cluster runtime. `StateApp[T]` and `Broadcast` are
  single-process. Horizontal scaling requires sticky sessions; App
  state is per-pod. There is no built-in fan-out across instances.
- Not offline-first. Disconnect the SSE stream and the tab freezes
  until reconnect — Via is for connected sessions, not PWAs.
- Not a JavaScript replacement. The browser still runs Datastar's
  Alien Signals graph. Via removes hand-written JS for the reactivity
  layer, not the runtime.
- Not a build-step framework. There is no `via generate`. If you want
  a code-gen template language, look at `templ`.
- Not stable yet — pre-1.0, APIs can shift between minor versions.
  ~12 examples. No third-party component library yet. The Datastar
  dependency is load-bearing — Via does not vendor its own client
  runtime.

## Restart and tab survivability

Be specific about what doesn't work. A live tab's state lives in
memory on the server (the `*via.Ctx` and its `session`). It does not
survive a process restart:

- After a deploy, every client's `via_tab` is unknown to the new
  process. The next SSE reconnect 404s and the next action POST 404s.
- The client (Datastar) retries the SSE connection forever, so a user
  watching a stale tab sees it freeze rather than recover. Tell users
  to reload, or pair the deploy with a sticky load balancer that
  drains long enough for tabs to close naturally.
- Sessions are also in-memory; logged-in users will need to re-auth
  unless you back the session store with something durable (not built
  in).

If you need session survivability across restarts, persist the
`sess.Put`-stored payload (e.g. a JWT or an opaque token your auth
layer recognizes) to a database keyed by the `via_session` cookie
value, and rehydrate inside an `OnInit` hook.

## Why Via, not X

|                       | Language | Authoring                | Client runtime              | Build step             | Reactive state                |
|-----------------------|----------|--------------------------|-----------------------------|------------------------|-------------------------------|
| **Via**               | Go       | typed structs + `h` DSL  | Datastar (Alien Signals)    | none                   | typed fields, client + server |
| HTMX                  | any      | HTML + `hx-*` attributes | tiny attribute interpreter  | none                   | server-only, manual           |
| Phoenix LiveView      | Elixir   | EEx templates + macros   | morphdom + tiny JS          | none                   | `assigns` (Elixir-typed)      |
| Hotwire (Turbo)       | Ruby     | ERB + Turbo Streams      | Turbo (WebSocket)           | none                   | server-only, untyped DOM      |
| templ                 | Go       | `.templ` template files  | none (BYO)                  | yes (`templ generate`) | none built-in                 |
| Datastar (direct)     | any      | HTML + `data-*` attrs    | Datastar (Alien Signals)    | none                   | client signals, manual        |

Via is the only row that gives you typed end-to-end state (server +
client) with no build step, SSE-only transport, and a fine-grained
reactive client runtime in the same import. Pick another row if you
want a different language, a template file format, or a different
state-ownership split.

## How reactivity runs

```
   ┌──────────────────────────┐                       ┌──────────────────────────┐
   │  Browser                 │  ◀──── SSE patches ── │  Server (Go)             │
   │                          │     + signal deltas   │                          │
   │  Alien Signals graph     │                       │  Compositions            │
   │   Signal[T] nodes        │                       │   StateTab[T]            │
   │   data-* subscriptions   │                       │   StateSess[T]           │
   │                          │                       │   StateApp[T]            │
   │                          │  ────── POST ──────▶  │   per-tab action mutex   │
   │                          │       actions         │                          │
   └──────────────────────────┘                       └──────────────────────────┘
        view reactivity                                  truth + side effects
```

Two reactive runtimes, one typed boundary.

Server. Go owns truth. `StateTab[T]`, `StateSess[T]`, and `StateApp[T]`
live only on the server; `Signal[T]` is mirrored. A re-render walks the
View, diffs the resulting tree against the previous emission, and ships
targeted element/attribute patches plus a signal-payload delta over
SSE. The per-tab action mutex serialises writes — concurrent POSTs to
one tab cannot race.

Client. Datastar runs a fine-grained Alien Signals graph in the
browser. `Signal[T]` values are nodes in that graph; the `data-*`
attributes emitted by `s.Bind()`, `s.Text()`, `s.Show()`, `s.Attr()`,
`s.Style()` are subscriptions. Mutating a signal — from an `<input>`
edit, from `on.SetSignal`, or from a server-pushed patch — propagates
through derived bindings without a re-render and without a round-trip.

The split shows up at the field level. `Signal[int]` IS a client
signal. `StateTab[int]` IS server-only. The author decides in the
struct, not in the rendering code. UI state the client owns (modal
open, current tab, filter string, derived counts) reacts instantly with
zero SSE traffic; state the server owns (DB rows, cross-tab invariants,
secrets) flows through actions and re-renders.

## The four reactive shapes

| Handle              | Scope          | Lives on        |
|---------------------|----------------|-----------------|
| `via.Signal[T]`     | per-tab        | client + server |
| `via.StateTab[T]`   | per-tab        | server only     |
| `via.StateSess[T]`  | per-session    | server only     |
| `via.StateApp[T]`   | global         | server only     |

Reads go through `Read(ctx)`; writes through `Update(ctx, fn)`.
`Signal[T]` and `StateTab[T]` also expose `Write(ctx, v)` for direct
sets — per-tab writes are already serialized by the action mutex.
`Update` holds a per-key mutex across the load → fn → store sequence,
so concurrent writers from different ctxs cannot lose increments. Wire
keys, initial values, and the tag grammar are documented in godoc.

### Typed ops via `Op(ctx)`

For the common shape buckets — numeric, bool, string, slice, map — use
the `Num` / `Bool` / `Str` / `Slice` / `Map` typed wrappers and call
`Op(ctx)` for shape-aware verbs. Drop back to `Update(ctx, fn)` for
custom transforms or non-bucket `T` (structs, interfaces).

| Field type                          | Common verbs                                |
|-------------------------------------|---------------------------------------------|
| `via.StateTabNum[int]`              | `Add(n) / Sub(n) / Inc() / Dec() / Zero() / Min(lo) / Max(hi)` |
| `via.SignalBool`                    | `Toggle() / True() / False()`               |
| `via.StateSessStr`                  | `Append(s) / Prepend(s) / Clear()`          |
| `via.SignalSlice[T]`                | `Append(v) / Prepend(v) / Pop() / Shift() / Take(n) / Drop(n) / Filter(pred) / Empty()` |
| `via.StateAppMap[K,V]`              | `Put(k,v) / Delete(k) / Empty()`            |

### View helpers driven by `Signal[T]`

`Signal[T]` mirrors into the browser's reactive graph. The view helpers
compile to Datastar `data-*` attributes that subscribe to it — DOM
updates are fine-grained, no re-render, no round-trip:

```go
s.Bind()              // <input data-bind="key"> two-way binding
s.Text()              // <span data-text="$key"></span>
s.Show()              // data-show="$key" — toggle display by truthiness
s.Attr("disabled")    // data-attr-disabled="$key" — drives an HTML attr
s.Style("color")      // data-style-color="$key" — drives an inline CSS prop
```

`StateTab[T]` / `StateSess[T]` / `StateApp[T]` share `Text(ctx)`, which
re-renders server-side instead of subscribing to a client signal.

## Actions

A method on the composition with signature `func(*via.Ctx) error` — or
`func(*via.Ctx)` when nothing in the body can fail meaningfully — is
an action. Bind it to a DOM event with the `on` sub-package:

```go
h.Button(h.Text("+"), on.Click(c.Inc))
h.Form(on.Submit(c.Save), ...)
h.Input(on.Input(c.Filter, on.Debounce("200ms")))
h.Div(on.Key("Enter", c.Send))
h.Button(h.Text("Pick blue"),
    on.Click(c.Apply, on.SetSignal(&c.Theme, "blue")))
```

`on.SetSignal(&c.Field, value)` bundles a typed signal write with the
action so the value updates client-side before the POST fires.
`&c.Theme` is type-checked against the field — wrong type is a compile
error.

The action body can:

- Write typed state: `c.Hits.Write(ctx, …)` or `c.Hits.Op(ctx).Add(1)`.
- Push targeted patches: `ctx.Patch.Elements(h.Ul(h.ID("list"), …))`.
- Push raw signals: `ctx.Patch.Signal("_picoTheme", "purple")`.
- Show a quick toast: `ctx.Toast("saved!")` — a styled, non-blocking
  notice that auto-dismisses (JSON-safe, zero setup).
- Redirect: `ctx.Redirect("/profile")`.
- Decode the request payload into a typed struct:

  ```go
  var f LoginForm
  via.DecodeForm(ctx, &f)
  ```

Per-tab actions are serialized. Concurrent POSTs to one tab cannot
race on State writes.

For try-before-commit and bulk reconciliation flows, `ctx.SyncOff()`
opts the whole action out of the dirty-mark/flush cycle — see godoc.

## Lifecycle hooks

| Method                    | Fires when                                    |
|---------------------------|-----------------------------------------------|
| `OnInit(ctx) error`       | Before View on the page-load request          |
| `OnConnect(ctx) error`    | First time the SSE stream opens (one-shot)    |
| `OnDispose(ctx)`          | Tab closed, ctx swept, or app shut down       |
| `View(ctx) h.H`           | Required; renders the composition             |

Implement any subset; `Mount` detects whichever are defined.

`OnConnect` is where long-running per-tab work belongs — bots that hit
GET without ever opening the SSE never trigger it.

`via.Stream(ctx, interval, fn)` wires the most common ticker pattern:

```go
func (p *Page) OnConnect(ctx *via.Ctx) error {
    via.Stream(ctx, time.Second, func(ctx *via.Ctx, t time.Time) {
        p.Now.Write(ctx, t.Format("15:04:05"))
    })
    return nil
}
```

`Stream` returns a `*via.Ticker` with `Pause`, `Resume`, `Stop`, and
`SetInterval(d)` so actions can toggle the stream or change cadence at
runtime. See `internal/examples/sysmon` for a full pause / rate-change
UI driven by this surface.

Inside actions and `via.Stream` callbacks the flush is automatic. From
a raw goroutine you started yourself, call `ctx.SyncNow()` to force a
re-render and push pending writes. It serialises with in-flight action
handlers via the per-tab action mutex.

## File uploads

Add a `via.File` field. The action dispatcher detects multipart bodies
and binds the named part for the duration of the action:

```go
type Page struct {
    Avatar via.File           `via:"avatar"`
    Note   via.Signal[string] `via:"note"`
}

func (p *Page) Upload(ctx *via.Ctx) error {
    if !p.Avatar.Present() { return nil }
    return p.Avatar.Save("/var/uploads/" + sanitized)
}
```

The handle exposes `Filename()` (untrusted), `Size()`,
`ContentType()` (untrusted), `Open()` for streaming, `Bytes()` for
in-memory reads, and `Save(path)` for the common case (mode `0o600`,
truncate). Text fields in the same multipart POST populate `Signal[T]`
fields like a JSON action body.

For raw streaming control (mixed parts, custom headers, files larger
than the in-memory buffer), call `ctx.MultipartReader()`. Once read,
typed `via.File` fields on the same action will be empty for any parts
already advanced past.

`WithMaxRequestBody(n)` caps total body size; oversized requests return
413.

## Path parameters

```go
type Profile struct {
    UserID int    `path:"id"`
    Slug   string `path:"slug"`
}
via.Mount[Profile](app, "/u/{id}/posts/{slug}")
```

Each `path:"name"` tag must match a `{name}` segment. Reflection runs
once at Mount; per-request decoding writes directly into the typed
field.

## Sessions

```go
import "github.com/go-via/via/sess"

type User struct{ Email, Name string }

sess.Put(ctx, User{Email: "alice@example.com", Name: "Alice"})
u, ok := sess.Get[User](ctx)                 // handler/action
u, ok := sess.Get[User](r)                   // middleware
sess.Clear[User](ctx)
sess.Rotate(ctx)                             // after login / privilege change
```

`requireAuth` is one line of middleware:

```go
func requireAuth(w http.ResponseWriter, r *http.Request, next http.Handler) {
    if u, ok := sess.Get[User](r); !ok || u.Email == "" {
        http.Redirect(w, r, "/login", http.StatusSeeOther)
        return
    }
    next.ServeHTTP(w, r)
}
```

## Middleware

```go
import "github.com/go-via/via/mw"

app := via.New()
mw.Defaults(app)                // RequestID + AccessLog + Recover
app.Use(mw.CSP())               // strict CSP with per-request nonce
app.Use(requireAuth)            // your own
```

Factories under `via/mw`:

- `mw.Defaults(app)` — RequestID + AccessLog + Recover.
- `mw.RequestID()` — stamp `X-Request-ID` + plant on `r.Context`.
- `mw.AccessLog(app)` — one info-line per request, with rid + status;
  CR/LF stripped from method/path/rid so user input can't forge log
  entries (CWE-117).
- `mw.Recover(app)` — panic → 500 + error log (same CR/LF scrub); the
  goroutine survives.
- `mw.CSP(extra…)` — strict CSP header + nonce on `r.Context`.
- `mw.HSTS(opts…)` — Strict-Transport-Security for HTTPS deploys.
- `mw.RedirectHTTPS()` — 301 plain HTTP → https; trusts
  `X-Forwarded-Proto` (use behind a TLS-terminating proxy).
- `mw.RedirectHTTPSStrict()` — same redirect but ignores XFP; only
  `r.TLS != nil` counts as secure (use for direct-bind TLS).

Read it back inside actions / handlers:

```go
via.RequestIDFrom(r)             // string or ""
via.Log(ctx).Log(via.LogInfo, "checkout", "amount", n)
ctx.CSPNonce()                   // matches header set by mw.CSP
```

## Routing and groups

```go
via.Mount[Counter](app, "/counter/{id}")

api := app.Group("/api")
api.Use(requireAuth)
via.Mount[Profile](api, "/profile")

api.HandleFunc("POST /widgets", createWidget) // method-prefixed
api.HandleFunc("/widgets",       listWidgets) // bare path = GET

app.Routes()                                  // []RouteInfo for boot logging
```

Group patterns follow `http.ServeMux` shape: `"GET /foo"`, `"POST /foo"`,
or just `"/foo"` (defaults to GET). Mounting two routes at the same
path panics at registration with the offending pattern and the
original registrar tag. `WithNotFound(h)` installs a custom 404
handler.

## Plugins

```go
app := via.New(via.WithPlugins(
    picocss.Plugin(picocss.WithThemes(picocss.AllPicoThemes)),
    echarts.Plugin(),
))
```

Plugins implement `Register(*via.App)` and call any of `AppendToHead`,
`AppendToFoot`, `AppendAttrToHTML`, `HandleFunc`, or
`RegisterAppSignal` during boot to inject document fragments, asset
routes, and client-driven signals. Call these only from `Register` —
the document-mutation slices are not lock-guarded against concurrent
appends after the server starts.

Plugin packages expose `Plugin(...)` as the canonical constructor
(never `New(...)`) so `via.WithPlugins(...)` call sites stay uniform.

## Testing

Tests drive the composition through HTTP — same path as a real
browser, so the full middleware stack, session cookie, and SSE
machinery run end-to-end. There is no "direct method" seam: assertions
hit rendered HTML or SSE frames, never internal state.

```go
import (
    "github.com/go-via/via"
    "github.com/go-via/via/vt"
)

var server *httptest.Server
app := via.New(via.WithTestServer(&server))
via.Mount[Counter](app, "/")

tc := vt.NewClient(t, server, "/")
c := &Counter{}
require.Equal(t, 200, tc.Action(c.Inc).Fire())   // typed: typo → compile error
require.Equal(t, 200, tc.Action("Apply").        // string still works
    WithSignal("step", 5).Fire())
require.Contains(t, tc.Reload(), ">1<")

frames, cancel := tc.SSE()
defer cancel()
vt.AwaitFrame(t, frames, 2*time.Second, ">3<")

tc.Action(p.Upload).
    WithFile("avatar", "me.png", pngBytes).
    WithSignal("note", "from CLI").
    Fire()
```

`tc.Action` accepts a method value (compile-time typo protection) or
the action's name as a string. `tc.Reload` re-fetches the mounted page
so post-action body assertions are one call. `tc.Fork(path)` opens a
second tab on the same cookie jar — the only way to drive `StateSess`
behaviour that spans tabs.

## Performance

Bench files: [`bench_test.go`](bench_test.go) (full request → SSE
turn) and [`h/h_bench_test.go`](h/h_bench_test.go) (DSL only). Run
`go test -bench=. -benchmem` against your target hardware — quoting
numbers from someone else's laptop is rarely useful. `ci-check.sh`
gates the steady-state allocation floors on `CounterRender`,
`CounterAction`, and `CounterActionWithLogger` so regressions fail CI.

`h.Static(...)` pre-renders fragments that don't depend on per-request
state — see `BenchmarkSysmonShape_staticChrome_render` for the
per-tick allocation delta against rebuilding the same chrome on every
tick.

## h package helpers

`h` is the HTML DSL — elements, attributes, text, iteration,
conditionals, static pre-render, custom tags. The full reference lives
in [`docs/h-helpers.md`](docs/h-helpers.md) and in
[`go doc github.com/go-via/via/h`](https://pkg.go.dev/github.com/go-via/via/h).

```go
h.Div(h.Class("card"),
    h.H1(h.T("Title")),
    h.Each(items, func(it Item) h.H { return h.Li(h.T(it.Name)) }),
)
```

`h.Static(n)` pre-renders fragments that don't depend on per-request
state (layout chrome, headers); every later Render writes the captured
bytes verbatim. `h.NewTag("svg")` declares constructors for tags
outside the built-in list (web components, SVG, MathML).
`h.With(base, more...)` extends an existing element non-destructively.

## Cross-tab broadcast

```go
app.Broadcast(`alert("Maintenance in 30 seconds.")`)
app.BroadcastSignals(map[string]any{"_systemNotice": "site read-only"})
app.LiveTabs()
```

`Broadcast` queues a JS snippet on every live tab; `BroadcastSignals`
queues a signal patch. Both return the tab count they reached and
deliver via the existing patch queue + SSE drain — no extra wiring.

## Production wiring

```go
app := via.New(
    via.WithLang("en"),
    via.WithLogger(via.SlogLogger(slog.Default())),
    via.WithMaxRequestBody(1<<20),
    via.WithMaxContexts(10000),
    via.WithSSEHeartbeat(25*time.Second),
)
mw.Defaults(app)
app.Use(mw.HSTS())
app.Use(mw.CSP())
app.Use(mw.RedirectHTTPS())

via.Mount[Home](app, "/")
api := app.Group("/api")
api.Use(requireAuth)
via.Mount[Profile](api, "/profile")

http.ListenAndServe(":8080", app)
```

### Operations: metrics

`via.WithMetrics(m)` accepts an implementation of the `Metrics`
interface and emits structured events for ops dashboards:

| Event                  | Kind      | Labels             |
|------------------------|-----------|--------------------|
| `via.action.total`     | counter   | `method`           |
| `via.action.latency`   | histogram | `method`           |
| `via.render.total`     | counter   | `route`            |
| `via.sse.connect`      | counter   |                    |
| `via.sse.disconnect`   | counter   | `reason`           |
| `via.ctx.live`         | gauge     |                    |

Adapt to Prometheus, OTel, or expvar by implementing three methods
(`Counter`, `Gauge`, `Histogram`) that forward to your backend.

## Security defaults

- CSRF: every page mints a 256-bit `via_tab` id; action POSTs and SSE
  handshakes carry it as a signal. The id IS the CSRF token — unknown
  ids 404. Action POSTs are also session-pinned (cookie mismatch → 403).
- Sessions: `via_session` cookie is `HttpOnly`, `SameSite=Lax`, 256-bit,
  and `Secure` by default; `WithInsecureCookies()` drops `Secure` for a
  local http:// dev loop. After auth state changes, call
  `sess.Rotate(ctx)` (session-fixation defence).
- CSP: `mw.CSP()` emits a strict header with a per-request nonce
  reachable via `ctx.CSPNonce()`.
- Body limits: `WithMaxRequestBody(n)` (default 1 MiB) caps action
  POST and SSE-close bodies; oversized requests return 413.
- Panic sanitization: action panics surface as `"Something went wrong"`
  to the client. User-returned errors flow through unmodified.
- Random sources: `crypto/rand.Read` failures panic rather than fall
  back to zero-byte ids.

## Examples

`internal/examples/` ships:

- `counter` — `StateTab[int]` + `Signal[int]` + a typed action.
- `greeter` — `Signal[string]` mutated from two distinct actions.
- `pathparams` — typed `path:"id"` decoding into composition fields.
- `countercomp` — two independent counter compositions nested on one
  page; isolation across instances.
- `counterscope` — `StateTab[int]` (tab-local) vs `StateApp[int]`
  (shared across every session) side-by-side.
- `picocss` — `picocss.Plugin()` driving theme + dark-mode switching
  on the client without a full reload.
- `auth` — typed sessions, `requireAuth` middleware, and `sess.Rotate`
  after login.
- `todos` — `StateSess[T]` survives reload, `h.Each`, and
  `on.SetSignal` for client-bundled writes.
- `sysmon` — OnConnect-driven ticker streaming CPU / RAM / disk / net
  into ECharts; drives an interactive pause + interval-slider UI via
  `via.Ticker.Pause / SetInterval`.
- `upload` — `via.File` field bound to a `multipart/form-data` `<form>`
  POST, persisted to disk, redirect-back-to-/.
- `feed` — append-only / bounded-ring slice stream driven by
  `Signal[[]T].Update`, paused/cleared from actions.

```bash
go run ./internal/examples/counter
```

## Configuration

Every `WithX(...)` option is documented in
[`go doc github.com/go-via/via`](https://pkg.go.dev/github.com/go-via/via)
with its default and behaviour. Common production knobs:

- `WithMaxContexts(n)`, `WithLogger(SlogLogger(...))`,
  `WithInsecureCookies()` (dev opt-out — `Secure` is on by default)
- `WithMaxRequestBody(n)`, `WithSessionTTL(d)`, `WithContextTTL(d)`
- `WithSSEHeartbeat(d)`, `WithReadHeaderTimeout(d)`,
  `WithIdleTimeout(d)`
- `WithActionErrorHandler(fn)`, `WithNotFound(h)`,
  `WithHTTPServer(hook)`

## License

MIT
