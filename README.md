# ⚡Via

Real-time web applications in pure Go. No JavaScript. No build step. No excuses.

## The Web Broke. We Fixed It

Somewhere between jQuery and today's framework circus, the web became an
absurdist comedy. You write JavaScript to transpile JavaScript to bundle
JavaScript to hydrate JavaScript. You install 400MB of node_modules to render
a button. You spend more time configuring Webpack than building features.

**Via says: enough.**

Write Go. Get HTML. Real-time updates via Server-Sent Events. One persistent
connection. Zero JavaScript. Zero build tools. Zero cognitive overhead.

## What You Get

- **No templates** - Type-safe HTML composition in pure Go
- **No JavaScript** - Not a single line. Not even JSON serialization on your end
- **No transpilation** - No build step. No bundler. No toolchain hell
- **No hydration** - Server renders. Browser receives. That's it
- **Full reactivity** - Real-time UI updates via SSE
- **Single connection** - One persistent stream, not REST spam
- **Built-in compression** - Brotli level 5, automatic
- **Pure Go** - If you know Go, you know Via
- **State scopes** - Tab, session, or app-wide state
- **Session data handles** - Access session-scoped data from anywhere
- **Testing utilities** - Ergonomic vtest package for integration tests

## Getting Started

Install Via:

```bash
go get github.com/go-via/via
```

Create `main.go`:

```go
package main

import (
    "github.com/go-via/via"
    "github.com/go-via/via/h"
)

func main() {
    v := via.New()

    v.Page("/", func(c *via.Composition) {
        count := via.State(c, 0)
        step := via.Signal(c, 1)

        increment := via.Action(c, func(ctx *via.Context) {
            count.Set(s, count.Get(s)+step.Get(s))
        })

        c.View(func(ctx *via.Context) h.H {
            return h.Div(
                h.H1(h.Text("Counter Example")),
                h.P(h.Textf("Count: %d", count.Get(s))),
                h.Label(h.Text("Step: ")),
                h.Input(h.Type("number"), h.Name("step"), step.Bind()),
                h.Button(h.Text("Increment"), increment.OnClick()),
            )
        })
    })

    v.Start()
}
```

Run it:

```bash
go run main.go
```

Open `http://localhost:3000`. Click the button. Watch the counter update in
real-time. No JavaScript was written. No build step was run. No frustration was
needed in the making of this demo.

That's it. That's the entire stack.

## Reusable Components

```go
package main

import (
    "github.com/go-via/via"
    "github.com/go-via/via/h"
)

type CounterProps struct {
    Name string
    Step int
}

func NewCounter(props CounterProps) via.ComposeFn {
    return func(c *via.Composition) {
        count := via.State(c, 0)
        step := via.Signal(c, props.Step)

        increment := via.Action(c, func(ctx *via.Context) {
            count.Set(s, count.Get(s)+step.Get(s))
        })

        c.View(func(ctx *via.Context) h.H {
            return h.Div(
                h.H2(h.Text(props.Name)),
                h.P(h.Textf("Count: %d", count.Get(s))),
                h.Input(h.Type("number"), step.Bind()),
                h.Button(h.Text("+"), increment.OnClick()),
            )
        })
    }
}

func main() {
    v := via.New()

    v.Page("/", func(c *via.Composition) {
        counter1 := c.Component(NewCounter(CounterProps{Name: "Clicks", Step: 1}))
        counter2 := c.Component(NewCounter(CounterProps{Name: "Jumps", Step: 10}))

        c.View(func(ctx *via.Context) h.H {
            return h.Div(
                counter1.Mount(s),
                counter2.Mount(s),
            )
        })
    })

    v.Start()
}
```

## State Scopes

State can have different lifetimes:

```go
// Tab scope (default) - unique per browser tab
clicks := via.State(c, 0)

// Session scope - shared across tabs for same user
preferences := via.State(c, "", via.WithScope(via.ScopeSession))

// App scope - global across all users
visitorCount := via.State(c, 0, via.WithScope(via.ScopeApp))
```

## Session Data Handles

Access session-scoped data from middleware, actions, or views:

```go
type User struct {
    ID   string
    Name string
}

// Create handle (typically at module level)
userHandle := via.NewSessionDataHandle[User]()

// In middleware - set data
userHandle.Set(ctx, User{ID: "1", Name: "Alice"})

// In action or view - retrieve data
if user, ok := userHandle.Get(ctx); ok {
    // use user
}

// Clear data and invalidate session
userHandle.Clear(ctx)
```

**Key distinction:** Session data handles are for cross-cutting concerns set in middleware (auth, config). For reactive state that triggers UI updates, use `State` with `ScopeSession`.

## How It Works

**State** lives on the server. **Signals** sync between browser and server.
**Actions** mutate state. **Views** render HTML. Changes stream to the browser
via SSE. The browser morphs the DOM. Everything just works.

You write Go functions. Via handles the rest.

## What About

**"But I need JavaScript for..."** - No, you don't. Via handles forms, real-time
updates, and interactivity through Datastar. If you truly need custom JS, you
can still add it. Via doesn't hold you hostage.

**"But React/Vue/Svelte..."** - Great tools. Wrong paradigm. They make you write
your application twice: once on the server, once on the client. Via makes you
write it once: on the server. In Go. Where your data already lives.

**"But what about performance?"** - Brotli-compressed SSE streams. Morphdom for
surgical DOM updates. Go's goroutines for concurrency. It's fast. Faster than
your SPAs bloated with megabytes of JavaScript.

**"But SEO..."** - Server-rendered HTML. Every page load is pure HTML. Search
engines don't need to execute JavaScript. They just parse HTML like it's 2005.
Which it is. But better.

## Status: 🚧 Experimental

Via just took its first steps. Version `0.1.0` is out. The API is stabilizing.
The examples work. The tests pass. Production? Your call.

Expect rough edges. Expect rapid iteration. Expect breaking changes until `1.0`.

## Contributing

Via is intentionally minimal. Every feature fights for its place. Every
abstraction earns its keep.

If you love Go, simplicity, and meaningful design:

1. Fork it
2. Branch it
3. Build it
4. Test it
5. Send a PR

Keep every line purposeful. Share feedback via issues or discussions.

## Credits

Via stands on the shoulders of giants:

- 🚀 [Datastar](https://data-star.dev) - The hypermedia powerhouse behind
  Via's reactivity. Powers browser signals and SSE-based DOM patching without
  a single line of JavaScript on your end.
- 🧩 [Gomponents](https://maragu.dev/gomponents) - The brilliant library that
  gives Via type-safe, composable HTML generation through the `via/h` package.

> Thank you for building tools that don't just work — they inspire 🫶

## License

MIT. Build something great.
