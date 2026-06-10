---
title: Testing
layout: default
parent: Reference & ops
nav_order: 1
---

# Testing

Tests drive the composition through the same HTTP path the server uses, so
the full middleware stack, session cookie, and SSE machinery run end-to-end.
There is no "direct method" seam: assertions hit rendered HTML or SSE frames,
never internal state. The harness lives in `via/vt`.

```go
import (
    "github.com/go-via/via"
    "github.com/go-via/via/vt"
)

app := via.New()
srv := vt.Serve(t, app) // httptest server dispatching through App.ServeHTTP
via.Mount[Counter](app, "/")

tc := vt.NewClient(t, srv, "/")
c := &Counter{}
require.Equal(t, 200, tc.Action(c.Inc).Fire())   // typed: typo → compile error
require.Equal(t, 200, tc.Action("Apply").        // string still works
    WithSignal("step", 5).Fire())
require.Contains(t, tc.Reload(), ">1<")

frames, cancel := tc.SSE()
defer cancel()
vt.AwaitFrame(t, frames, 2*time.Second, ">3<")

tc.Action(p.Upload).
    WithFile("avatar", "me.png", pngBytes).
    WithSignal("note", "from CLI").
    Fire()
```

## API

- `vt.NewClient(t, server, path)` — performs the initial GET (acquiring the
  `via_tab` id + session cookie on a shared jar) and returns a `*Client`.
- `tc.Action(target)` — accepts a **method value** (compile-time typo
  protection) or the action's **name** as a string. Chain `.WithSignal`,
  `.WithFile`, then `.Fire()` (returns the HTTP status).
- `tc.HTML()` / `tc.Reload()` — the initial / re-fetched page body, so
  post-action body assertions are one call.
- `tc.SSE()` / `tc.SSEReady()` — open the tab's SSE stream; `SSEReady`
  blocks until the server handshake so there's no timing guess.
- `vt.AwaitFrame(t, frames, timeout, needles...)` — wait until all needles
  appear across the accumulated frames; returns the matched content.
- `tc.Fork(path)` — a second tab on the same cookie jar — the only way to
  drive `StateSess` behaviour that spans tabs.

## What vt does not simulate

`vt` runs the real server, but it is **not** a browser — Datastar never
executes. A green `vt` test is necessary, not sufficient. In particular:

- **Local (`_`-prefixed) signals are sent.** A real browser never POSTs a
  `_`-prefixed signal to the server; `WithSignal("_open", v)` does. A test can
  pass while the in-browser behaviour differs.
- **No client-side key filter / debounce / bind coercion.** `on.Key`,
  `on.Debounce`, and `data-bind` value coercion are evaluated by Datastar in
  the browser. `vt` posts the action body directly, so it cannot reproduce
  them.
- **Frames are matched as raw strings.** `AwaitFrame` does a substring match
  over the accumulated SSE bytes, not a parsed DOM — it cannot assert element
  structure, and a needle can match a stale frame.

For behaviour that depends on the Datastar client — local signals, key
filters, reconnect/retry, multi-tab cookie races — verify in a real browser
(e.g. a `chromedp`/Playwright check against the `vt.Serve` URL) in addition to
the `vt` test.

## Conventions

Tests enter through exported symbols (use `package foo_test`), assert on
observable output (HTML / SSE frames / status / errors), and call
`t.Parallel()` wherever there's no shared mutable state. Use real or stub
implementations over mocks for interfaces you own; reserve mocks for true
system boundaries.
