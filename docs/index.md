---
title: Home
layout: default
nav_order: 1
---

# Reactive web apps in pure Go
{: .fs-9 }

A composition is a struct. Reactive state is a typed field. Actions are
methods. The compiler understands your UI.
{: .fs-6 .fw-300 }

[Get started](getting-started){: .btn .btn-primary .mr-2 }
[Why Via?](why-via){: .btn .mr-2 }
[View on GitHub](https://github.com/go-via/via){: .btn }

---

A complete Via app — a counter whose **step** is client-owned and whose
**count** is server-owned. No template files, no build step, no hand-written
JavaScript:

```go
type Counter struct {
    Hits via.StateTabNum[int]                     // server-owned, per tab
    Step via.SignalNum[int] `via:"step,init=1"`   // client-owned, in the browser
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
```

[Build and run it →](getting-started){: .btn .btn-outline }

## What makes Via different

Via is the only framework — in any language — that expresses the
client/server reactive split as a Go type. `Signal[T]` is a client signal,
mirrored to a fine-grained Alien Signals graph in the browser via Datastar.
`StateTab[T]`, `StateSess[T]`, `StateApp[T]` are server-only. Whether a piece
of UI state round-trips or doesn't is a choice made at the field declaration,
checked by the compiler, not by a convention you can grep for. Transport is
SSE only — one stream per tab — so there are no WebSockets to wrestle with a
corporate proxy.

![Two browsers, two scopes — StateTabNum[int] is per-tab, StateAppNum[int]
is shared across every session.](counter-scope.gif)

## The thesis: the client/server split is a Go type

Every server-rendered framework eventually faces the question "is this state
client-owned or server-owned?" In every other ecosystem the answer is a
convention. In Via it is the field's type.

Declare client-owned state as `Signal[T]`. Declare server-owned state as
`StateTab[T]`, `StateSess[T]`, or `StateApp[T]`. The compiler enforces which
side owns what. View helpers, actions, and lifecycle hooks all see the
correct shape.

```go
type Page struct {
    // Client-owned. Lives in the browser's Alien Signals graph.
    // Bind to <input>; mutate without a round-trip.
    Theme via.Signal[string] `via:"theme,init=auto"`

    // Server-owned. Lives only in Go. Re-renders re-emit the value.
    Hits  via.StateTab[int]
}
```

`Theme` mutates inside the browser — flipping it from an `<input>` does not
POST. `Hits` mutates only through an action handler; the next flush diffs the
View and ships targeted DOM patches over SSE. The four reactive shapes are
covered in [Reactive state](reactive-state).

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

Two reactive runtimes, one typed boundary. Go owns truth; the client owns
view reactivity. UI state the client owns (modal open, current tab, filter
string) reacts instantly with zero SSE traffic; state the server owns (DB
rows, cross-tab invariants, secrets) flows through actions and re-renders.

## Where to go next

- **New here?** [Getting started](getting-started), then build a
  [live chatroom](tutorial) in ~60 lines.
- **Evaluating Via?** [Why Via](why-via) — the comparison matrix and the
  deliberate non-goals.
- **Want working code?** Browse the [examples](examples).
