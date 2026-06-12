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
    via.WithAddr(":8080"),
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

// app.Start() (or app.Run() to handle the bind error) wires SIGINT/SIGTERM
// to a graceful Shutdown — draining SSE streams, running OnDispose, and
// closing the backplane. A raw http.ListenAndServe(app) skips all of that
// and hard-kills in-flight streams on deploy.
if err := app.Run(); err != nil {
    log.Fatal(err)
}
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
- `WithMaxSessions(n)` — bound the live session map (a sibling of
  `WithMaxContexts`); a flood of fresh visitors can't grow it without limit
- `WithMaxUploadSize(n)` / `WithRequestTooLarge(h)` — see Security defaults
- Diagnostic knobs (`EXPERIMENTAL:`): `WithStrictDecode()` rejects lossy
  client-signal decodes instead of silently coercing; `WithVerboseErrors()`
  surfaces the real panic message to the client (dev only — leaks internals);
  dev-only composition checks (by-value child-clobber detection) are **on by
  default** and disabled in production with `WithoutDevChecks()`

## Health & readiness probes

Via serves `GET /livez`, `/healthz`, and `/readyz` by default — **before** the
session and middleware chain, so a frequent probe never mints a session or
logs a request:

- `/livez`, `/healthz` — `200` while the process is up (liveness).
- `/readyz` — `200` normally; flips to `503` the moment `Shutdown` begins
  draining, so a load balancer pulls the pod out of rotation *before* its
  in-flight SSE streams are torn down on deploy.

Disable all three with `WithoutHealthEndpoints()` (e.g. to serve your own at
those paths).

## Security defaults

- **CSRF:** every page mints a 256-bit `via_tab` id; action POSTs and SSE
  handshakes carry it as a signal. The id **is** the CSRF token — unknown
  ids 404. Action POSTs are also session-pinned (cookie mismatch → 403).
- **Sessions:** the `via_session` cookie is `HttpOnly`, `SameSite=Lax`,
  256-bit, and `Secure` by default; `WithInsecureCookies()` drops `Secure`
  for a local http:// dev loop. After auth-state changes call
  `sess.Rotate(ctx)` (session-fixation defence).
- **CSP:** `mw.CSP()` emits `default-src 'self'; script-src 'self'
  'nonce-X' 'unsafe-eval'; object-src 'none'; base-uri 'self';
  frame-ancestors 'self'`, with the per-request nonce reachable via
  `ctx.CSPNonce()`. The `'unsafe-eval'` keyword is required: Via's
  bundled Datastar runtime compiles every `data-*` expression and
  event handler with `Function()`, which CSP gates behind it. In this
  policy it only authorizes eval inside script the policy already
  admitted (same-origin files and nonce-carrying tags) — inline
  injection stays nonce-gated. A policy without `'unsafe-eval'` makes
  every click throw `EvalError`; clicks under the default policy are
  proven in a real browser.
  <!-- proof: TestBrowser_clickFiresUnderDefaultCSPPolicy -->
- **Body limits:** `WithMaxRequestBody(n)` (default 1 MiB) caps action POST
  and SSE-close bodies; `WithMaxUploadSize(n)` (default 32 MiB) caps
  `multipart/form-data` bodies. Either overflow returns 413; customise it
  with `WithRequestTooLarge(h)`.
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
| `via.session.mismatch` | counter | |
| `via.tab.unknown` | counter | `kind` |
| `via.action.recover` | counter | `mode` |

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
| `via.consumer.error` | counter | `name`, `key` | `OnEvent` handler returned an error; retried head-of-line with exponential backoff + jitter (does not advance). By default retries forever; `WithMaxAttempts(n>0)` opts the record into being skipped (poisoned) after `n` attempts |
| `via.consumer.stuck` | gauge | `name`, `key` | per-pod retry attempt count for the head-of-line record an `OnEvent` handler keeps failing; nonzero means the consumer is blocked and pinning the Compactor floor (loud even when blocking forever) |
| `via.consumer.poisoned` | counter | `name`, `key` | `OnEvent` skipped (dead-lettered or dropped) a record whose handler failed `WithMaxAttempts` times; only fires when skipping is opted into |
| `via.consumer.undecodable` | counter | `name`, `key` | `OnEvent` skipped a poison record |
| `via.consumer.forward_incompatible` | counter | `name`, `key` | `OnEvent` blocked on a newer-binary record |
| `via.consumer.erased` | counter | `name`, `key` | `OnEvent` skipped a crypto-shredded record |

Alerting hints: a sustained nonzero `via.fold.divergence`, a persistent
`via.fold.digest` mismatch across pods at the same `key`+`offset`, or any
`via.events.compaction_gap_halt` / `via.snapshot.*_halt` is a halted projector —
investigate before the affected key's state can advance.

{: .note }
**Non-contiguous offsets are normal.** A backend may number a key's events from a
sequence it shares across keys — e.g. a single NATS JetStream stream sequenced
globally across subjects, so each key's offsets skip the numbers other keys took
(3, 7, 12 rather than 1, 2, 3). The projector treats those gaps as benign and
folds every record; it only flags a lost prefix (`via.events.compaction_gap_halt`)
when a *Compacted* snapshot proves one. So `compaction_gap_halt` is a genuine
compaction problem, never just a side-effect of a multi-key/multi-subject backend.

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
a signal patch. Both return **this pod's** reached-tab count and deliver via
the existing patch queue + SSE drain — no extra wiring. With a
[backplane](distributed-state) wired (`WithBackplane`), the whole
`Broadcast*` family — `Broadcast`, `BroadcastNotify`, `BroadcastSignals`,
`BroadcastSignal[T]` — fans out to live tabs on **every pod**; without one it
stays pod-local. (The cross-pod path is `EXPERIMENTAL:` — see
[API stability](stability); the single-pod behavior is stable.)

## Horizontal scaling & affinity

A tab's `*via.Ctx` — its SSE stream and the action POSTs that drive it — is
in-memory on the pod that first served it. So the rule for scaling out is:
**affinity for transport, backplane for state.** Put the pods behind a
load balancer that pins each browser to the pod it first hit (sticky by
cookie), and wire a [backplane](distributed-state) so the *shared* state
converges across pods. Stickiness only governs per-tab transport; it is not a
state-sharing mechanism.

Plain round-robin breaks a live tab: its SSE stream and its action POSTs would
land on different pods, and the pod handling the action has no `*via.Ctx` to
render against. Two things the load balancer must get right:

- **Sticky cookie** — pin a browser to one pod after its first request.
- **Generous tunnel/read timeouts** — SSE streams are long-lived; default LB
  timeouts silently sever an idle-but-open stream.

A complete, runnable deployment ships in
[`internal/examples/chatcluster`](https://github.com/go-via/via/tree/main/internal/examples/chatcluster):
JetStream NATS + two app nodes behind an HAProxy that inserts a `VIA_LB`
cookie, with the affinity config (and the timeout settings) spelled out in
`haproxy.cfg`. `docker compose up` and it's live.

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
- **Retries exhausted (clean-close deploy, or a persistent failure):** the
  server-side recovery above can only run once a reconnect *reaches* the
  server. If Datastar's own retries are exhausted first — a graceful
  clean-close on deploy, or a session mismatch that keeps 403-ing — the tab
  would otherwise freeze silently. via injects a small client-side reconnect
  manager that shows a "Reconnecting…" banner while retrying and, on
  `retries-failed`, reloads the page (jittered, and bounded to a few attempts
  so a down server can't pin a reload loop) to re-bootstrap a fresh stream and
  session. Disable it with `WithoutSSEReconnect()` to supply your own.
- **Connection status for your own UI:** the same reconnect manager publishes a
  `data-via-connection` attribute on `<html>` — `online`, `connecting`, or
  `offline` — so you can style your own indicator in CSS without via's banner:

  ```css
  html[data-via-connection="offline"]   .app { opacity: .5; pointer-events: none }
  html[data-via-connection="connecting"] #net-dot { background: orange }
  ```

  (It's a DOM attribute, not a reactive signal — Datastar exposes no supported
  way to merge a signal from outside its own fetch lifecycle.)
- **Pending state per action** is already built in — there's no server
  round-trip needed to disable a button while its action is in flight. Add
  [`on.Indicator`](https://pkg.go.dev/github.com/go-via/via/on#Indicator)
  alongside the handler; Datastar flips the bound signal true for the request's
  duration so you can drive a spinner, `aria-busy`, or a disabled attribute off
  it client-side.
- Sessions are also in-memory; logged-in users re-auth unless you back the
  session store with something durable. `OnInit` runs again on every
  re-bootstrap, so session-backed rehydration (below) applies there too.

For session survivability, persist the `sess.Put`-stored payload (e.g. a JWT
or opaque token) to a database keyed by the `via_session` cookie value, and
rehydrate inside an `OnInit` hook.

### Rolling deploys and event versioning

`StateAppEvents` is roll-forward-only. During a rolling deploy two binaries read
the same log: the new one writes events at a new envelope version, and the old
one **halts** that key's projector rather than mis-fold an event it doesn't
understand (`via.events.forward_incompatible`). That's a deliberate guard, not a
bug — but it means you don't roll *back* past a new event version. To evolve an
event's shape safely, add a new version with `via.RegisterEvent` + an upcaster
so every binary can decode old and new records, deploy that everywhere, and only
then start writing the new shape. A projector that halts on
`via.events.forward_incompatible` clears once the lagging binary is rolled
forward.

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
