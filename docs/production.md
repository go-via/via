---
title: Production & ops
layout: default
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
| `via.sse.disconnect` | counter | `reason` |
| `via.sse.resync` | counter | |
| `via.sse.recover` | counter | `mode` |
| `via.ctx.live` | gauge | |
| `via.ctx.reap` | counter | `reason` |

State backplane (`StateAppEvents`, the clustered event-log path):

| Event | Kind | Labels | Meaning |
|---|---|---|---|
| `via.events.epoch_reset` | counter | `key` | stream offset-space reset (recreate/trim/restore); projector re-folds from genesis |
| `via.events.undecodable` | counter | `key` | poison record skipped (no decode path) — never wedges the key |
| `via.events.forward_incompatible` | counter | `key` | record written by a newer binary; projector HALTS (roll forward, not back) |
| `via.events.erased` | counter | `key` | record skipped because its data subject was crypto-shredded (GDPR erasure) |
| `via.events.compaction_reseed` | counter | `key` | projector fell behind the compacted prefix and recovered from the snapshot |
| `via.events.compaction_gap_halt` | counter | `key` | a compaction gap had no bridging snapshot; projector HALTS rather than diverge |
| `via.fold.offset` | gauge | `key` | applied offset after each fold (cross-pod convergence signal) |
| `via.fold.digest` | gauge | `key`, `offset` | fnv digest of the projection — compare across pods at the same offset to detect fold divergence (high-cardinality `offset`; relabel/drop outside investigations) |
| `via.fold.divergence` | counter | `key` | `WithFoldVerify` caught a non-deterministic fold; the key will not compact |
| `via.snapshot.unbridgeable` | counter | `key` | compacted-key snapshot can't be migrated to the current codec; projector HALTS |
| `via.snapshot.erasure_halt` | counter | `key` | a crypto-shred erasure invalidated a compacted (durable-genesis) snapshot; projector HALTS |
| `via.consumer.error` | counter | `name`, `key` | `OnEvent` handler returned an error; retried head-of-line (does not advance) |
| `via.consumer.undecodable` | counter | `name`, `key` | `OnEvent` skipped a poison record |
| `via.consumer.forward_incompatible` | counter | `name`, `key` | `OnEvent` blocked on a newer-binary record |
| `via.consumer.erased` | counter | `name`, `key` | `OnEvent` skipped a crypto-shredded record |

Alerting hints: a sustained nonzero `via.fold.divergence`, a persistent
`via.fold.digest` mismatch across pods at the same `key`+`offset`, or any
`via.events.compaction_gap_halt` / `via.snapshot.*_halt` is a halted projector —
investigate before the affected key's state can advance.

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
session). It does **not** survive a process restart, but the *connection*
recovers on its own:

- **Transient drop (server up, tab still known):** Datastar reconnects and
  via re-ships the current view on the reconnect (counted as
  `via.sse.resync`), so a client that drifted during the gap converges back
  to server truth. Signals are not re-seeded — live client-side signal
  state survives the blip. No user action needed.
- **Stale tab (deploy/restart, or TTL-swept):** the reconnecting `via_tab`
  is unknown to the process. via **re-bootstraps** the tab over the same
  stream: it recovers the route from the tab id, rebuilds path/query params
  from the `Referer`, runs `OnInit`/`OnConnect` on a fresh `*via.Ctx`, and
  pushes the full view plus a fresh signal seed (including a new `via_tab`).
  The user sees current — not stale — state without a reload; in-memory tab
  state starts fresh (`via.sse.recover` with `mode=rebootstrap`).
- **Unrecoverable (param route with no usable `Referer`, or the app is at
  `WithMaxContexts` capacity):** via pushes an explicit
  `window.location.reload()` so the tab recovers via a normal page load
  instead of freezing (`via.sse.recover` with `mode=reload`).
- A `via_tab` whose route prefix was never mounted is treated as forged and
  still 404s — junk traffic can't mint contexts.
- Sessions are also in-memory; logged-in users re-auth unless you back the
  session store with something durable. `OnInit` runs again on every
  re-bootstrap, so session-backed rehydration (below) applies there too.

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

### State backplane under load

`backplanebench_internal_test.go` (in-memory, multi-pod) and
`vianats/bench_test.go` (durable JetStream) load-test the clustered
`StateAppEvents` path. Run them on your hardware; the shape, not the absolute
numbers, is what matters:

- **Cross-pod convergence is fan-out.** Every pod independently decodes and
  folds every event, so AGGREGATE fold throughput scales with pod count (until
  it saturates cores) while the per-event INPUT rate a fixed cluster sustains
  drops inversely as you add pods. Size the cluster for the input rate you need,
  not the fold rate.
- **Per-fold cost is decode-bound** (envelope + payload JSON, plus the
  always-on `via.fold.digest` encode). `WithFoldVerify` roughly doubles it (it
  folds each record twice) — run it on a canary, not the whole fleet.
- **A real backend keeps the log off-heap**; the in-memory backplane holds the
  whole log in the Go heap, so its GC cost grows with total events. Production
  bounds the log with snapshot+compaction (`WithSnapshotInterval`), which is
  also what keeps cold-start fast.

A keeping-up consumer or a fast pod compacting the shared log will not truncate
a lagging peer: a projector that falls behind a compacted prefix re-seeds from
the snapshot (`via.events.compaction_reseed`), or halts rather than diverge
(`via.events.compaction_gap_halt`).
