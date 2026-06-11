<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="branding/punch-dark.png">
    <img src="branding/punch-light.png" alt="Via — reactive web apps in pure Go" width="220">
  </picture>
</p>

# Via

[![Go Reference](https://pkg.go.dev/badge/github.com/go-via/via.svg)](https://pkg.go.dev/github.com/go-via/via)
[![Go Report Card](https://goreportcard.com/badge/github.com/go-via/via)](https://goreportcard.com/report/github.com/go-via/via)
[![CI](https://github.com/go-via/via/actions/workflows/ci.yml/badge.svg)](https://github.com/go-via/via/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-go--via.github.io%2Fvia-blue)](https://go-via.github.io/via)

**Reactive web apps in pure Go.** A composition is a struct, reactive state
is a typed field, actions are methods — and the compiler understands your UI.
Via is the only framework, in any language, that expresses the
**client/server reactive split as a Go type**: `Signal[T]` lives in the
browser, `StateTab/Sess/App[T]` live only on the server. Which side owns a
piece of state is a field declaration the compiler checks, not a convention
you grep for. Transport is SSE only — no WebSockets, no build step, no
hand-written JS.

📖 **[Documentation](https://go-via.github.io/via)** ·
[API reference](https://pkg.go.dev/github.com/go-via/via) ·
[Examples](https://go-via.github.io/via/examples)

## Install

```bash
go get github.com/go-via/via
```

## Quickstart: the counter

Two counters, two scopes. `Local` is per-tab server state; `Shared` is one
value across every session — clicking `+1` bumps `Local` only in that tab, but
`Shared` everywhere at once. No `Broadcast`, no WebSocket, no client JS.
`on.Click(p.IncShared)` is a typed method reference: the handler signature is
compile-checked and a misspelled method name won't build. It must be a real
bound method, though — a closure or plain function satisfies the type but has
no name to route to, so it panics at the first render rather than at compile
time.

```go
package main

import (
    "net/http"

    "github.com/go-via/via"
    "github.com/go-via/via/h"
    "github.com/go-via/via/on"
)

type Page struct {
    Local  via.StateTabNum[int] // per-tab — independent in every tab
    Shared via.StateAppNum[int] // global — synced across every session
}

func (p *Page) IncLocal(ctx *via.Ctx)  { p.Local.Op(ctx).Inc() }
func (p *Page) IncShared(ctx *via.Ctx) { p.Shared.Op(ctx).Inc() }

func (p *Page) View(ctx *via.CtxR) h.H {
    return h.Div(
        h.P(h.Text("Local: "), p.Local.Text(ctx)),
        h.Button(h.Text("+1"), on.Click(p.IncLocal)),
        h.P(h.Text("Shared: "), p.Shared.Text(ctx)),
        h.Button(h.Text("+1"), on.Click(p.IncShared)),
    )
}

func main() {
    app := via.New()
    via.Mount[Page](app, "/")
    _ = http.ListenAndServe(":3000", app)
}
```

```bash
go run ./internal/examples/counterscope   # open in two browsers
```

![Two browsers, two scopes — StateTab is per-tab, StateApp is shared across every session.](docs/counter-scope.gif)

For state shared across users, see the live chatroom — one app-scoped field
that fans every message out to every connected tab:
[`internal/examples/chat`](internal/examples/chat/main.go) ·
[tutorial](https://go-via.github.io/via/tutorial).

## The four reactive shapes

Whether state lives on the client, the server, or both is the field's type:

| Handle             | Scope       | Lives on        |
| ------------------ | ----------- | --------------- |
| `via.Signal[T]`    | per-tab     | client + server |
| `via.StateTab[T]`  | per-tab     | server only     |
| `via.StateSess[T]` | per-session | server only     |
| `via.StateApp[T]`  | global      | server only     |

`Read(ctx)` / `Update(ctx, fn)` everywhere; `Signal` and `StateTab` add
`Write(ctx, v)`. The `Num` / `Bool` / `Str` / `Slice` / `Map` wrappers add
typed `Op(ctx)` verbs (`Add`, `Toggle`, `Append`, …).
[Full model →](https://go-via.github.io/via/reactive-state)

## What Via is — and is not

- **Is:** server-rendered pages with typed end-to-end state, a reactive
  browser runtime (Datastar — it keeps the page reactive and updates it in
  place), and no build step —
  best for internal tools, dashboards, and line-of-business apps you'd
  otherwise build with LiveView, Hotwire, or htmx + hand-written JS.
- **Is not** an SPA framework — the browser receives HTML, not a JSON bundle.
- **Single-process by default** — without a backplane `StateApp[T]` and
  `Broadcast` are per-pod and horizontal scaling needs sticky sessions.
  `WithBackplane` (in preview) converges `StateApp` state across pods and fans
  `Broadcast` out to every pod's tabs; both inherit the backplane's pre-1.0
  status.
- **Is not** offline-first or stable yet — drop the SSE stream and the tab
  freezes until the client reconnects (transient drops retry automatically; a
  clean-close deploy may fall back to a reload), and APIs can still shift pre-1.0.

## Documentation

The full guide and reference live at
**[go-via.github.io/via](https://go-via.github.io/via)**.

- [Why Via](https://go-via.github.io/via/why-via) — the thesis, and Via vs.
  LiveView / Hotwire / htmx / templ.
- [Getting started](https://go-via.github.io/via/getting-started) ·
  [Tutorial](https://go-via.github.io/via/tutorial) — install, your first
  composition, then build the live chatroom.
- [Reactive state](https://go-via.github.io/via/reactive-state) — `Signal`
  vs `StateTab/Sess/App`, typed ops, view helpers.
- [Actions & lifecycle](https://go-via.github.io/via/actions-and-lifecycle)
  — events, hooks, streaming, broadcast.
- [Rendering](https://go-via.github.io/via/rendering) ·
  [h helpers](https://go-via.github.io/via/h-helpers) — the HTML DSL.
- [Routing & sessions](https://go-via.github.io/via/routing-sessions-middleware)
  — routing, groups, sessions, auth, the middleware stack.
- [File uploads](https://go-via.github.io/via/file-uploads) — `via.File`.
- [Plugins](https://go-via.github.io/via/plugins) — picocss, echarts, maplibre.
- [Testing](https://go-via.github.io/via/testing) ·
  [Production & ops](https://go-via.github.io/via/production) — `vt`; config,
  metrics, security, deploys.
- [Examples](https://go-via.github.io/via/examples) ·
  [Troubleshooting](https://go-via.github.io/via/troubleshooting) ·
  [Glossary](https://go-via.github.io/via/glossary).

## License

MIT
