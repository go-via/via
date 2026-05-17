# Via

Real-time engine for building reactive web apps in pure Go. A composition
is a struct. Reactive state is a typed field. Actions are methods. The
compiler understands your UI.

- No templates. No hand-written JavaScript. No transpilation. No hydration.
- Single SSE stream per tab.
- `*App` implements `http.Handler` — drops into any std mux.

## Quick start

```go
package main

import (
    "net/http"

    "github.com/go-via/via"
    "github.com/go-via/via/h"
    "github.com/go-via/via/on"
)

type Counter struct {
    Hits via.State[int]
    Step via.Signal[int] `via:"step,init=1"`
}

func (c *Counter) Inc(ctx *via.Ctx) {
    via.Add(ctx, &c.Hits, c.Step.Get(ctx))
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

## Breaking changes since v0.2.x

- `h.H` is now `interface { Render(w io.Writer) error }`. It is no
  longer a type alias for `maragu.dev/gomponents.Node`. Mixed-imports
  (e.g. passing `gomponents.El(...)` into `h.Div(...)`) no longer
  compile.
- `maragu.dev/gomponents` is no longer a dependency.
- New surface added to `h`: `T`, variadic `Class`, `Style`, `Styles`,
  `Maybe`, `With`, `Static`, `Tag`, `VoidTag`, `NewTag`, `NewVoidTag`,
  `Checked`, `Required`, `Disabled`, `Role`, `Min`, `Max`, `Step`,
  `For`, `Lang`, `Content`, `Charset`.
- `h.Classes` is deprecated in favour of variadic `h.Class(parts...)`.
  Nothing is removed; the call sites can migrate at leisure.

## Reactive state

| Handle              | Scope          | Lives on        |
|---------------------|----------------|-----------------|
| `via.Signal[T]`     | per-tab        | client + server |
| `via.State[T]`      | per-tab        | server only     |
| `scope.User[T]`     | per-session    | server only     |
| `scope.App[T]`      | global         | server only     |

Reads and writes go through `Get(ctx) / Set(ctx, v)` — explicit context,
no hidden globals. Wire keys default to lower-cased field names; override
with the `via:"name"` tag, seed an initial value with `via:"name,init=…"`.
Skip the name segment to keep the default key but still seed:
`via:",init=3"` on a `State[int]` field seeds it to 3 without renaming.

For common read-modify-write patterns there are typed helpers that work
on any handle satisfying `via.Mutable[T]` — that's all four reactive
shapes: `Signal[T]`, `State[T]`, `scope.User[T]`, `scope.App[T]`:

```go
s.Update(ctx, func(n int) int { return n + 1 })  // generic transform
via.Add(ctx, &p.Count, 1)                         // numeric delta
via.Toggle(ctx, &p.Open)                          // bool flip
via.SetIfChanged(ctx, &p.Status, "busy")          // skip patch if unchanged
via.SetIfChanged(ctx, &p.Theme, "dark")           // works on scope.User too
via.Push(ctx, &p.Series, point)                   // append-only feed (Signal[[]T])
via.PushBounded(ctx, &p.Series, point, 100)       // cap to last 100
```

`Signal[T]` (client-mirrored) also exposes view helpers for composing
with `h`:

```go
s.Bind()              // <input data-bind="key"> two-way binding
s.Text()              // <span data-text="$key"></span> reactive text node
s.Show()              // data-show="$key" — toggle display by truthiness
s.Attr("disabled")    // data-attr-disabled="$key" — drives an HTML attr
s.Style("color")      // data-style-color="$key" — drives an inline CSS prop
```

`State[T]` and `scope.User[T] / scope.App[T]` only have `Text()` —
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

- Set typed state: `c.Hits.Set(ctx, …)` or `via.Add(ctx, &c.Hits, 1)`.
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
redirect must live in `scope.User[T]` (session-scoped) — `via.State[T]`
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

Grouped by responsibility — `go doc github.com/go-via/via/h` has each
symbol's contract.

Iteration:

- `h.Each(items, fn)` — one node per slice element, nil-pruned.
- `h.EachIndexed(items, fn)` — same with `(i, v)` passed to fn.
- `h.EachSeq(seq, fn)` — `iter.Seq` variant (`slices.Values`,
  `maps.Values`, …).
- `h.EachSeq2(seq, fn)` — `iter.Seq2` variant (`slices.All`,
  `maps.All`, …).

Conditional:

- `h.If(cond, n)`, `h.IfElse(cond, then, els)` — eager.
- `h.When(cond, build)`, `h.WhenElse(cond, then, els)` — lazy; only
  the winning branch is constructed.
- `h.Maybe(v, fn)` — render `fn(v)` only when v ≠ zero(T) (T must be
  `comparable`).
- `h.Switch(value, h.Case(...), h.Default(...))` — tab-style equality.
- `h.IfStr(cond, s)` — `s` if cond, `""` otherwise; pairs with
  `h.Class` and `h.Styles`.

Composition:

- `h.Fragment(items...)` — bundle many nodes into one `h.H`. Pass a
  slice with `items...`.
- `h.With(base, more...)` — return a copy of `base` extended with
  `more`. Non-destructive; supports chaining without variadic
  signatures.
- `h.Static(n)` — pre-render `n` once into bytes; every later Render
  writes them verbatim. Use for layout chrome that doesn't depend on
  per-request state. See [Held fragments](#held-fragments) below.

Attributes:

- `h.Class(parts...)` — variadic class names; empty parts skipped;
  returns nil (omits the attribute) when nothing remains.
- `h.Classes(parts...)` — deprecated alias for `h.Class`; kept so a
  slice in hand can spread without a rename.
- `h.ClassMap(m)` — emit each true key in sorted order.
- `h.Style(v)` — inline `style="..."` attribute. For
  `<style>...</style>` use `h.StyleEl`.
- `h.Styles(parts...)` — join non-empty CSS declarations with `;` and
  emit one inline `style` attribute.
- `h.Checked()`, `h.Required()`, `h.Disabled()`, `h.Selected()` —
  boolean attributes (`<input required>`).
- `h.Role`, `h.Min`, `h.Max`, `h.Step`, `h.For`, `h.Lang`,
  `h.Content`, `h.Charset` — common single-string attributes.

Custom tags:

- `h.Tag(name, children...)`, `h.VoidTag(name, children...)` — escape
  hatch for tags absent from the static list (web components, SVG).
  The name is written verbatim; callers remain responsible for
  validity.
- `h.NewTag(name)`, `h.NewVoidTag(name)` — reusable constructors with
  the same shape as the built-ins.

Text:

- `h.Text(s)`, `h.T(s)` — HTML-escaped text node (`T` is the short
  alias). `h.Textf(f, args...)` formats first.
- `h.Raw(s)` — emit `s` verbatim without escaping. Caller-trusted.
- `h.RawAttr(name, value)` — emit a raw `name="value"` attribute pair
  without escaping the value. The sanctioned plugin escape hatch for
  attribute-shaped output (the `attribute` marker is unexported on
  purpose — see `on` for the canonical pattern).

### Held fragments

For fragments that don't change between renders, `h.Static` pre-renders
once and writes the captured bytes on every later Render — no
per-tick allocation for the chrome subtree:

```go
chrome := h.Static(h.Fragment(
    h.Nav(h.Class("container-fluid"),
        h.Ul(h.Li(h.Strong(h.T("System Monitor"))))),
))

func (p *Page) View(ctx *via.Ctx) h.H {
    return h.Div(chrome, p.body(ctx))
}
```

`internal/examples/sysmon` uses this pattern; the
`BenchmarkSysmonShape_staticChrome_render` bench shows the per-tick
allocation win versus rebuilding the same chrome on each tick.

### Custom elements

For tags absent from the static constructor list — web components,
SVG, MathML — declare them once with `h.NewTag` (or `h.NewVoidTag` for
void elements):

```go
var SVG = h.NewTag("svg")
SVG(h.Attr("xmlns", "http://www.w3.org/2000/svg"), shapes...)
```

The tag name is written verbatim; supply a valid HTML element name.

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

Two test surfaces, picked by what you want to verify:

```go
import (
    "github.com/go-via/via"
    "github.com/go-via/via/test"
)
```

**Direct method tests** (no HTTP, no SSE, no session):

```go
c := &Counter{}
ctx := test.NewCtx(t, c)
c.Inc(ctx)
require.Equal(t, 1, c.Hits.Get(ctx))

// Inspect non-state side effects:
require.Equal(t, "/profile", ctx.PendingRedirect())
require.Contains(t, ctx.PendingScripts(), "console.log")
require.Equal(t, "blue", ctx.PendingSignals()["theme"])
```

**End-to-end through HTTP** (SSE, session, full middleware stack):

```go
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
test.AwaitFrame(t, frames, 2*time.Second, ">3<")    // wait for substring

// Multipart action with a file part — switches the request to
// multipart/form-data automatically when any file is attached.
tc.Action(p.Upload).
    WithFile("avatar", "me.png", pngBytes).
    WithSignal("note", "from CLI").
    Fire()
```

`tc.Action` accepts either a method value (compile-time typo protection) or
the action's name as a string. `tc.Reload` re-fetches the mounted page so
post-action body assertions are one call instead of a hand-rolled GET.

## Examples

`internal/examples/` ships:

- `counter` — `State[int]` + `Signal[int]` + a typed action.
- `greeter` — `Signal[string]` mutated from two distinct actions.
- `pathparams` — typed `path:"id"` decoding into composition fields.
- `countercomp` — two independent counter compositions nested on
  one page; isolation across instances.
- `picocss` — `picocss.Plugin()` driving theme + dark-mode switching
  on the client without a full reload.
- `auth` — typed sessions, `requireAuth` middleware, and
  `via.RotateSession` after login.
- `todos` — `scope.User[T]` survives reload, `h.Each`, and
  `on.SetSignal` for client-bundled writes.
- `sysmon` — OnConnect-driven ticker streaming CPU/RAM/disk/net into
  ECharts; also drives an interactive pause + interval-slider UI via
  `via.Ticker.Pause / SetInterval`.
- `upload` — `via.File` field bound to a `multipart/form-data`
  `<form>` POST, persisted to disk, redirect-back-to-/.
- `feed` — append-only stream via `via.Push` / `via.PushBounded`,
  paused/cleared from actions.

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
