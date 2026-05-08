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

## Lifecycle hooks

| Method                    | Fires when                                    |
|---------------------------|-----------------------------------------------|
| `Init(ctx) error`         | Before View on the page-load request          |
| `OnConnect(ctx) error`    | First time the SSE stream opens (one-shot)    |
| `Dispose(ctx)`            | Tab closed, ctx swept, or app shut down       |
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

- Set typed state: `c.Hits.Set(ctx, …)`.
- Push targeted patches: `ctx.SyncElements(h.Ul(h.ID("list"), …))`.
- Push raw signals: `ctx.PatchSignal("_picoTheme", "purple")`.
- Run client JS: `ctx.ExecScriptf("alert(%q)", msg)`.
- Redirect: `ctx.Redirect("/profile")`.
- Decode the request payload into a typed struct:

  ```go
  var f LoginForm
  via.DecodeForm(ctx, &f)
  ```

Per-tab actions are serialized — concurrent POSTs to one tab will not
race on State writes.

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

## Routing & groups

```go
via.Mount[Counter](app, "/counter/{id}")

api := app.Group("/api")
api.Use(requireAuth)
via.MountOn[Profile](api, "/profile")

app.Routes()                 // []RouteInfo for boot logging
```

Mounting two routes at the same path panics at registration with the
offending pattern + the original registrar tag. `WithNotFound(h)`
installs a custom 404 handler.

## h package helpers

| Helper                      | What it does                                 |
|-----------------------------|----------------------------------------------|
| `h.Each(items, fn)`         | One node per element; empty slice → nothing  |
| `h.EachIndexed(items, fn)`  | Same with `(i, v)`                           |
| `h.When(cond, build)`       | Lazy `If` — only constructs when true        |
| `h.Group(items)`            | Bundle many nodes into one                   |
| `h.Classes(parts...)`       | Join class names; empty parts skipped        |
| `h.ClassMap(map[string]bool)` | Render only true keys                      |
| `h.IfStr(cond, s)`          | `s` when cond, `""` otherwise                |

## Plugins

```go
app := via.New(via.WithPlugins(
    picocss.Plugin(picocss.WithThemes(picocss.AllPicoThemes)),
    echarts.Plugin(),
))
```

Plugins call `app.AppendToHead`, `app.AppendToFoot`, `app.HandleFunc`,
and `app.RegisterAppSignal(key, value)` for client-driven signals.

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
require.Equal(t, 200, tc.Action("Inc").Fire())
require.Equal(t, 200, tc.Action("Apply").WithSignal("step", 5).Fire())
frames, cancel := tc.SSE(t)
defer cancel()
```

The client name-addresses actions and signals; no HTML scraping.

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

## License

MIT
