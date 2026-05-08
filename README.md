# Via

Real-time engine for building reactive web applications in pure Go.

## Why Via?

The web has become tangled in layers of JavaScript, build chains, and
frameworks stacked on frameworks. Via takes a different stance:

- No templates.
- No hand-written JavaScript.
- No transpilation.
- No hydration.
- Single SSE stream.
- Pure Go.
- A composition is a Go struct. Reactive state is a typed field. Actions
  are methods. The compiler understands your UI.

## Quick Start

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

func (c *Counter) Inc(ctx *via.Ctx) error {
    c.Hits.Set(ctx, c.Hits.Get(ctx)+c.Step.Get(ctx))
    return nil
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

`*App` implements `http.Handler`, so it slots straight into any standard
mux or middleware stack.

## API at a glance

### Composition types

A composition is a struct. Implement `View(ctx *via.Ctx) h.H`; everything
else is optional. Mount it at a route:

```go
via.Mount[MyPage](app, "/dashboard")
via.MountOn[Card](apiGroup, "/card/{id}")
```

### Reactive state

| Handle              | Scope          | Lives on        |
|---------------------|----------------|-----------------|
| `via.Signal[T]`     | per-tab        | client + server |
| `via.State[T]`      | per-tab        | server only     |
| `scope.User[T]`     | per-session    | server only     |
| `scope.App[T]`      | global         | server only     |

All four are zero-value-usable struct fields. Reads and writes go through
`Get(ctx) / Set(ctx, v)` — explicit context, no hidden globals.

The wire key for a signal is the lower-cased field name; override with
the `via:"name"` tag, and seed an initial value with `via:"name,init=value"`.

### Actions

A method on `*Composition` of signature `func(*via.Ctx) error` is an
action. Bind it to a DOM event with the `on` sub-package:

```go
h.Button(h.Text("+"), on.Click(c.Inc))
h.Form(on.Submit(c.Save), ...)
h.Input(on.Input(c.Filter, on.Debounce("200ms")))
h.Div(on.Key("Enter", c.Send))
```

`on.Click(c.Inc)` reads the method name from the bound method value and
renders a Datastar `@post('/_action/<method>')` attribute.

### Path parameters

```go
type Profile struct {
    UserID int    `path:"id"`
    Slug   string `path:"slug"`
}
via.Mount[Profile](app, "/u/{id}/posts/{slug}")
```

Each `path:"name"` tag must match a `{name}` segment in the route.

### Lifecycle hooks

| Method                       | Fires when                              |
|------------------------------|-----------------------------------------|
| `Init(ctx) error`            | Before View on the page-load request    |
| `Dispose(ctx)`               | Tab closed, ctx swept, or app shut down |
| `View(ctx) h.H`              | Required; renders the composition       |

### Plugins

```go
app := via.New(via.WithPlugins(
    picocss.Plugin(picocss.WithThemes(picocss.AllPicoThemes)),
    echarts.Plugin(),
))
```

A plugin's `Register(*App)` runs at `New` time and can:

- `app.AppendToHead`, `app.AppendToFoot`, `app.AppendAttrToHTML`
- `app.HandleFunc` for plugin-specific routes
- `app.RegisterAppSignal(key, value)` for client-driven app signals

### Testing

```go
import (
    via "github.com/go-via/via"
    "github.com/go-via/via/test"
)

var server *httptest.Server
app := via.New(via.WithTestServer(&server))
via.Mount[Counter](app, "/")

tc := test.NewClient(t, server, "/")
require.Equal(t, 200, tc.Action("Inc").Fire())
```

The client name-addresses actions and signals; no HTML scraping required.

## Examples

`internal/examples/` ships:

- `counter` — basic state + signal + actions
- `greeter` — Signal[string] mutated from two distinct actions
- `pathparams` — typed path:"…" tag decoding
- `countercomp` — two nested counter cards on one page
- `picocss` — full PicoCSS showcase via the plugin

```bash
go run ./internal/examples/counter
```

## Configuration

Pass options to `via.New`:

| Option                          | Default        | Notes                              |
|---------------------------------|----------------|------------------------------------|
| `WithAddr(":3000")`             | `:3000`        | listen address for `Start()`       |
| `WithTitle("Via")`              | `Via`          | document `<title>`                 |
| `WithSessionTTL(30m)`           | 30 min         | session cookie expiry              |
| `WithContextTTL(15m)`           | 15 min         | per-tab Ctx idle expiry            |
| `WithSSEHeartbeat(25s)`         | 25 s           | keep-alive comment frame interval  |
| `WithMaxRequestBody(1<<20)`     | 1 MiB          | body cap on action / close beacon  |
| `WithActionErrorHandler(fn)`    | browser alert  | replace default error UX           |
| `WithSecureCookies()`           | off            | mark session cookie Secure         |
| `WithHTTPServer(hook)`          | nil            | last-mile `*http.Server` tweaks    |
| `WithLogLevel(LogWarn)`         | warn           | minimum log severity               |

## License

MIT
