---
title: Testing
nav_order: 9
---

# Testing

Tests drive the composition through HTTP — the same path as a real browser,
so the full middleware stack, session cookie, and SSE machinery run
end-to-end. There is no "direct method" seam: assertions hit rendered HTML
or SSE frames, never internal state. The harness lives in `via/vt`.

```go
import (
    "github.com/go-via/via"
    "github.com/go-via/via/vt"
)

var server *httptest.Server
app := via.New(via.WithTestServer(&server))
via.Mount[Counter](app, "/")

tc := vt.NewClient(t, server, "/")
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

## Conventions

Tests enter through exported symbols (use `package foo_test`), assert on
observable output (HTML / SSE frames / status / errors), and call
`t.Parallel()` wherever there's no shared mutable state. Use real or stub
implementations over mocks for interfaces you own; reserve mocks for true
system boundaries.
