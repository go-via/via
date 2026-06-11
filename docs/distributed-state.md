---
title: Distributed state
layout: default
parent: Learn
nav_order: 4
---

# Distributed state (the backplane)
{: .no_toc }

1. TOC
{:toc}

By default a Via app is single-process: `StateApp[T]` / `StateSess[T]` live in
the pod that serves the tab, and horizontal scaling needs
[sticky sessions](production#horizontal-scaling--affinity). The
**backplane** lifts that limit. Wire one in and shared state converges across
every pod *and* survives a restart — the typed API from
[Reactive state](reactive-state) does not change.

{: .warning }
The backplane is in **preview** on the way to 1.0. It is **eventually
consistent**: no global ordering across keys and no cross-key transactions.
With a backplane wired, cross-tab [`Broadcast`](production#cross-tab-broadcast)
fans out to every pod (ephemeral, best-effort); without one it is pod-local.
Treat single-process as the supported topology until the backplane ships.

## The two patterns

The backplane carries shared state in two complementary shapes. Pick by
**write churn**, not by feature.

| | `StateApp[T]` / `StateSess[T]` | `StateAppEvents[E, V]` |
|---|---|---|
| Model | current **value**, CAS-replaced | immutable **events**, folded into a value |
| Best for | low-churn config, flags, a profile | high-churn streams: a counter, a chat feed, a queue |
| Write | `Update(ctx, fn)` — CAS the store cell | `Append(ctx, ev)` — append-only, never CAS |
| Conflicts | retried under contention | none — the log orders appends |
| API change vs single-pod | **none** | new opt-in sibling type |

`StateApp`/`StateSess` keep their exact API — in a cluster the durable store
cell becomes the source of truth and each pod's copy is an L1 cache reconciled
to it. The sticky-session requirement for *state* goes away: a session's tabs
may land on different pods and still converge.

For hot keys, prefer `StateAppEvents`: appends never CAS, so the high-churn
retry-storm is gone by construction.

## Wiring one in

The backplane is a single interface — `Store + EventLog (+ optional Compactor)`
— so the topology is a one-line swap. `via.New()` with no backplane is
in-process-only (the honest default). Pass `WithBackplane` to cluster.

```go
import (
    "github.com/go-via/via"
    "github.com/go-via/via/vianats"
)

// Dev / single-pod: in-memory, no infra. Identical code path to a real backend.
app := via.New(via.WithBackplane(via.InMemory()))

// Production: durable, clustered, over NATS JetStream (+KV).
bp, err := vianats.JetStream(nc) // nc is your *nats.Conn; you own its lifecycle
if err != nil {
    log.Fatal(err)
}
app := via.New(via.WithBackplane(bp))
```

`InMemory()` and a real backend run the **same** projector/snapshot/fold code,
so what you test in-process is what runs clustered. Adapters live in separate
modules (`vianats`, …) so the core takes zero infrastructure dependencies.

## Event-sourced state in practice

Define your event `E`, give it a `Fold` method, and declare a
`StateAppEvents[E, V]` field. `V` is whatever the events fold into.

```go
type addItem struct{ Text string }

// Fold is a pure reducer: (accumulator, event) → new accumulator.
func (addItem) Fold(acc []string, ev addItem) []string {
    return append(append([]string(nil), acc...), ev.Text)
}

type Feed struct {
    Items via.StateAppEvents[addItem, []string]
}

func (p *Feed) Add(ctx *via.Ctx) {
    _, _ = p.Items.Append(ctx, addItem{Text: "hello"})
}

func (p *Feed) View(ctx *via.CtxR) h.H {
    return h.Div(h.ID("feed"), h.Text(strings.Join(p.Items.Read(ctx), ", ")))
}
```

- `Append(ctx, ev)` writes one immutable fact to the per-key log and returns its
  `Offset`. It never folds locally.
- `Read(ctx)` returns the current folded value `V`.
- `Text(ctx)` is `Read` rendered as a node — shorthand for the common case.

The wire key defaults to the lower-cased field name; set it (and share one log
across compositions) with the `via:"name"` tag, same as the other state shapes.

### The counter pattern

A monotonic shared counter is the canonical tiny example — a `StateAppEvents`
whose event is an empty tick and whose fold is `+1`. Each `Inc` appends an
immutable tick that never conflicts, so it sidesteps the CAS retry-storm a
`StateApp[int]` hits under churn:

```go
type tick struct{}

func (tick) Fold(acc int64, _ tick) int64 { return acc + 1 }

type Page struct {
    Hits via.StateAppEvents[tick, int64]
}

func (p *Page) Bump(ctx *via.Ctx) { p.Hits.Append(ctx, tick{}) }
func (p *Page) View(ctx *via.CtxR) h.H { return p.Hits.Text(ctx) }
```

## The determinism contract

Every pod independently tails the same event log and folds it in offset order.
Identical events folded the same way **converge** to the same value — that is the
whole guarantee. It holds only if `Fold` is **pure**:

- No clock, no RNG, no I/O, no globals, no goroutine-order dependence.
- Need a timestamp or a random id? Stamp it at `Append` time and carry it as a
  **field on the event**, so every pod folds the same value.
- Handle unknown event variants as a no-op — during a rolling deploy an older
  binary may fold events a newer one wrote.

`WithFoldVerify()` double-folds each record and compares, catching
non-determinism in dev/CI (it ~doubles fold CPU — run it on a canary, not the
fleet). A caught divergence is surfaced as `via.fold.divergence` and stops the
key from compacting.

## Side effects with `OnEvent`

A pure fold can't send an email or charge a card. Register a named, offset-tracked
consumer for that:

```go
p.Orders.OnEvent("ship", func(ctx context.Context, ev orderPlaced, off via.Offset) error {
    return shipper.Send(ctx, ev.OrderID)
})
```

- Delivery is **at-least-once**; the committed offset is persisted, so a restart
  resumes instead of replaying from genesis. Derive an idempotency key from the
  offset.
- A handler that returns an error is retried **head-of-line** with exponential
  backoff + jitter — the consumer does not advance past a failing event
  (surfaced as `via.consumer.error`). **By default it retries forever**: a
  permanently-failing handler never drops the side effect, but it pins the
  Compactor floor (so the log grows). The block is loud, not silent — each
  retry emits a `via.consumer.stuck` gauge (the attempt count) and a `WARN`
  fires once the attempts cross a threshold.

A poison record (a handler that can never succeed) can wedge the consumer and
pin the floor forever. Opt into skipping it with consumer options:

```go
p.Orders.OnEvent("ship",
    func(ctx context.Context, ev orderPlaced, off via.Offset) error {
        return shipper.Send(ctx, ev.OrderID)
    },
    via.WithMaxAttempts(10),                    // skip the record after 10 failed attempts
    via.WithRetryBackoff(50*time.Millisecond, time.Minute), // backoff bounds (default 10ms → 30s)
    via.WithDeadLetter(func(ctx context.Context, key string, off via.Offset, data []byte, cause error) error {
        return dlq.Publish(ctx, key, off, data, cause) // archive the record before skipping
    }),
)
```

- `WithMaxAttempts(n)` — after `n` consecutive handler errors on the same record
  the consumer treats it as **poison**: it emits `via.consumer.poisoned` and
  advances past the record, un-wedging the consumer and unpinning the floor. The
  **default is `0` = block forever** (above); skipping is strictly opt-in, so
  dropping a side effect is never the default.
- `WithRetryBackoff(base, max)` — exponential-backoff bounds (with jitter) between
  head-of-line retries. Defaults `10ms → 30s`.
- `WithDeadLetter(fn)` — invoked just before a record is poisoned. If it returns
  `nil` the consumer advances past the record; if it returns an error the consumer
  does **not** advance and keeps retrying, so a record you opted to dead-letter is
  never silently lost while the sink is unavailable.
- A **forward-incompatible** record (written by a newer binary) always blocks and
  is **never** poisoned, regardless of `WithMaxAttempts` — it is a rollback guard,
  not a bad record. An **undecodable** record is skipped via the decode path
  (`via.consumer.undecodable`), distinct from the poison path.

## Snapshots, compaction, cold start

Left alone, an event log grows forever. A projector periodically writes a
**snapshot** of its folded value (`WithSnapshotInterval`, default 64 folds); cold
start resumes from the latest snapshot and tails only the remainder, so startup
stays fast. A backend that implements the optional `Compactor` then drops the
log prefix the snapshot already covers — clamped so it never discards events a
lagging `OnEvent` consumer still needs.

A pod that falls behind a compacted prefix re-seeds from the snapshot
(`via.events.compaction_reseed`) or, if no bridging snapshot exists, halts rather
than diverge (`via.events.compaction_gap_halt`). A fast pod compacting the shared
log never truncates a slow peer.

## What it does NOT do

- **No cross-key transactions.** Each key converges independently; there is no
  atomic multi-key write.
- **No global ordering.** Events are totally ordered *per key*, not across keys.
- **Not strongly consistent.** Reads can lag the latest append by a reconcile
  hop. Don't gate a safety-critical invariant on it.
- **Broadcast is pod-local without a backplane.** `Broadcast` /
  `BroadcastSignals` reach only the calling pod's tabs unless `WithBackplane` is
  wired, in which case they ride the shared feed to every pod (ephemeral and
  best-effort — no replay, no convergence). The returned count is always just
  the calling pod's live-tab count.
- **Tabs and sessions are still in-memory.** The backplane converges *state*; a
  process restart still re-bootstraps live tabs — see
  [Restart and tab survivability](production#restart-and-tab-survivability).

## Operating it

The clustered path emits a full metrics family — `via.fold.*`, `via.events.*`,
`via.snapshot.*`, `via.consumer.*` — catalogued with alerting hints under
[Metrics](production#metrics), plus load-test guidance in
[State backplane under load](production#state-backplane-under-load). Watch
`via.fold.offset` for convergence and treat any `*_halt` counter as a stuck key.
