---
title: Why Via
layout: default
nav_order: 2
---

# Why Via
{: .no_toc }

1. TOC
{:toc}

**Via is a Go framework for building reactive, server-rendered web apps** — no
JavaScript build step, no separate template language, and no hand-maintained
client/server API. You write Go structs and an `h(...)` view; the browser gets
HTML plus a small client runtime.

Its one idea: Via expresses the **client/server reactive split as a Go type**.
`Signal[T]` is a client-owned value mirrored into the browser; `StateTab[T]`,
`StateSess[T]`, and `StateApp[T]` are server-owned. Whether a piece of UI state
*round-trips* — makes a server request to change — is decided at the field
declaration and checked by the compiler, not by a convention you grep for. The
browser runtime is Datastar, which keeps the page reactive and updates it in
place; Via ships it so you don't hand-write the reactive layer.

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

The scope of each value — and whether it crosses the network — *is* the field
type. (`*via.Ctx` is the action context an event handler gets; `*via.CtxR` is
the read-only render context `View` gets.) See [Reactive state](reactive-state)
for the model, or
[Getting started](getting-started) to run it.

## Why Via, not X

Each row is an alternative you might already use — skim to the one you know.

| | Language | Authoring | Client runtime | Build step | Reactive state |
|---|---|---|---|---|---|
| **Via** | Go | typed structs + `h` DSL | Datastar | none | typed fields, client + server |
| HTMX | any | HTML + `hx-*` attributes | tiny attribute interpreter | none | server-only, manual |
| templ + HTMX (+ Alpine) | Go | `.templ` files + `hx-*`/`data-*` | HTMX + BYO reactivity | `templ generate` | typed templates, untyped state wiring |
| Phoenix LiveView | Elixir | EEx templates + macros | morphdom + tiny JS | asset pipeline | `assigns` (server, Elixir-typed) |
| Hotwire (Turbo) | Ruby | ERB + Turbo Streams | Turbo (HTTP; Streams over WS/SSE) | asset pipeline | server-only, untyped DOM |
| Datastar (direct) | any | HTML + `data-*` attrs | Datastar | none | client signals, manual |

Via's wedge is the first column most of the table can't claim: the
client/server state split is a **typed Go field — end-to-end, compiler-checked,
no build step, no glue code**. The SSE-only transport and the fine-grained
client runtime are *inherited from Datastar* (the Datastar row has the same
runtime) — Via's contribution is the typed Go layer over it, in one import.

The closest Go-native alternative is **templ + HTMX**: templ gives you typed
templates, but the state wiring (form values, what round-trips) is untyped and
the client reactivity is hand-wired. Pick another row if you want a different language, a template-file
format, a non-SSE transport, or a different state-ownership split.

**Best fit:** internal tools, admin dashboards, and line-of-business apps —
anywhere you'd otherwise reach for Phoenix LiveView, Hotwire, or htmx +
hand-written JS but want to stay in Go. (It's a pleasant fit for hobby projects
too.)

**Don't reach for Via if** you need offline/PWA support, a public
SEO-critical or JS-disabled audience, a transport other than SSE, a non-Go team,
cluster-wide realtime push, or you can't budget one long-lived connection plus
an in-memory context per open tab (see [what it costs to run](#what-it-costs-to-run)).

## What Via is NOT

Read this before adopting. The list aims to warn you, not to flatter Via — the
non-goals are deliberate.

- **Not stable yet.** Pre-1.0: APIs can shift between minor versions.
- **Not an SPA framework.** Routes are server-rendered pages; the browser
  receives HTML, not a JSON bundle. No client-side routing, no offline store.
- **Coupled to Datastar.** The client reactivity *is* the Datastar runtime —
  a third-party dependency you inherit, including its reconnect/retry
  behavior. Via removes hand-written reactive JS; it does not abstract over the
  runtime and cannot outlive it.
- **Single-process by default.** App state is per-pod and horizontal scaling
  needs sticky sessions. A [backplane](distributed-state) lifts that —
  `WithBackplane` clusters `StateApp`/`StateSess` with no API change and adds an
  opt-in `StateAppEvents[E, V]` event-log shape — but it is in **preview** on the
  way to 1.0: eventually consistent, with no global ordering or cross-key
  transactions, and cross-tab `Broadcast` stays pod-local. Treat single-process
  as the supported topology until it ships.
- **Not offline-first.** Drop the SSE stream and the tab is inert until it
  reconnects. Transient drops usually resync or re-bootstrap automatically; some
  cases (e.g. a deploy that closes the stream cleanly) fall back to a full page
  reload — see [Production & ops](production#restart-and-tab-survivability) for
  the exact recovery modes and their limits.
- **Auth is yours.** Via ships CSRF protection, sessions, and session-pinning —
  but not authentication or authorization. Bring your own.
- **Not for large uploads/streaming out of the box.** Action bodies are capped
  (`WithMaxRequestBody`, 1 MiB default); Via isn't built for large-media
  streaming.
- **Not a build-step framework.** There is no `via generate`. If you want a
  code-gen template language, use [`templ`](https://templ.guide).

## What it costs to run

Via holds **one SSE connection and one in-memory context per live tab** —
capacity scales with open tabs, not request rate. Plan file-descriptor and
memory limits accordingly; `WithMaxContexts` caps live tabs, and a tab over the
cap is told to reload rather than dropped silently. What does and doesn't
survive a process restart is spelled out in
[Production & ops](production#restart-and-tab-survivability).

---

Convinced? → [Getting started](getting-started). Want the state model? →
[Reactive state](reactive-state).
