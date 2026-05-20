# Via

[![Go Reference](https://pkg.go.dev/badge/github.com/go-via/via.svg)](https://pkg.go.dev/github.com/go-via/via)
[![Go Report Card](https://goreportcard.com/badge/github.com/go-via/via)](https://goreportcard.com/report/github.com/go-via/via)
[![CI](https://github.com/go-via/via/actions/workflows/ci.yml/badge.svg)](https://github.com/go-via/via/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Real-time engine for building reactive web apps in pure Go. A composition
is a struct. Reactive state is a typed field. Actions are methods. The
compiler understands your UI.

The runtime is split where it matters: **the server owns truth, the
client owns view reactivity.** Server-side state (`StateTab`, `StateSess`,
`StateApp`) lives only in Go; `Signal[T]` values are mirrored into a
fine-grained reactive graph in the browser (Alien Signals, via
Datastar), so DOM updates driven by a client-set signal never
round-trip. The two halves talk over one SSE stream per tab with typed
JSON payloads.

- No templates. No hand-written JavaScript. No transpilation. No
  hydration. No bundler.
- Single SSE stream per tab; reconnect, heartbeat, and tab-death cleanup
  are the framework's job.
- `*App` implements `http.Handler` — drops into any std mux.

![Local vs app-scoped counters across two browsers — from `internal/examples/counterscope`](docs/counter-scope.gif)

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
    Hits via.StateTab[int]
    Step via.Signal[int] `via:"step,init=1"`
}

func (c *Counter) Inc(ctx *via.Ctx) {
    c.Hits.Update(ctx, func(n int) int { return n + c.Step.Get(ctx) })
}

func (c *Counter) View(ctx *via.Ctx) h.H {
    return h.Div(
        h.P(h.Text("Count: "), c.Hits.Text()),
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

## Why Via, not X

|                       | Language | Authoring                | Client runtime              | Build step             | Reactive state                |
|-----------------------|----------|--------------------------|-----------------------------|------------------------|-------------------------------|
| **Via**               | Go       | typed structs + `h` DSL  | Datastar (Alien Signals)    | none                   | typed fields, client + server |
| HTMX                  | any      | HTML + `hx-*` attributes | tiny attribute interpreter  | none                   | server-only, manual           |
| Phoenix LiveView      | Elixir   | EEx templates + macros   | morphdom + tiny JS          | none                   | `assigns` (Elixir-typed)      |
| templ                 | Go       | `.templ` template files  | none (BYO)                  | yes (`templ generate`) | none built-in                 |
| Datastar (direct)     | any      | HTML + `data-*` attrs    | Datastar (Alien Signals)    | none                   | client signals, manual        |

Via is the only row that gives you typed end-to-end state (server + client),
no build step, and a fine-grained reactive client runtime in the same
package. Pick another row if you want a different language, a template
file format, or a different state-ownership split.

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

**Server.** Go owns truth. `StateTab[T]`, `StateSess[T]`, and
`StateApp[T]` live only on the server; `Signal[T]` is mirrored. A
re-render walks the View, diffs the resulting tree against the previous
emission, and ships targeted element/attribute patches plus a
signal-payload delta over SSE. The per-tab action mutex serialises
writes — concurrent POSTs to one tab can't race.

**Client.** Datastar runs a fine-grained Alien Signals graph in the
browser. `Signal[T]` values are nodes in that graph; the `data-*`
attributes emitted by `s.Bind()`, `s.Text()`, `s.Show()`, `s.Attr()`,
`s.Style()` are subscriptions. Mutating a signal — from an `<input>`
edit, from `on.SetSignal`, or from a server-pushed patch — propagates
through derived bindings without a re-render and without a round-trip.

The split shows up at the field level: a `Signal[int]` IS a client
signal, a `StateTab[int]` IS server-only. The author decides in the
struct, not in the rendering code. UI state the client owns (modal
open, current tab, filter string, derived counts) reacts instantly with
zero SSE traffic; state the server owns (DB rows, cross-tab
invariants, secrets) flows through actions and re-renders.

## Performance

Bench files: [`bench_test.go`](bench_test.go) (full request → SSE turn)
and [`h/h_bench_test.go`](h/h_bench_test.go) (DSL only). Run with
`go test -bench=. -benchmem` against your target hardware — quoting
numbers from someone else's laptop is rarely useful. `ci-check.sh`
gates the steady-state allocation floors on `CounterRender`,
`CounterAction`, and `CounterActionWithLogger` so regressions fail CI.

`h.Static(...)` pre-renders fragments that don't depend on per-request
state — see `BenchmarkSysmonShape_staticChrome_render` for the
per-tick allocation delta against rebuilding the same chrome on every
tick.

## Reactive state

| Handle              | Scope          | Lives on        |
|---------------------|----------------|-----------------|
| `via.Signal[T]`     | per-tab        | client + server |
| `via.StateTab[T]`      | per-tab        | server only     |
| `via.StateSess[T]`  | per-session    | server only     |
| `via.StateApp[T]`   | global         | server only     |

Reads go through `Get(ctx)` and writes through `Update(ctx, fn)` —
explicit context, no hidden globals. `Signal[T]` and `StateTab[T]` also
expose `Set(ctx, v)` since per-tab writes are already serialized by the
action mutex; `StateSess[T]` and `StateApp[T]` deliberately don't, since
a blind `Set` on shared state is almost always a read-modify-write race
in disguise — `Update` holds a per-key mutex across the load → fn →
store sequence so concurrent writers from different ctxs cannot lose
increments. Wire keys default to lower-cased field names; override with
the `via:"name"` tag, seed an initial value with `via:"name,init=…"`.
Skip the name segment to keep the default key but still seed:
`via:",init=3"` on a `StateTab[int]` field seeds it to 3 without renaming.

All four reactive shapes (`Signal[T]`, `StateTab[T]`, `StateSess[T]`,
`StateApp[T]`) speak the same `Update(ctx, fn)` surface, so common
read-modify-write patterns are one call regardless of scope. `Update`
holds the per-key mutex on shared-state handles, so the load → fn →
store sequence is atomic:

```go
p.Count.Update(ctx, func(n int) int { return n + 1 })  // numeric delta
p.Open.Update(ctx, func(b bool) bool { return !b })    // bool flip

if p.Status.Get(ctx) != "busy" {                       // skip patch if unchanged
    p.Status.Update(ctx, func(string) string { return "busy" })
}

p.Series.Update(ctx, func(s []Point) []Point {         // append-only feed
    return append(s, point)
})
p.Series.Update(ctx, func(s []Point) []Point {         // cap to last 100
    s = append(s, point)
    if len(s) > 100 {
        copy(s, s[len(s)-100:])
        s = s[:100]
    }
    return s
})
```

`Signal[T]` is mirrored into the browser's reactive graph. The view
helpers below compile to Datastar `data-*` attributes that subscribe to
that graph — when the signal changes (a client edit, an `on.SetSignal`,
or a server-pushed patch), the DOM updates fine-grained without a
re-render and without a round-trip:

```go
s.Bind()              // <input data-bind="key"> two-way binding
s.Text()              // <span data-text="$key"></span> reactive text node
s.Show()              // data-show="$key" — toggle display by truthiness
s.Attr("disabled")    // data-attr-disabled="$key" — drives an HTML attr
s.Style("color")      // data-style-color="$key" — drives an inline CSS prop
```

`StateTab[T]` and `StateSess[T] / StateApp[T]` only have `Text()` —
they're server-side, so the view re-renders the value rather than the
client reading a signal.

## Lifecycle hooks

| Method                    | Fires when                                    |
|---------------------------|-----------------------------------------------|
| `OnInit(ctx) error`       | Before View on the page-load request          |
| `OnConnect(ctx) error`    | First time the SSE stream opens (one-shot)    |
| `OnDispose(ctx)`          | Tab closed, ctx swept, or app shut down       |
| `View(ctx) h.H`           | Required; renders the composition             |

Embed `via.Page` to make the optional hooks discoverable in your
editor — type `p.On...` and the completion list shows them:

```go
type Profile struct {
    via.Page
    UserID int `path:"id"`
}

func (p *Profile) OnInit(ctx *via.Ctx) error { /* ... */ return nil }
func (p *Profile) View(ctx *via.Ctx) h.H     { /* ... */ return h.Div() }
```

Embedding is optional — compositions that don't embed it work
exactly the same way (Mount detects whichever hooks are defined and
skips the rest). `View` is always required.

`OnConnect` is where long-running per-tab work belongs — bots that hit
GET without ever opening the SSE never trigger it.

`via.Stream(ctx, time.Second, fn)` wires the most common ticker pattern:

```go
func (p *Page) OnConnect(ctx *via.Ctx) error {
    via.Stream(ctx, time.Second, func(ctx *via.Ctx, t time.Time) {
        p.Now.Set(ctx, t.Format("15:04:05"))
    })
    return nil
}
```

`Stream` returns a `*via.Ticker` with `Pause`, `Resume`, `Paused`, and
`SetInterval(d)` so actions can toggle the stream on/off or change its
cadence at runtime:

```go
ticker := via.Stream(ctx, 200*time.Millisecond, p.poll)
ticker.Pause()
ticker.SetInterval(time.Second)
ticker.Resume()
```

See `internal/examples/sysmon` for a full pause/rate-change UI driven
by this surface.

Inside actions and `via.Stream` callbacks the flush is automatic. From a
raw goroutine you started yourself, call `ctx.Flush()` (no-op when nothing
is dirty) to push pending Set values to the browser, or `ctx.Sync()` to
force a re-render even if no signal/state changed. Both serialise with
in-flight action handlers via the per-tab action mutex.

## Actions

A method on `*Composition` of signature `func(*via.Ctx) error` — or
`func(*via.Ctx)` when nothing in the body can fail meaningfully — is
an action. Bind it to a DOM event with the `on` sub-package:

```go
h.Button(h.Text("+"), on.Click(c.Inc))
h.Form(on.Submit(c.Save), ...)
h.Input(on.Input(c.Filter, on.Debounce("200ms")))
h.Div(on.Key("Enter", c.Send))
h.Button(h.Text("Pick blue"), on.Click(c.Apply, on.SetSignal(&c.Theme, "blue")))
```

`on.SetSignal(&c.Field, value)` bundles a typed signal write with the
action so the value updates client-side _before_ the POST fires.

The action method's body can:

- Set typed state: `c.Hits.Set(ctx, …)` or `c.Hits.Update(ctx, func(n int) int { return n + 1 })`.
- Push targeted patches: `ctx.SyncElements(h.Ul(h.ID("list"), …))`.
- Push raw signals: `ctx.PatchSignal("_picoTheme", "purple")`.
- Show a quick alert: `ctx.Toast("saved!")` (JSON-safe), or
  `return via.Toast("saved!")` — the dispatcher recognises the
  returned `*via.ToastError` and queues the alert without invoking the
  action-error handler.
- Redirect: `ctx.Redirect("/profile")` or `return via.Redirect("/profile")`
  — the dispatcher recognises the returned `*via.RedirectError` and
  navigates the tab without invoking the action-error handler. Useful
  when a helper deep in the call chain decides to redirect without
  having `*Ctx` in scope.
- Decode the request payload into a typed struct:

  ```go
  var f LoginForm
  via.DecodeForm(ctx, &f)
  ```

Per-tab actions are serialized — concurrent POSTs to one tab will not
race on State writes.

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

The handle exposes `Filename()` (untrusted), `Size()`, `ContentType()`
(untrusted), `Open()` for streaming, `Bytes()` for in-memory reads,
and `Save(path)` for the common "stash to disk" case (mode `0o600`,
truncate). Text fields in the same multipart POST populate `Signal[T]`
fields exactly like a JSON action body.

For raw streaming control over a multipart body (mixed parts, custom
headers, files larger than the in-memory buffer), call
`ctx.MultipartReader()` for the std-library reader. Once read, typed
`via.File` fields on the same action will be empty for any parts you
advanced past.

`WithMaxRequestBody(n)` caps the total body size; oversized requests
return 413.

A plain HTML `<form enctype=multipart/form-data>` posts to
`/_action/Method` and the response body shows in the browser, so most
upload flows finish with `http.Redirect(ctx.Writer(), ctx.Request(),
"/", 303)` to refresh the page. Any state you want visible after the
redirect must live in `via.StateSess[T]` (session-scoped) — `via.StateTab[T]`
is per-tab and the redirected GET allocates a fresh tab.

## Path parameters

```go
type Profile struct {
    UserID int    `path:"id"`
    Slug   string `path:"slug"`
}
via.Mount[Profile](app, "/u/{id}/posts/{slug}")
```

Each `path:"name"` tag must match a `{name}` segment. Reflection runs
once at Mount; per-request decoding writes directly into the typed field.

## Sessions

```go
type User struct{ Email, Name string }

via.PutSess(ctx, User{Email: "alice@example.com", Name: "Alice"})
u, ok := via.GetSess[User](ctx)              // inside a handler/action
u, ok := via.GetSess[User](r)                // inside a Middleware
via.ClearSess[User](ctx)
via.RotateSession(ctx)                       // after login/privilege change
```

`requireAuth` is a one-line middleware:

```go
func requireAuth(w http.ResponseWriter, r *http.Request, next http.Handler) {
    if u, ok := via.GetSess[User](r); !ok || u.Email == "" {
        http.Redirect(w, r, "/login", http.StatusSeeOther)
        return
    }
    next.ServeHTTP(w, r)
}
```

## Middleware

```go
app := via.New()
via.Defaults(app)               // RequestID + AccessLog + Recover
app.Use(via.StrictCSP())        // strict CSP with per-request nonce
app.Use(requireAuth)            // your own
```

Built-in factories under `via`:

- `Defaults(app)` — install RequestID + AccessLog + Recover.
- `RequestID()` — stamp `X-Request-ID` + plant on `r.Context`.
- `AccessLog(app)` — one info-line per request, with rid + status.
- `Recover(app)` — panic → 500 + error log; the goroutine survives.
- `StrictCSP(extra…)` — strict CSP header + nonce on `r.Context`.
- `HSTS(opts…)` — Strict-Transport-Security for HTTPS deploys.
- `RedirectHTTPS()` — 301 plain HTTP → https; respects XFP header.

Read it back inside actions / handlers:

```go
via.RequestIDFrom(r)             // string or ""
via.Log(ctx).Log(via.LogInfo, "checkout", "amount", n)
ctx.CSPNonce()                   // matches header set by StrictCSP
```

## Routing & groups

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
… or just `"/foo"` (defaults to GET). Mounting two routes at the same
path panics at registration with the offending pattern + the original
registrar tag. `WithNotFound(h)` installs a custom 404 handler.

### Production wiring

```go
app := via.New(
    via.WithLang("en"),
    via.WithSecureCookies(),
    via.WithLogger(via.SlogLogger(slog.Default())),
    via.WithMaxRequestBody(1<<20),
    via.WithMaxContexts(10000),
    via.WithSSEHeartbeat(25*time.Second),
)
via.Defaults(app)
app.Use(via.HSTS())
app.Use(via.StrictCSP())
app.Use(via.RedirectHTTPS())

via.Mount[Home](app, "/")
api := app.Group("/api")
api.Use(requireAuth)
via.Mount[Profile](api, "/profile")

http.ListenAndServe(":8080", app)
```

### Restart and tab survivability

A live tab's state lives in memory on the server (the `*via.Ctx` and its
`session`). It does **not** survive a process restart:

- After a deploy, every client's `via_tab` is unknown to the new
  process. The next SSE reconnect 404s and the next action POST 404s.
- The client (Datastar) retries the SSE connection forever, so a user
  watching a stale tab sees it freeze rather than recover. Tell users
  to reload, or pair the deploy with a sticky load balancer that
  drains long enough for tabs to close naturally.
- Sessions are also in-memory; logged-in users will need to re-auth
  unless you back the session store with something durable (not built
  in; users with auth flows generally roll their own
  `via.PutSess` keyed off a real session store).

If you need session survivability across restarts, persist the
`via.PutSess`-stored payload (e.g. a JWT or an opaque token your auth
layer recognizes) to a database keyed by the `via_session` cookie value,
and rehydrate inside an `OnInit` hook on the relevant compositions.

### Operations: metrics

`via.WithMetrics(m)` accepts an implementation of the [`Metrics`] interface
and emits structured events for ops dashboards:

| Event                  | Kind      | Labels             |
|------------------------|-----------|--------------------|
| `via.action.total`     | counter   | `method`           |
| `via.action.latency`   | histogram | `method`           |
| `via.render.total`     | counter   | `route`            |
| `via.sse.connect`      | counter   |                    |
| `via.sse.disconnect`   | counter   |                    |
| `via.ctx.live`         | gauge     |                    |

Adapt to Prometheus, OTel, or expvar by implementing three methods
(`Counter`, `Gauge`, `Histogram`) that forward to your backend.

## Cross-tab broadcast

```go
app.Broadcast(`alert("Maintenance in 30 seconds.")`)
app.BroadcastSignals(map[string]any{"_systemNotice": "site read-only"})
app.LiveTabs()               // current tab count
```

`Broadcast` queues a JS snippet on every live tab; `BroadcastSignals`
queues a signal patch. Both return the tab count they reached and
deliver via the existing patch queue + SSE drain — no extra wiring.

## h package helpers

`h` is the HTML DSL — elements, attributes, text, iteration,
conditionals, static pre-render, custom tags. The full reference (every
constructor with its contract) lives in
[`docs/h-helpers.md`](docs/h-helpers.md) and in
[`go doc github.com/go-via/via/h`](https://pkg.go.dev/github.com/go-via/via/h).

The shapes you reach for daily:

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

## Plugins

```go
app := via.New(via.WithPlugins(
    picocss.Plugin(picocss.WithThemes(picocss.AllPicoThemes)),
    echarts.Plugin(),
))
```

Plugins implement `Register(*via.App)` and call any of `AppendToHead`,
`AppendToFoot`, `AppendAttrToHTML`, `HandleFunc`, or `RegisterAppSignal`
during boot to inject document fragments, asset routes, and
client-driven signals. Call these only from `Register` — the document-
mutation slices aren't lock-guarded against concurrent appends after
the server starts.

## Testing

Tests drive the composition through HTTP — same path as a real
browser, so the full middleware stack, session cookie, and SSE
machinery run end-to-end. There is no "direct method" seam: assertions
hit rendered HTML or SSE frames, never internal state (see
[CONVENTIONS.md](CONVENTIONS.md) — *Test Scope: Outside-In Through
the Public API*).

```go
import (
    "github.com/go-via/via"
    "github.com/go-via/via/test"
)

var server *httptest.Server
app := via.New(via.WithTestServer(&server))
via.Mount[Counter](app, "/")

tc := test.NewClient(t, server, "/")
c := &Counter{}
require.Equal(t, 200, tc.Action(c.Inc).Fire())             // typed: typo → compile error
require.Equal(t, 200, tc.Action("Apply").                  // string still works
    WithSignal("step", 5).Fire())
require.Contains(t, tc.Reload(), ">1<")                    // re-fetch + assert on body

frames, cancel := tc.SSE()
defer cancel()
test.AwaitFrame(t, frames, 2*time.Second, ">3<")           // wait for substring

// Multipart action with a file part — switches the request to
// multipart/form-data automatically when any file is attached.
tc.Action(p.Upload).
    WithFile("avatar", "me.png", pngBytes).
    WithSignal("note", "from CLI").
    Fire()
```

`tc.Action` accepts either a method value (compile-time typo protection)
or the action's name as a string. `tc.Reload` re-fetches the mounted
page so post-action body assertions are one call instead of a
hand-rolled GET. `tc.Fork(path)` opens a second tab on the same cookie
jar — the only way to drive `StateSess` behaviour that spans tabs.

## Examples

`internal/examples/` ships:

- `counter` — `StateTab[int]` + `Signal[int]` + a typed action.
- `greeter` — `Signal[string]` mutated from two distinct actions.
- `pathparams` — typed `path:"id"` decoding into composition fields.
- `countercomp` — two independent counter compositions nested on
  one page; isolation across instances.
- `counterscope` — `StateTab[int]` (tab-local) vs `StateApp[int]`
  (shared across every session) side-by-side.
- `picocss` — `picocss.Plugin()` driving theme + dark-mode switching
  on the client without a full reload.
- `auth` — typed sessions, `requireAuth` middleware, and
  `via.RotateSession` after login.
- `todos` — `StateSess[T]` survives reload, `h.Each`, and
  `on.SetSignal` for client-bundled writes.
- `sysmon` — OnConnect-driven ticker streaming CPU/RAM/disk/net into
  ECharts; also drives an interactive pause + interval-slider UI via
  `via.Ticker.Pause / SetInterval`.
- `upload` — `via.File` field bound to a `multipart/form-data`
  `<form>` POST, persisted to disk, redirect-back-to-/.
- `feed` — append-only / bounded-ring slice stream driven by
  `Signal[[]T].Update`, paused/cleared from actions.

```bash
go run ./internal/examples/counter
```

## Configuration

Every `WithX(...)` option is documented in
[`go doc github.com/go-via/via`](https://pkg.go.dev/github.com/go-via/via)
with its default and behaviour. The common production knobs:

- `WithSecureCookies()`, `WithMaxContexts(n)`, `WithLogger(SlogLogger(...))`
- `WithMaxRequestBody(n)`, `WithSessionTTL(d)`, `WithContextTTL(d)`
- `WithSSEHeartbeat(d)`, `WithReadHeaderTimeout(d)`, `WithIdleTimeout(d)`
- `WithActionErrorHandler(fn)`, `WithNotFound(h)`, `WithHTTPServer(hook)`

## Security

What via defends against by default:

- **CSRF**: every page mints a 256-bit `via_tab` id; action POSTs and SSE
  handshakes carry it as a signal. The id IS the CSRF token — unknown
  ids 404. Action POSTs are also session-pinned (cookie mismatch → 403).
- **Sessions**: `via_session` cookie is `HttpOnly`, `SameSite=Lax`, 256-bit.
  `WithSecureCookies()` flips on `Secure` for HTTPS. After auth state
  changes, call `RotateSession(ctx)` (session-fixation defence).
- **CSP**: `StrictCSP()` middleware emits a strict header with a
  per-request nonce reachable via `ctx.CSPNonce()`.
- **Body limits**: `WithMaxRequestBody(n)` (default 1 MiB) caps action
  POST and SSE-close bodies; oversized requests return 413.
- **Panic sanitization**: action panics surface as `"Something went
  wrong"` to the client. User-returned errors flow through unmodified.
- **Random sources**: `crypto/rand.Read` failures panic rather than
  fall back to zero-byte ids.

### Recommended production stack

```go
app := via.New(
    via.WithSecureCookies(),
    via.WithMaxContexts(10000),
    via.WithLogger(via.SlogLogger(slog.Default())),
)
via.Defaults(app)              // RequestID + AccessLog + Recover
app.Use(via.HSTS())
app.Use(via.StrictCSP())
app.Use(via.RedirectHTTPS())
```

## License

MIT
