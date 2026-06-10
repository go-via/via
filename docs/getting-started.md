---
title: Getting started
layout: default
parent: Learn
nav_order: 1
---

# Getting started
{: .no_toc }

1. TOC
{:toc}

## Install

```bash
go get github.com/go-via/via
```

Via targets a current Go toolchain and has no build step, code generation,
or template files.

## Your first composition

A composition is a Go struct. Reactive state is a typed field, actions are
methods, and `View` renders it.

```go
package main

import (
    "github.com/go-via/via"
    "github.com/go-via/via/h"
    "github.com/go-via/via/on"
)

type Counter struct {
    Hits via.StateTabNum[int]
    Step via.SignalNum[int] `via:"step,init=1"`
}

func (c *Counter) Inc(ctx *via.Ctx) {
    c.Hits.Op(ctx).Add(c.Step.Read(ctx))
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
    app.Start() // binds :3000, wires SIGINT/SIGTERM to a graceful Shutdown
}
```

```bash
go run .
# open http://localhost:3000
```

No template files. No build step. No hand-written JavaScript.
`on.Click(c.Inc)` is a typed method reference: the handler signature is
compile-checked and a misspelled method name won't build. It must be a real
bound method — a closure or plain function type-checks but panics at the first
render, since it has no name to route to.

## What just happened

- `Step` is a `SignalNum[int]` — a **client** signal. The `<input>` it binds
  to (`c.Step.Bind()`) mutates it in the browser with no round-trip.
- `Hits` is a `StateTabNum[int]` — **server-only**, per-tab. It changes only
  through the `Inc` action.
- `Inc` runs server-side, reads the current client `Step`, and updates
  `Hits`. The next flush diffs the `View` and ships a targeted DOM patch
  over the tab's SSE stream — `c.Hits.Text(ctx)` updates in place.

The whole client/server split is visible in the field types. See
[Reactive state](reactive-state) for the full model.

## Next steps

- [Reactive state](reactive-state) — the four shapes, scopes, and typed ops.
- [Actions & lifecycle](actions-and-lifecycle) — events, hooks, streaming.
- [Rendering (h)](rendering) — the HTML DSL.
- [Testing](testing) — drive compositions over HTTP with `vt`.
- Browse `internal/examples/` in the repo (`go run ./internal/examples/counter`).
