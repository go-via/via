---
title: Production & ops
parent: Reference & ops
nav_order: 2
---

# Production & ops
{: .no_toc }

1. TOC
{:toc}

## Production wiring

```go
app := via.New(
    via.WithLang("en"),
    via.WithLogger(via.SlogLogger(slog.Default())),
    via.WithMaxRequestBody(1<<20),
    via.WithMaxContexts(10000),
    via.WithSSEHeartbeat(25*time.Second),
)
mw.Defaults(app)
app.Use(mw.HSTS())
app.Use(mw.CSP())
app.Use(mw.RedirectHTTPS())

via.Mount[Home](app, "/")
api := app.Group("/api")
api.Use(requireAuth)
via.Mount[Profile](api, "/profile")

http.ListenAndServe(":8080", app)
```

## Configuration

Every `WithX(...)` option is documented in
[godoc](https://pkg.go.dev/github.com/go-via/via) with its default and
behaviour. Common production knobs:

- `WithMaxContexts(n)`, `WithLogger(SlogLogger(...))`,
  `WithInsecureCookies()` (dev opt-out — `Secure` is on by default)
- `WithMaxRequestBody(n)`, `WithSessionTTL(d)`, `WithContextTTL(d)`
- `WithSSEHeartbeat(d)`, `WithReadHeaderTimeout(d)`, `WithIdleTimeout(d)`
- `WithActionErrorHandler(fn)`, `WithNotFound(h)`, `WithHTTPServer(hook)`

## Security defaults

- **CSRF:** every page mints a 256-bit `via_tab` id; action POSTs and SSE
  handshakes carry it as a signal. The id **is** the CSRF token — unknown
  ids 404. Action POSTs are also session-pinned (cookie mismatch → 403).
- **Sessions:** the `via_session` cookie is `HttpOnly`, `SameSite=Lax`,
  256-bit, and `Secure` by default; `WithInsecureCookies()` drops `Secure`
  for a local http:// dev loop. After auth-state changes call
  `sess.Rotate(ctx)` (session-fixation defence).
- **CSP:** `mw.CSP()` emits a strict header with a per-request nonce
  reachable via `ctx.CSPNonce()`.
- **Body limits:** `WithMaxRequestBody(n)` (default 1 MiB) caps action POST
  and SSE-close bodies; oversized requests return 413.
- **Open redirects:** `ctx.Redirect` rejects `javascript:`/`data:`/
  protocol-relative/backslash and whitespace-only URLs.
- **Panic sanitization:** action panics surface as `"Something went wrong"`
  to the client; user-returned errors flow through unmodified.
- **Random sources:** `crypto/rand.Read` failures panic rather than fall
  back to predictable zero-byte ids.

## Metrics

`via.WithMetrics(m)` accepts an implementation of the `Metrics` interface
and emits structured events for ops dashboards:

| Event | Kind | Labels |
|---|---|---|
| `via.action.total` | counter | `method` |
| `via.action.latency` | histogram | `method` |
| `via.render.total` | counter | `route` |
| `via.sse.connect` | counter | |
| `via.sse.disconnect` | counter | |
| `via.ctx.live` | gauge | |

Adapt to Prometheus, OTel, or expvar by implementing three methods
(`Counter`, `Gauge`, `Histogram`) that forward to your backend. The default
backend discards every event, so apps that don't configure metrics pay no
allocation cost.

## Cross-tab broadcast

```go
app.Broadcast(`alert("Maintenance in 30 seconds.")`)
app.BroadcastSignals(map[string]any{"_systemNotice": "site read-only"})
app.LiveTabs()
```

`Broadcast` queues a JS snippet on every live tab; `BroadcastSignals` queues
a signal patch. Both return the tab count they reached and deliver via the
existing patch queue + SSE drain — no extra wiring. Single-process only.

## Restart and tab survivability

A live tab's state lives in memory on the server (the `*via.Ctx` and its
session). It does **not** survive a process restart:

- After a deploy, every client's `via_tab` is unknown to the new process.
  The next SSE reconnect 404s and the next action POST 404s.
- Datastar retries the SSE connection forever, so a user watching a stale
  tab sees it freeze rather than recover. Tell users to reload, or pair the
  deploy with a sticky load balancer that drains long enough for tabs to
  close naturally.
- Sessions are also in-memory; logged-in users re-auth unless you back the
  session store with something durable.

For session survivability, persist the `sess.Put`-stored payload (e.g. a JWT
or opaque token) to a database keyed by the `via_session` cookie value, and
rehydrate inside an `OnInit` hook.

## Performance

Benchmarks: `bench_test.go` (full request → SSE turn) and
`h/h_bench_test.go` (DSL only). Run `go test -bench=. -benchmem` against your
target hardware — quoting numbers from someone else's laptop is rarely
useful. `ci-check.sh` gates the steady-state allocation floors on
`CounterRender`, `CounterAction`, and `CounterActionWithLogger` so
regressions fail CI.

`h.Static(...)` pre-renders fragments that don't depend on per-request state
— see [Rendering](rendering#static-pre-render).
