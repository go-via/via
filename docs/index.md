---
title: Home
layout: default
nav_order: 1
---

<!-- The docs theme is always dark, so serve the dark-background (cream) variant
     directly rather than a prefers-color-scheme <picture> — otherwise an
     OS-light visitor gets the ink-letter variant and only the amber slash
     shows against the dark page. -->
<p align="center">
  <img src="{{ '/assets/branding/punch-dark.png' | relative_url }}" alt="Via" width="220">
</p>

# Reactive web apps in pure Go
{: .fs-9 }

A composition is a struct. Reactive state is a typed field. Actions are
methods. The compiler understands your UI.
{: .fs-6 .fw-300 }

[Get started](getting-started){: .btn .btn-primary .mr-2 }
[Why Via?](why-via){: .btn .mr-2 }
[View on GitHub](https://github.com/go-via/via){: .btn }

---

A complete Via app — a **Local** counter that's independent in every tab and a
**Shared** counter that syncs across every session. No template files, no build
step, no hand-written JavaScript:

```go
type Page struct {
    Local  via.StateTabNum[int] // per-tab — independent in every tab
    Shared via.StateAppNum[int] // shared across every session
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
```

[Build and run it →](getting-started){: .btn .btn-outline }

## What makes Via different

Via is the only framework — in any language — that expresses the
client/server reactive split as a Go type. `Signal[T]` is a client signal,
mirrored into the browser by Datastar — the runtime Via uses to keep the page
reactive and update it in place. `StateTab[T]`, `StateSess[T]`, `StateApp[T]` are
server-only. Whether a piece
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
    // Client-owned. Lives in the browser, driven by Datastar.
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
   │  Datastar runtime        │                       │  Compositions            │
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

## Scale across pods

The **Shared** counter above is per-pod by default. Wire in a
[backplane](distributed-state) and it converges across every instance — and
survives a restart — with the *same typed fields*. One line at boot:

```go
app := via.New(via.WithBackplane(via.InMemory())) // dev: no infra
// prod: via.WithBackplane(bp) where bp, _ := vianats.JetStream(nc) — durable, clustered
```

`StateApp`/`StateSess` cluster with no API change; a new opt-in
`StateAppEvents[E, V]` carries high-churn shared state — a counter, a chat feed,
a queue — as an append-only event log that every pod folds to the same value.
In **preview** on the way to 1.0; see [Distributed state](distributed-state).

## Where to go next

- **New here?** [Getting started](getting-started), then build a
  [live chatroom](tutorial) in ~60 lines.
- **Evaluating Via?** [Why Via](why-via) — the comparison matrix and the
  deliberate non-goals.
- **Want working code?** Browse the [examples](examples).
