# Via

Real-time engine for building reactive web applications in pure Go.

## Why Via?

Somewhere along the way, the web became tangled in layers of JavaScript,
build chains, and frameworks stacked on frameworks.

Via takes a radical stance:

- No templates.
- No hand-written JavaScript.
- No transpilation.
- No hydration.
- No front-end fatigue.
- Single Brotli-compressed SSE stream.
- Full reactivity.
- Pure Go.

## Quick Start

```go
package main

import (
  "github.com/go-via/via"
  "github.com/go-via/via/h"
)

func main() {
  v := via.New()

  v.Page("/", func(cmp *via.Cmp) {
    count := via.State(cmp, 0)
    step := via.Signal(cmp, 1)

    increment := cmp.Action(func(ctx *via.Ctx) error {
      count.Set(ctx, count.Get(ctx)+step.Get(ctx))
      return nil
    })

    cmp.View(func(ctx *via.Ctx) h.H {
      return h.Div(
        h.P(h.Textf("Count: %d", count.Get(ctx))),
        h.P(h.Span(h.Text("Step: ")), h.Span(step.Text())),
        h.Label(
          h.Text("Update Step: "),
          h.Input(h.Type("number"), step.Bind()),
        ),
        h.Button(h.Text("Increment"), increment.OnClick()),
      )
    })
  })

  v.Start()
}
```

## Core Concepts

### State and Signals

Via has two reactive primitives, both generic and type-safe:

- **State** — server-side values. Mutating state re-renders the view and
  pushes an HTML patch over SSE.
- **Signal** — client-side reactive values. Bind them to inputs, display them
  with `Text()`, or toggle visibility with `Show()`. The browser owns the
  value; the server reads it on actions and can push updates back.

```go
count := via.State(cmp, 0)        // server-owned, triggers re-render on Set
query := via.Signal(cmp, "")      // client-owned, bound to an input via Bind()
```

### Actions

Actions are server-side event handlers triggered by the browser. State and
signal mutations inside an action are automatically synced — no manual
`Sync()` needed.

```go
submit := cmp.Action(func(ctx *via.Ctx) error {
  name := query.Get(ctx)       // read the signal value sent by the browser
  count.Set(ctx, count.Get(ctx)+1)  // mutate state — auto-synced after return
  return nil
})

// In the view:
h.Button(h.Text("Submit"), submit.OnClick())
h.Input(query.Bind(), submit.OnChange())
h.Input(query.Bind(), submit.OnKeyDown("Enter"))
```

### Components

Reusable UI pieces that encapsulate state, signals, and actions within a
parent page:

```go
v.Page("/", func(cmp *via.Cmp) {
  counter1 := cmp.Component(counterComponent)
  counter2 := cmp.Component(counterComponent)

  cmp.View(func(ctx *via.Ctx) h.H {
    return h.Div(counter1(ctx), counter2(ctx))
  })
})

func counterComponent(cmp *via.Cmp) {
  n := via.State(cmp, 0)
  inc := cmp.Action(func(ctx *via.Ctx) error {
    n.Set(ctx, n.Get(ctx)+1)
    return nil
  })
  cmp.View(func(ctx *via.Ctx) h.H {
    return h.Div(
      h.P(h.Textf("Count: %d", n.Get(ctx))),
      h.Button(h.Text("+"), inc.OnClick()),
    )
  })
}
```

### Lifecycle

```go
v.Page("/dashboard", func(cmp *via.Cmp) {
  data := via.State(cmp, "")

  // Init runs once when the browser connects via SSE.
  // Use it to start background work (polling, tickers, streams).
  cmp.Init(func(ctx *via.Ctx) {
    go func() {
      ticker := time.NewTicker(time.Second)
      defer ticker.Stop()
      for {
        select {
        case <-ctx.Done():
          return
        case <-ticker.C:
          // push updates from a goroutine
          data.Set(ctx, fetchData())
          ctx.Sync()
        }
      }
    }()
  })

  // Dispose runs when the tab closes or the server shuts down.
  cmp.Dispose(func() {
    cleanup()
  })

  cmp.View(func(ctx *via.Ctx) h.H { /* ... */ })
})
```

### Configuration

```go
v := via.New(
  via.WithAddr(":8080"),
  via.WithTitle("My App"),
  via.WithLogLevel(via.LogDebug),
  via.WithShutdownTimeout(10 * time.Second),
  via.WithPlugins(picocss.New(), echarts.Plugin()),
)
```

### State Scopes

```go
// Default: per-tab (each browser tab has its own copy)
count := via.State(cmp, 0)

// App-wide: shared across all tabs and sessions
visits := via.State(cmp, 0, via.WithScopeApp())
```

### Plugins

Plugins hook into the app at startup to register routes, inject head/foot
elements, or modify the HTML document.

```go
// Using built-in plugins
v := via.New(via.WithPlugins(
  picocss.New(
    picocss.WithThemes(picocss.AllPicoThemes),
    picocss.WithDefaultTheme(picocss.PicoThemeAmber),
  ),
  echarts.Plugin(),
))

// Writing a custom plugin
type myPlugin struct{}

func (p myPlugin) Register(app *via.App) {
  app.AppendToHead(h.Link(h.Rel("stylesheet"), h.Href("/style.css")))
  app.HandleFunc("GET /api/health", healthHandler)
}
```

## Experimental

Via is taking its first steps!

- Version `0.2.0` released.
- The API is stabilizing but may still change.

## Contributing

- Via is intentionally minimal and opinionated — and so is contributing.
- If you love Go, simplicity, and meaningful abstractions — come along for the ride!
- Fork, branch, build, tinker with things, submit a pull request.
- Keep every line purposeful.
- Share feedback: open an issue or start a discussion.

## Credits

Via builds upon the work of these amazing projects:

- [Datastar](https://data-star.dev) - The hypermedia powerhouse at the
  core of Via. It powers browser reactivity through Signals and enables
  real-time HTML/Signal patches over an always-on SSE event stream.
- [Gomponents](https://maragu.dev/gomponents) - The awesome project that
  gifts Via with Go-native HTML composition superpowers through the
  `via/h` package.
