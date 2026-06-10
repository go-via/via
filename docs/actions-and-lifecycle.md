---
title: Actions & lifecycle
layout: default
parent: Guides
nav_order: 1
---

# Actions & lifecycle
{: .no_toc }

1. TOC
{:toc}

## Actions

A method on the composition with signature `func(*via.Ctx) error` — or
`func(*via.Ctx)` when nothing in the body can fail meaningfully — is an
action. Bind it to a DOM event with the `on` sub-package:

```go
h.Button(h.Text("+"), on.Click(c.Inc))
h.Form(on.Submit(c.Save), ...)
h.Input(on.Input(c.Filter, on.Debounce("200ms")))
h.Div(on.Key("Enter", c.Send))
h.Button(h.Text("Pick blue"),
    on.Click(c.Apply, on.SetSignal(&c.Theme, "blue")))
```

`on.SetSignal(&c.Field, value)` bundles a typed signal write with the action
so the value updates client-side before the POST fires. `&c.Theme` is
type-checked against the field — the wrong type is a compile error.

Named event helpers include `Click`, `Change`, `Input`, `Submit`, `Focus`,
`Blur`, `DblClick`, `MouseEnter`, `MouseLeave`, `Load`, and `Key`; use
`on.Event("name", fn, ...)` for anything else. Modifiers like
`on.Debounce`, `on.Throttle`, and `on.Prevent` attach to any of them.

## What an action body can do

- Write typed state: `c.Hits.Write(ctx, …)` or `c.Hits.Op(ctx).Add(1)`.
- Push targeted patches: `ctx.Patch().Elements(h.Ul(h.ID("list"), …))`.
- Push raw signals: `ctx.Patch().Signal("_picoTheme", "purple")`.
- Show a quick notification: `ctx.Notify("saved!")` — a styled, non-blocking
  toast that auto-dismisses (JSON-safe, zero setup).
- Redirect: `ctx.Redirect("/profile")`. Only http/https/relative URLs are
  honoured; `javascript:`, `data:`, protocol-relative `//`, and backslash
  variants are dropped and logged (open-redirect / XSS defence).
- Decode the request payload into a typed struct:

  ```go
  var f LoginForm
  via.DecodeForm(ctx, &f)
  ```

Per-tab actions are serialized: concurrent POSTs to one tab cannot race on
State writes.

{: .note }
For try-before-commit and bulk reconciliation flows, `ctx.SyncOff()` opts
the whole action out of the dirty-mark/flush cycle — see godoc.

## Lifecycle hooks

| Method | Fires when |
|---|---|
| `OnInit(ctx) error` | Before View on the page-load request |
| `OnConnect(ctx) error` | First time the SSE stream opens (one-shot) |
| `OnDispose(ctx)` | Tab closed, ctx swept, or app shut down |
| `View(ctx *via.CtxR) h.H` | Required; renders the composition |

`View` receives `*via.CtxR` — a **read-only** render context. You can read
state during a render but cannot mutate it; mutations happen in actions and
lifecycle hooks, which receive the full `*via.Ctx`. That split is enforced by
the type, not by convention.

Implement any subset; `Mount` detects whichever are defined. `OnConnect` is
where long-running per-tab work belongs — bots that hit GET without ever
opening the SSE never trigger it.

## Streaming with `via.Stream`

`via.Stream(ctx, interval, fn)` wires the most common ticker pattern:

```go
func (p *Page) OnConnect(ctx *via.Ctx) error {
    via.Stream(ctx, time.Second, func(ctx *via.Ctx, t time.Time) {
        p.Now.Write(ctx, t.Format("15:04:05"))
    })
    return nil
}
```

`Stream` returns a `*via.Ticker` with `Pause`, `Resume`, `Stop`, and
`SetInterval(d)` so actions can toggle the stream or change cadence at
runtime. The controls are nil-safe and `Stop` is idempotent. See
`internal/examples/sysmon` for a full pause / rate-change UI driven by this
surface.

Inside actions and `via.Stream` callbacks the flush is automatic. From a raw
goroutine you started yourself, call `ctx.SyncNow()` to force a re-render and
push pending writes — it serialises with in-flight action handlers via the
per-tab action mutex.
