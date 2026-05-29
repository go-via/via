---
title: Home
nav_order: 1
---

# Via
{: .fs-9 }

Reactive web apps in pure Go. A composition is a struct. Reactive state is
a typed field. Actions are methods. The compiler understands your UI.
{: .fs-6 .fw-300 }

[Get started](getting-started){: .btn .btn-primary .mr-2 }
[View on GitHub](https://github.com/go-via/via){: .btn }
[API reference](https://pkg.go.dev/github.com/go-via/via){: .btn }

---

Via is the only framework — in any language — that expresses the
client/server reactive split as a Go type. `Signal[T]` is a client signal,
mirrored to a fine-grained Alien Signals graph in the browser via Datastar.
`StateTab[T]`, `StateSess[T]`, `StateApp[T]` are server-only. Whether a
piece of UI state round-trips or doesn't is a choice made at the field
declaration, checked by the compiler, not by a convention you can grep for.
Transport is SSE only — one stream per tab — so there are no WebSockets to
wrestle with a corporate proxy.

![Two browsers, two scopes — StateTabNum[int] is per-tab, StateAppNum[int]
is shared across every session.](counter-scope.gif)

**Best fit:** internal tools, admin dashboards, line-of-business apps, and
hobby projects — anywhere you would otherwise reach for Phoenix LiveView,
Hotwire, or htmx + hand-written JS, but want to stay in Go.

## The thesis: the client/server split is a Go type

Every server-rendered framework eventually faces the question "is this
state client-owned or server-owned?" In every other ecosystem the answer
is a convention. In Via it is the field's type.

Declare client-owned state as `Signal[T]`. Declare server-owned state as
`StateTab[T]`, `StateSess[T]`, or `StateApp[T]`. The compiler enforces
which side owns what. View helpers, actions, and lifecycle hooks all see
the correct shape.

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
POST. `Hits` mutates only through an action handler; the next flush diffs
the View and ships targeted DOM patches over SSE.

The four reactive shapes are covered in [Reactive state](reactive-state).

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

## What Via is NOT

Read this before adopting. The non-goals are deliberate.

- **Not an SPA framework.** Routes are server-rendered pages. The browser
  receives HTML, not a JSON bundle.
- **Not a cluster runtime.** `StateApp[T]` and `Broadcast` are
  single-process. Horizontal scaling requires sticky sessions; App state is
  per-pod. There is no built-in fan-out across instances.
- **Not offline-first.** Disconnect the SSE stream and the tab freezes
  until reconnect — Via is for connected sessions, not PWAs.
- **Not a JavaScript replacement.** The browser still runs Datastar's Alien
  Signals graph. Via removes hand-written JS for the reactivity layer, not
  the runtime.
- **Not a build-step framework.** There is no `via generate`. If you want a
  code-gen template language, look at `templ`.
- **Not stable yet** — pre-1.0, APIs can shift between minor versions. The
  Datastar dependency is load-bearing.

See [Production & ops](production#restart-and-tab-survivability) for what
does and doesn't survive a process restart.

## Why Via, not X

| | Language | Authoring | Client runtime | Build step | Reactive state |
|---|---|---|---|---|---|
| **Via** | Go | typed structs + `h` DSL | Datastar (Alien Signals) | none | typed fields, client + server |
| HTMX | any | HTML + `hx-*` attributes | tiny attribute interpreter | none | server-only, manual |
| Phoenix LiveView | Elixir | EEx templates + macros | morphdom + tiny JS | none | `assigns` (Elixir-typed) |
| Hotwire (Turbo) | Ruby | ERB + Turbo Streams | Turbo (WebSocket) | none | server-only, untyped DOM |
| templ | Go | `.templ` template files | none (BYO) | yes (`templ generate`) | none built-in |
| Datastar (direct) | any | HTML + `data-*` attrs | Datastar (Alien Signals) | none | client signals, manual |

Via is the only row that gives you typed end-to-end state (server + client)
with no build step, SSE-only transport, and a fine-grained reactive client
runtime in the same import.
