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

For common read-modify-write patterns there are typed helpers that work
on any handle satisfying `via.Mutable[T]` — that's all four reactive
shapes: `Signal[T]`, `State[T]`, `scope.User[T]`, `scope.App[T]`:

```go
s.Update(ctx, func(n int) int { return n + 1 })  // generic transform
via.Add(ctx, &p.Count, 1)                         // numeric delta
via.Toggle(ctx, &p.Open)                          // bool flip
```

`Signal[T]` (client-mirrored) also exposes view helpers for composing
with `h`:

```go
s.Bind()  // <input data-bind="key"> two-way binding
s.Text()  // <span data-text="$key"></span> reactive text node
s.Show()  // data-show="$key" on parent — toggles display by truthiness
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
- Show a quick alert: `ctx.Toast("saved!")` (JSON-safe).
- Redirect: `ctx.Redirect("/profile")`.
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

| Factory                          | What it does                                  |
|----------------------------------|-----------------------------------------------|
| `Defaults(app)`                  | install RequestID + AccessLog + Recover       |
| `RequestID()`                    | stamp X-Request-ID + plant on r.Context       |
| `AccessLog(app)`                 | one info-line per request, with rid + status  |
| `Recover(app)`                   | panic → 500 + error log; goroutine survives   |
| `StrictCSP(extra…)`              | strict CSP header + nonce on r.Context        |
| `HSTS(opts…)`                    | Strict-Transport-Security for HTTPS deploys   |
| `RedirectHTTPS()`                | 301 plain HTTP → https; respects XFP header   |

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

| Helper                              | What it does                                  |
|-------------------------------------|-----------------------------------------------|
| `h.Each(items, fn)`                 | One node per element; empty slice → nothing   |
| `h.EachIndexed(items, fn)`          | Same with `(i, v)`                            |
| `h.EachSeq(seq, fn)`                | Iter-based variant (slices.Values, maps.Values…) |
| `h.EachSeq2(seq, fn)`               | Same for `iter.Seq2` (maps.All, slices.All…)  |
| `h.If(cond, n)`                     | Render `n` when cond, nothing otherwise       |
| `h.IfElse(cond, then, els)`         | Eager two-branch picker                       |
| `h.When(cond, build)`               | Lazy `If` — only constructs when true         |
| `h.WhenElse(cond, then, els)`       | Lazy `IfElse` — only the winning builder runs |
| `h.Switch(value, h.Case…, h.Default…)` | Tab-style branching by equality            |
| `h.Fragment(items…)`                | Bundle many nodes into one (use `items...` for slices) |
| `h.Classes(parts...)`               | Join class names; empty parts skipped         |
| `h.ClassMap(map[string]bool)`       | Render true keys in sorted (stable) order     |
| `h.IfStr(cond, s)`                  | `s` when cond, `""` otherwise                 |

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

- `counter` — basic state + signal + actions
- `greeter` — Signal[string] mutated by two actions
- `pathparams` — typed path:"…" decoding
- `countercomp` — two nested counter cards on one page
- `picocss` — theme/dark-mode showcase
- `auth` — typed sessions + `RotateSession` + `requireAuth` middleware
- `todos` — h.Each + scope.User + on.SetSignal
- `sysmon` — per-tab ticker streaming CPU/RAM/disk/net charts via `OnConnect`
- `upload` — `via.File` field driving a multipart `<form>` POST

```bash
go run ./internal/examples/counter
```

## Configuration

| Option                            | Default       |
|-----------------------------------|---------------|
| `WithAddr(":3000")`               | `:3000`       |
| `WithTitle("Via")`                | `Via`         |
| `WithLang("en")`                  | unset         |
| `WithDescription(s)`              | unset         |
| `WithSessionTTL(30m)`             | 30 min        |
| `WithContextTTL(15m)`             | 15 min        |
| `WithSSEHeartbeat(25s)`           | 25 s          |
| `WithMaxRequestBody(1<<20)`       | 1 MiB         |
| `WithMaxContexts(n)`              | 0 (no cap)    |
| `WithReadHeaderTimeout(10s)`      | 10 s          |
| `WithReadTimeout(d)`              | 0 (disabled)  |
| `WithWriteTimeout(d)`             | 0 (SSE-safe)  |
| `WithIdleTimeout(120s)`           | 120 s         |
| `WithActionErrorHandler(fn)`      | browser alert |
| `WithSecureCookies()`             | off           |
| `WithHTTPServer(hook)`            | nil           |
| `WithLogger(l)`                   | log.Printf    |
| `WithLogLevel(LogWarn)`           | warn          |
| `WithNotFound(h)`                 | std 404       |
| `WithShutdownTimeout(5s)`         | 5 s           |
| `WithPlugins(...)`                | none          |
| `WithTestServer(&server)`         | nil           |

## Security

What via defends against by default and what you opt into:

### CSRF

Every page mints a 256-bit `via_tab` id (32 bytes from `crypto/rand`,
hex-encoded). Every action POST and SSE handshake must carry it; the
server looks the id up in its in-memory registry and rejects unknown
ids with 404. Forged requests can't guess a live tab id, and the same-
origin policy keeps a third-party site from reading one. Action POSTs
are also session-pinned: if the cookie sent doesn't match the session
the tab was minted under, the request is 403'd before the action runs.
The `via_tab` id is the CSRF token — no separate token plumbing.

### Sessions

`via_session` is the cookie name. Defaults: `HttpOnly`, `SameSite=Lax`,
`Path=/`, 64-char hex value (32 bytes from `crypto/rand`). The `Secure`
flag is opt-in via `WithSecureCookies()` — some deployments terminate
TLS at a proxy and run plain HTTP between proxy and app, so the default
stays off. Pair with `HSTS()` and `RedirectHTTPS()` for a pure-HTTPS
posture. After authentication state changes, call `RotateSession(ctx)`
to mint a new id and invalidate the captured pre-auth one (session-
fixation defence).

### CSP

`StrictCSP()` middleware sets a strict header (`default-src 'self';
script-src 'self' 'nonce-XYZ'; object-src 'none'; base-uri 'self'`)
with a per-request nonce threaded through `r.Context` and reachable via
`ctx.CSPNonce()`. Pass extra directives as variadic args. Without
`StrictCSP`, no CSP header is sent.

### Body limits

`WithMaxRequestBody(n)` (default 1 MiB) caps action POST and SSE-close
bodies via `http.MaxBytesReader`. Oversized requests return
413 Request Entity Too Large.

### Action error sanitization

When an action panics, the default error handler replaces the panic
message with `"Something went wrong"` before sending it to the client
— so an accidental `panic("password=" + secret)` doesn't surface
sensitive internals. User-returned errors (non-panic) flow through
unmodified — the user controls what's in their own error.

### Random sources

`crypto/rand.Read` failures **panic** rather than fall back to
zero-byte ids. Failing loud beats shipping predictable session/tab
tokens.

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
