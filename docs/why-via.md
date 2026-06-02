---
title: Why Via
layout: default
nav_order: 2
---

# Why Via
{: .no_toc }

1. TOC
{:toc}

Via expresses the client/server reactive split as a **Go type**. `Signal[T]`
is a client signal mirrored into the browser; `StateTab[T]`, `StateSess[T]`,
and `StateApp[T]` are server-only. Whether a piece of UI state round-trips is
a decision made at the field declaration and checked by the compiler — not a
convention you can grep for. See [Reactive state](reactive-state) for the
model, or [Getting started](getting-started) to try it.

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
runtime in the same import. Pick another row if you want a different
language, a template file format, or a different state-ownership split.

**Best fit:** internal tools, admin dashboards, line-of-business apps, and
hobby projects — anywhere you would otherwise reach for Phoenix LiveView,
Hotwire, or htmx + hand-written JS, but want to stay in Go.

## What Via is NOT

Read this before adopting. The non-goals are deliberate.

- **Not an SPA framework.** Routes are server-rendered pages. The browser
  receives HTML, not a JSON bundle.
- **Not a cluster runtime.** `StateApp[T]` and `Broadcast` are
  single-process. Horizontal scaling requires sticky sessions; App state is
  per-pod. There is no built-in fan-out across instances.
- **Not offline-first.** Disconnect the SSE stream and the tab is inert until
  reconnect (the view resyncs automatically once the stream is back) — Via is
  for connected sessions, not PWAs.
- **Not a JavaScript replacement.** The browser still runs Datastar's Alien
  Signals graph. Via removes hand-written JS for the reactivity layer, not
  the runtime.
- **Not a build-step framework.** There is no `via generate`. If you want a
  code-gen template language, look at `templ`.
- **Not stable yet** — pre-1.0, APIs can shift between minor versions. The
  Datastar dependency is load-bearing.

What does and doesn't survive a process restart is spelled out in
[Production & ops](production#restart-and-tab-survivability).
