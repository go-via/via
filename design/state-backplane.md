# Scoping: the Via State Backplane (event-log model)

Status: DECIDED — scoped event-log approach (not a core/API rewrite)
Branch: `design/state-backplane`
Date: 2026-06-01
Related: reconnect-resilience issue #82 (transport-layer companion)

## Decision

Give Via cluster fan-out + restart survivability **additively**, without
rewriting the v0.4.0 typed API:

- **Value state stays as-is.** `StateApp[T]` / `StateSess[T]` keep their exact
  API (`Update(func(old)(new))`, no `Write`). Cross-pod: CAS a durable
  `Store`, then append a value-less `Change{key,rev}` to a shared change log;
  other pods tail it and re-pull (notify-and-pull, reusing the existing
  `broadcastRender → SyncNow → Read` path).
- **New opt-in sibling: `StateAppEvents[E, V]`.** For high-churn shared state (a
  shared counter, the chat/ticket queue) you append immutable events `E`; the
  value `V` is a deterministic **fold**. Appends never CAS, so the hot-key
  retry-storm is gone structurally; the fold is a method on `E`, so purity is
  the path of least resistance.
- **The backplane is one `Store + EventLog (+ optional Compactor) + Codec`
  interface**, implementable over NATS JetStream(+KV), Redis (Streams+Hash),
  Postgres (snapshot + append/seq table), or Kafka(+snapshot). Adapters live
  in separate modules so core takes zero infra deps.
- **`nil` backplane == today's exact in-process behavior**, byte-for-byte.

Explicitly NOT doing: a full event-sourcing rewrite of Via's core/API. Event
sourcing is a domain-persistence pattern; most Via state (`StateTab`,
`Signal`, and the bulk of `StateApp`) is value-shaped UI state that gains
nothing from a log and would only pay the ceremony. We apply the log exactly
where it pays — one new opt-in typed field — consistent with Via's "the
client/server split is a Go type" philosophy.

## Problem

Two deliberate non-goals in `docs/why-via.md`:

- **Not a cluster runtime.** A `StateApp.Update` on pod A re-renders only pod
  A's tabs; pods B/C never hear it. Horizontal scaling needs sticky sessions.
- **No restart survivability.** In-memory tab/session state; after a deploy a
  fresh process 404s the reconnect and the tab freezes (`docs/production.md`).

Both reduce to: make server-owned state durable + observable across N
processes, without prescribing the infrastructure.

## Design evolution (why an ordered log, not a doorbell)

The first architect pass landed on `Store(CAS)` + a **lossy, value-less Bus**
(notify-and-pull, Redis pub/sub qualifies). That left two unavoidable open
questions — dropped-notice *stranding* (a set-once value whose single notify
is lost strands sibling pods forever) and *reconnect rehydrate* — and a
*hot-key* CAS retry-storm on the flagship shared-log shape. Upgrading the Bus
to a **durable, ordered, offset-resumable EventLog** dissolves stranding/reconnect
(a pod resumes from its offset and cannot miss a record), and modelling the
hot shape as **append-events-and-fold** dissolves the retry-storm. That is the
event-log model below.

## Non-goals (v1)

- `StateTab[T]` / `Signal[T]` — tab-local / client-owned, stay in-process.
- A core/API rewrite — the feature is purely additive.
- Server-side compute / Lua / stored procedures.
- Cross-key transactions or global ordering.
- Cluster-wide `Broadcast` census (`BroadcastCluster` / `ClusterTabCount`
  deferred; local `Broadcast` is unchanged) — see #4 below.

## Current state model (grounding)

- `StateApp[T]` → `App.appStore` (in-memory `kvStore`, per-key-mutex `Update`).
  `Read` during View calls `trackRead(key)` → subscribes the tab.
- `StateApp.Update` → atomic RMW → `broadcastRender(ctx,nil,key)` → every
  in-process tab `subscribed(key)` gets `go c.SyncNow()` → re-render → `Read`.
- `StateSess[T]` → per-`session.data`, fan-out scoped to that session.
- Detection/binding: marker interfaces in `walker.go` (`isStateApp`…) →
  `bindScopeKeys` in `runtime.go`. `StateAppEvents` gets its own marker so the
  walker starts a per-key projector only for log keys.

## Recommended backplane interface

```go
// Package via — state backplane (additive; nil backplane == today's exact
// in-process behavior, byte-for-byte).
//
// Backplane is the ONE interface a backend author implements to make
// app/session-scoped reactive state survive restarts and span a cluster.
// It fuses THREE concerns the design treats as inseparable but keeps as
// distinct method sets so each maps cleanly onto every target backend:
//
//   - Store : a durable per-key CAS snapshot. Backs the EXISTING
//             value-shaped StateApp[T]/StateSess[T] (Update→CAS the Store,
//             then Append a value-less Change to the EventLog) AND holds
//             StateAppEvents fold-snapshots + side-effect consumer checkpoints.
//   - EventLog   : a durable, ORDERED, OFFSET-RESUMABLE append log. This is the
//             load-bearing half: because a pod always resumes from its last
//             committed Offset and CANNOT miss a record, the EventLog alone
//             closes stranding (#3) and reconnect-rehydrate (#7) with no
//             census and no re-broadcast.
//   - Compactor : an OPTIONAL capability (type-asserted, not in the core
//             set) — the one place the abstraction would leak if forced on
//             every backend. A backend that cannot truncate (e.g. a Kafka
//             event topic) simply does not implement it; the runtime then
//             runs snapshot-only (replay re-reads the un-truncated tail,
//             correct but less efficient). The gap is visible in the type
//             system, never hidden behind a silent no-op.
//
// The Codec is supplied to the runtime PER StateAppEvents[E] (see WithBackplane
// / the per-field codec), NOT by the backend — so swapping NATS for Postgres
// never touches your wire format, and your wire format is never hostage to a
// backend's native encoding. A backend author moves opaque bytes and assigns
// offsets; it never sees a Go event type or a generic.
//
//	app := via.New(via.WithBackplane(vianats.JetStream(nc)))
//
// Wire it once at boot; it is never swapped at runtime. A nil backplane is
// the documented default and means the in-process kvStore + an in-memory
// per-key log: existing v0.4.0 code compiles and behaves identically.
type Backplane interface {
	Store
	EventLog
	io.Closer // graceful drain on App.Shutdown; after Close, Append/Subscribe
	//          return ErrClosed and never block.
}

// Offset is an opaque, per-key, monotonically-INCREASING cursor. It is the
// resume primitive: a pod that committed Offset N resumes at "everything
// after N" and provably cannot miss a record (kills #3/#7).
//
// Treat it as OPAQUE. It is comparable and ordered WITHIN one key, but a
// Kafka offset, a JetStream stream sequence, a Redis stream id, and a
// Postgres bigserial are NOT interchangeable across backends and are NOT
// guaranteed gap-free (compaction and rolled-back txns create holes). The
// runtime only (a) orders, (b) persists "last applied" and resumes from it,
// (c) round-trips it as `from`. It NEVER does arithmetic on it. Offset(0)
// means "before the first record" — Subscribe(from:0) replays all.
type Offset uint64

// Rev is the Store cell's CAS version, DISTINCT from Offset (Store and EventLog
// have independent counters; a backend MAY alias them but need not).
type Rev uint64

// Record is one delivered EventLog entry. The runtime, not the backend,
// interprets Data via the per-key Codec — the backend only moves bytes and
// assigns Offset.
type Record struct {
	Key    string // the log subject (a StateAppEvents wire key, or the shared "changes" feed)
	Offset Offset
	Data   []byte // opaque; Codec.Decode turns it into an E (or a value-less Change)
}

// Store is the durable per-key CURRENT-VALUE snapshot with compare-and-swap.
//
// Backend mapping: JetStream KV (Update w/ revision), Redis Hash + WATCH/MULTI
// or a Lua CAS, Postgres `UPDATE … WHERE rev=$1`, Kafka compacted topic keyed
// by Key (rev = the topic offset of the last write to that key).
type Store interface {
	// LoadSnapshot returns the stored bytes for key and its revision, or
	// ok=false if the key was never written. Bytes are opaque to the Store.
	LoadSnapshot(ctx context.Context, key string) (data []byte, rev Rev, ok bool, err error)

	// CAS stores data for key IFF the current revision == expectedRev (Rev(0)
	// means "must not exist yet"). Returns the NEW revision, or ErrCASConflict
	// if the current rev moved — the caller reloads and retries. This is the
	// ONE place value-shaped StateApp tolerates a retry; StateAppEvents.Append
	// never CASes, which is exactly why it beats value-shaped state under
	// churn (open #2). Used for: value-StateApp survivability, StateAppEvents
	// fold-snapshots, and side-effect-consumer offset checkpoints.
	CAS(ctx context.Context, key string, expectedRev Rev, data []byte) (newRev Rev, err error)
}

// EventLog is the durable, ordered, offset-resumable append log that REPLACES the
// originally-proposed lossy fire-and-forget Bus.
//
// GUARANTEES (load-bearing — the whole correctness story rests here):
//   - PER-KEY TOTAL ORDER. Records on one key are delivered to every
//     Subscribe in the exact order they were committed.
//   - DURABILITY. Append returns only after the record is durably committed
//     and assigned its Offset; that Offset is observed by every future
//     Subscribe from an earlier offset.
//   - AT-LEAST-ONCE, GAP-FREE, RESUMABLE delivery. Subscribe(from:K) yields
//     every committed record with Offset>K, in order, no gaps. Redelivery
//     after reconnect is possible (hence at-least-once); the runtime dedupes
//     by Offset (effectively-once consumers — #1).
//
// EXPLICIT NON-GUARANTEES: NO cross-key ordering (distinct StateAppEvents fields
// are independent aggregates). NO exactly-once delivery. NO wall-clock order.
// Plain Append is NOT idempotent unless you use AppendIf.
//
// Backend mapping: JetStream stream/durable consumer w/ ack floor, Redis
// Streams + XREAD BLOCK + last-id cursor, Postgres append table w/ monotonic
// seq + LISTEN (or seq-poll fallback), Kafka topic partition + offset.
type EventLog interface {
	// Append commits one opaque record to key's stream and returns its
	// assigned Offset. Plain append: it NEVER conflicts — that is the whole
	// point of the EventLog path (#2). Used by StateAppEvents.Append AND by the
	// value-shaped path's value-less Change{key,rev} on the shared "changes"
	// feed.
	Append(ctx context.Context, key string, record []byte) (Offset, error)

	// AppendIf is plain Append's optimistic sibling: commit only if key's
	// current head Offset == expectedHead, else ErrLogConflict. The ONE
	// conditional primitive every backend must map (JetStream
	// expected-last-seq, Redis XADD+WATCH/Lua, Postgres INSERT…WHERE seq=$1,
	// Kafka idempotent producer). Backs the rare first-writer-wins case
	// (claim-ticket, resolve-if-unresolved). Most call sites use Append.
	AppendIf(ctx context.Context, key string, expectedHead Offset, record []byte) (Offset, error)

	// Subscribe streams records for key with Offset > from (so a pod passes
	// its last-APPLIED offset and resumes exactly after it), then live-tails.
	// The channel closes when ctx is cancelled or on unrecoverable error.
	// MUST deliver in Offset order with no gaps. THIS is the resumability
	// that retires #3/#7 — no census, no re-broadcast.
	Subscribe(ctx context.Context, key string, from Offset) (<-chan Record, error)

	// Head returns the current highest committed Offset for key (cold-start
	// replay bounds, AppendIf preflight, diagnostics). Offset(0) if empty.
	Head(ctx context.Context, key string) (Offset, error)
}

// Compactor is OPTIONAL. The runtime type-asserts the Backplane to it and
// only calls Compact AFTER a snapshot covering [0, beforeOffset) is durably
// written to the Store (snapshot-FIRST, compact-SECOND is mandatory: a crash
// between them costs only a few re-replayed events, never a lost one).
// Backends without native compaction omit it and run snapshot-only.
//
//   - JetStream: purge-up-to-sequence / per-subject limits.
//   - Redis Streams: XTRIM MINID.
//   - Postgres: DELETE FROM events WHERE key=$1 AND offset<=$2, in the
//     snapshot's txn.
//   - Kafka: native compaction keeps latest-per-key which is WRONG for an
//     event log, so Kafka compacts the SNAPSHOT topic and delete-records the
//     EVENT topic up to beforeOffset — OR declines Compactor and runs
//     snapshot-only.
type Compactor interface {
	Compact(ctx context.Context, key string, beforeOffset Offset) error
}

// Codec serializes events and snapshots. Supplied PER StateAppEvents[E] (the
// runtime binds one to E at Mount), NOT implemented by the backend. Default
// is a self-describing JSON codec with a version envelope (see E-versioning).
// A backend author never writes a Codec.
type Codec interface {
	// Encode wraps v in an Envelope{TypeTag, Version, Payload} at the CURRENT
	// version and returns the wire bytes. We never rewrite history, so old
	// records keep their old version bytes on disk.
	Encode(v any) ([]byte, error)

	// Decode reads the envelope, runs the registered upcaster chain from the
	// stored Version up to current, then unmarshals into a fresh E (or
	// Change). On NO viable upcaster path it returns ErrUndecodable; the fold
	// then DROPS the record (treats it as a no-op event) and emits a
	// via.events.undecodable metric — a poison/forward-incompatible record must
	// NEVER panic the pod and wedge every peer replaying the key.
	Decode(data []byte) (any, error)
}

var (
	ErrCASConflict  = errors.New("via: store CAS revision conflict")
	ErrLogConflict  = errors.New("via: log head moved since expected offset")
	ErrUndecodable  = errors.New("via: no upcaster path to current event version")
	ErrClosed       = errors.New("via: backplane closed")
)

// WithBackplane sets App.backplane. nil (the default) == in-process
// today's-behavior. Additive Option in the config.go family.
func WithBackplane(b Backplane) Option
```

## Recommended user API — `StateAppEvents[E, V]`

```go
// Package via — StateAppEvents[E], an event-log-backed sibling of StateApp[T].
//
// Where StateApp[T] holds ONE current value mutated by Update(func(old)(new))
// — which, in a cluster, becomes a compare-and-swap on a single key and
// retry-storms under churn (open #2: a shared counter, the chat/ticket QUEUE)
// — StateAppEvents[E] holds an append-only sequence of immutable events E whose
// current value is a deterministic FOLD. You never write the value; you
// APPEND a fact and the fold derives it. Concurrent Appends from N pods never
// conflict (the EventLog orders them; there is no CAS to lose), the payload is
// O(1) (one event, not the window), and delete is a tombstone event + Compact
// (#5). Reach for it on HIGH-CHURN shared state; keep StateApp[T] for the
// low-churn current-value case.
//
//	type Room struct {
//	    Events via.StateAppEvents[ChatEvent, []Message] // events:ChatEvent, value:[]Message
//	}
//
// E is the immutable on-the-wire fact, versioned FOREVER. V is the projected
// value your View reads, free to change every deploy (it is never persisted
// except as a disposable snapshot — see E-versioning). They are SEPARATE type
// params on purpose: a counter folds Tick→int; a room folds ChatEvent→[]Message.
//
// The fold is a METHOD on E (the EventReducer constraint), NOT a func field
// and NOT a runtime-registered closure. This is the single most important
// ergonomic decision: a func field or closure can capture a *Ctx, a clock, an
// RNG, a package map — every impurity that breaks replay determinism (two
// pods, same offset, DIFFERENT value → silently divergent HTML, no error).
// A method on E can see ONLY its receiver (the event) and its arg (the
// accumulator); impurity requires reaching for a package-global, which is a
// visible code smell in review, not an invisible closure capture. And it
// binds at COMPILE time: if E satisfies the constraint the field compiles,
// else via.Mount won't build — there is no Mount-time "register your reducer"
// call to forget and no nil-reducer panic path.
//
// The zero value is usable: declare the field, no init. With a nil backplane
// it degrades to an in-process append-only log with identical semantics —
// today's single-pod behavior, no API difference. Adopt the type single-pod
// and gain clustering later by adding one WithBackplane option, no call-site
// change.
type StateAppEvents[E EventReducer[E, V], V any] struct {
	wireKey string
	app     *App // bound at Mount; nil before
}

// EventReducer constrains E to be its own reducer. The fold lives on the TYPE.
type EventReducer[E any, V any] interface {
	// Zero returns the fold seed: the projected value of an EMPTY log.
	// Determinism rule #1: Zero() is a constant — it must not read instance
	// state, a clock, or a global.
	Zero() V

	// Fold returns the new accumulator after applying THIS event to acc.
	// Determinism rule #2 (LOAD-BEARING): Fold is a PURE function of
	// (acc, receiver). No clock, no RNG, no I/O, no package globals, no map
	// iteration order leaking into the result. Two pods replaying the same
	// offset range MUST converge to the same V or the cluster desyncs. The
	// signature gives Fold nothing else to read, so purity is the path of
	// least resistance. A wall-clock or random input must be carried AS A
	// FIELD on E, stamped at Append, never sampled inside Fold. Fold MUST
	// have a default branch that returns acc unchanged for an unknown event
	// variant, so a pod running old code that tails a new-variant event folds
	// it as a no-op instead of corrupting the projection during a rolling
	// deploy.
	Fold(acc V, ev E) V
}

func (l *StateAppEvents[E, V]) bindWireKey(k string) { l.wireKey = k }
func (l *StateAppEvents[E, V]) bindApp(a *App)        { l.app = a }
func (*StateAppEvents[E, V]) isStateAppEvents()          {} // distinct marker → walker
//                                                       starts the projector only for log keys

// Key returns the wire key (lowercase field name unless overridden by `via:` tag).
func (l *StateAppEvents[E, V]) Key() string { return l.wireKey }

// Read returns the current PROJECTED value: the fold of every event in the
// EventLog up to this pod's locally-applied offset, seeded by E.Zero(). A Read
// during View execution subscribes the ctx via ctx.trackRead(wireKey) —
// IDENTICAL to StateApp.Read — so a later Append anywhere in the cluster fans
// a re-render out to this tab through the SAME broadcastRender→SyncNow→Read
// path StateApp uses. No manual Subscribe in View.
//
// Read is O(1): the runtime keeps a cached projection per key, updated
// incrementally as EventLog records arrive (one tailer goroutine per key folds
// each Record forward). Read NEVER re-folds from genesis. Accepts *Ctx or *CtxR.
func (l *StateAppEvents[E, V]) Read(rc readCtx) V {
	var ev E
	zero := ev.Zero()
	if rc == nil || l.app == nil {
		return zero
	}
	ctx := rc.rctx()
	if ctx == nil {
		return zero
	}
	ctx.trackRead(l.wireKey) // identical subscription hook to StateApp.Read
	v, ok := l.app.logProjection(l.wireKey)
	if !ok {
		return zero
	}
	return v.(V)
}

// Append commits ONE immutable event to the EventLog. Unlike StateApp.Update there
// is NO read-modify-write and NO old value: you describe WHAT HAPPENED, the
// fold derives the new value. Concurrent Appends from different pods never
// conflict (the EventLog orders them; there is no CAS to lose), so the chat/ticket
// hot key cannot retry-storm (#2).
//
// On success the event is durably appended at the returned offset; this pod
// folds it into its cached projection, marks the page dirty (writer re-renders
// via autoflush), and every other pod tailing the EventLog from its offset
// fold-forwards and broadcastRenders to its subscribed tabs. The returned
// offset is useful for read-your-write and effect dedup.
//
// There is intentionally NO Write and NO Update(func(old)(new)) — a blind set
// of an event log is a category error, and the RMW race the type exists to
// remove is not even expressible. To reset, append a tombstone variant and
// Compact.
//
// Panics on nil ctx, EXACTLY like StateApp.Update: without a ctx no re-render
// can fan out, so silently succeeding would desync server state from every tab.
func (l *StateAppEvents[E, V]) Append(ctx *Ctx, ev E) (offset uint64, err error) {
	if ctx == nil {
		panic("via: StateAppEvents.Append called with nil *Ctx")
	}
	if l.app == nil {
		return 0, nil // nil backplane pre-Mount: parity with StateApp's no-op guard
	}
	off, err := l.app.appendEvent(l.wireKey, ev) // Codec.Encode + EventLog.Append + local fold
	if err != nil {
		return 0, err
	}
	ctx.markStateDirty()
	l.app.broadcastRender(ctx, nil, l.wireKey) // reuse the confirmed render path
	return off, nil
}

// AppendIf is the rare optimistic, first-writer-wins sibling: commit ev only
// if the key's head offset is still expectedHead, else ErrLogConflict and the
// caller re-reads and decides. Get expectedHead from ReadAt (the only way to
// obtain a head — you cannot fabricate a stale-but-plausible number). Use it
// for claim-ticket-if-unclaimed / resolve-if-unresolved. 99% of call sites use
// plain Append.
func (l *StateAppEvents[E, V]) AppendIf(ctx *Ctx, expectedHead uint64, ev E) (offset uint64, err error)

// ReadAt returns the projected value AND the head offset it reflects, for the
// read-then-conditionally-append (AppendIf) flow. Subscribes like Read.
func (l *StateAppEvents[E, V]) ReadAt(rc readCtx) (V, uint64)

// Text returns the projected value as a text node. Sibling of StateApp.Text.
func (l *StateAppEvents[E, V]) Text(rc readCtx) h.H { return h.Textf("%v", l.Read(rc)) }

// OnEvent registers a NAMED, offset-tracked, side-effecting consumer over the
// EventLog (send email on ticket-closed, charge a card). Side effects do NOT live
// in Fold (Fold must stay pure); they live here. The runtime drives a separate
// tailer whose committed offset is persisted in the Store
// ("consumer:<name>:"+wireKey) and advanced ONLY after handler returns nil.
// A restart resumes from the committed offset; an event whose effect already
// ran is skipped. Effectively-once = at-least-once delivery + offset-gated
// idempotent commit: a handler that must be exactly-once carries an
// idempotency key derived from off (e.g. Stripe idempotency-key = key+":"+off).
func (l *StateAppEvents[E, V]) OnEvent(name string, fn func(ctx *Ctx, ev E, off uint64) error)

// ============================================================================
// CALL SITE 1 — live shared counter (compare against StateApp[int])
// ============================================================================
// StateApp[int] today: Hits.Update(ctx, func(n int)(int,error){return n+1,nil})
//   → cluster CAS on key "hits"; N pods clicking → CAS contention, retries.
// StateAppEvents: append a Tick. Appends are ordered, never conflict.

type Tick struct{} // "one increment happened". Empty on purpose.

func (Tick) Zero() int              { return 0 }     // empty log → 0
func (Tick) Fold(n int, _ Tick) int { return n + 1 } // each Tick adds 1

type Counter struct {
	Hits via.StateAppEvents[Tick, int]
}

func (c *Counter) Inc(ctx *via.Ctx) { _, _ = c.Hits.Append(ctx, Tick{}) }

func (c *Counter) View(ctx *via.CtxR) h.H { // Read subscribes; any pod's Inc re-renders here
	return h.Div(
		h.H1(h.Textf("%d", c.Hits.Read(ctx))),
		h.Button(h.Text("+1"), on.Click(c.Inc)),
	)
}

// ============================================================================
// CALL SITE 2 — the chat / ticket QUEUE, verbatim replacement of the example
// ============================================================================
// internal/examples/chat/main.go TODAY: Log via.StateAppSlice[Message]; Send
// does Log.Op(ctx).Append(m) THEN a SECOND Update to trim to recentWindow —
// two store mutations, the whole slice re-stored each time, CAS-storm across
// pods. AS A LOG: the window cap is a PURE part of the fold — no second
// mutation, no trim-Update, no whole-slice CAS. ChatEvent is a TAGGED STRUCT
// (Kind tag) not a sealed interface, so a pod on old code decodes a new Kind
// and the default branch folds it as a no-op (forward-compat across deploys).

type Message struct{ From, Body string }

type ChatEvent struct {
	Kind string // "say" | "clear"  (a tombstone variant for #5/delete)
	Msg  Message
}

const recentWindow = 50

func (ChatEvent) Zero() []Message { return nil }

func (e ChatEvent) Fold(log []Message, _ ChatEvent) []Message {
	switch e.Kind {
	case "clear":
		return nil // tombstone: queue drained / ticket closed
	case "say":
		log = append(log, e.Msg)
		if len(log) > recentWindow { // the window cap IS the fold — pure, deterministic
			log = log[len(log)-recentWindow:]
		}
		return log
	default:
		return log // unknown future variant from a newer deploy → no-op
	}
}

type Room struct {
	Events   via.StateAppEvents[ChatEvent, []Message] // was StateAppSlice[Message]
	Name  via.SignalStr `via:"name,init=Anon"`
	Draft via.SignalStr `via:"draft"`
}

func (r *Room) Send(ctx *via.Ctx) {
	body := strings.TrimSpace(r.Draft.Read(ctx))
	if body == "" {
		return
	}
	name := strings.TrimSpace(r.Name.Read(ctx))
	if name == "" {
		name = "Anon"
	}
	// ONE append. No trim-Update, no whole-slice re-store. Trim is in Fold.
	_, _ = r.Events.Append(ctx, ChatEvent{Kind: "say", Msg: Message{From: name, Body: body}})
	r.Draft.Write(ctx, "")
}

func (r *Room) View(ctx *via.CtxR) h.H { // identical to the existing example
	return h.Main(h.Class("container"),
		h.H1(h.Text("Via Chat")),
		h.Article(h.Each(r.Events.Read(ctx), func(m Message) h.H {
			return h.P(h.Strong(h.Text(m.From+": ")), h.Text(m.Body))
		})),
		h.Form(
			h.Input(h.Type("text"), r.Name.Bind()),
			h.Input(h.Type("text"), r.Draft.Bind(), on.Key("Enter", r.Send)),
			h.Button(h.Type("button"), h.Text("Send"), on.Click(r.Send)),
		),
	)
}
```

## Why these picks

Picked the api lens as the SPINE (method-on-E fold, two type params E+V, plain Append) because fold purity is the entire correctness model and method-on-E is the ONLY option of the four that makes impurity a visible review smell instead of an invisible closure capture (rejected adapters' `Project(closure)` and versioning's `OnFold(seed,reduce)` — both let you capture a clock; rejected correctness' composition-level StateFolder — can close over struct fields). Compile-time binding via the EventReducer constraint also kills the "forgot to register the reducer" nil path that adapters/versioning/correctness all carry.

Grounded in code: StateApp/StateSess detect via marker iface in walker.go (isStateApp/isStateSess → roleStateApp); bind via scopeBinder.bindWireKey in runtime.go:bindScopeKeys. Gave StateAppEvents its OWN marker isStateAppEvents() + a new roleStateAppEvents (NOT versioning's "reuse isStateApp marker") so the walker starts the per-key projector goroutine only for log keys and scopeSlot carries a kind flag — minimal additive wiring, no conflation of append-mode detection. Read reuses ctx.trackRead/subscribed verbatim (ctx.go); Append reuses broadcastRender→go SyncNow→Read (broadcast.go, confirmed path) and panic-on-nil-ctx parity with StateApp.Update (stateapp.go:60).

Kept V as the 2nd type param despite the uglier `StateAppEvents[ChatEvent,[]Message]` decl — collapsing it (adapters/versioning erase View to `any`) loses compile-time projection safety and the counter→int / chat→[]Message demo is the core pitch. The decl cost is paid once per field.

Borrowed from the other lenses: adapters' OPTIONAL Compactor (type-asserted, backend declines) — honest about the Kafka leak vs api's core EventLog.Compact; adapters' snapshot=pure-cache / invalidate-on-codec-hash → re-fold (evolving V FREE) — but only for UNCOMPACTED keys; T2-GO-4 later made the snapshot durable genesis once a key compacts (typed `Codec[V]` + seeded migration + retained-event floor there, see #6). versioning's drop-on-undecodable (ErrUndecodable → fold no-ops, never panics the pod) + stable TypeTag decoupled from Go type name — load-bearing for a poison event not wedging the cluster. api/correctness' shared "changes" subject for value-shaped fan-out + per-key subjects for logs.

## How the open questions resolve

### #1 side-effect dup (effectively-once) — RESOLVED

l.OnEvent(name, fn(ctx,ev,off) error): named offset-tracked consumer, SEPARATE tailer, committed offset persisted in Store key consumer:<name>:<wireKey>, advanced ONLY after handler returns nil. Restart resumes from committed offset → events with off<=committed skipped. Effectively-once = at-least-once + offset-gated idempotent commit; exactly-once handlers carry idempotency key = wireKey+":"+off. Effects live OUT of Fold (Fold pure) — structural, not discipline.

### #2 hot key (CAS retry-storm) — RESOLVED

Structural: StateAppEvents.Append → EventLog.Append (plain, no expected-offset, never conflicts). N concurrent appends from N pods all land, totally ordered by the EventLog's per-key monotonic counter. No shared value cell to CAS → nothing to retry-storm. Throughput bounded by per-key append rate, not CAS amplification.

### #3 stranding — RESOLVED

Durable+ordered+resumable EventLog. Pod resumes Subscribe(key, from=lastAppliedOffset); backend replays the gap then live-tails. Next Read folds the replayed gap first → cannot be stranded with a stale projection. No census, no re-broadcast needed.

### #4 Broadcast/BroadcastSignals cross-pod count — RESOLVED

Broadcast/BroadcastSignals STAY LOCAL — int return documented as live-tab count on THIS pod (broadcast.go already iterates snapshotContexts; no signature break, v0.4.0 safe). Cluster announce: add app.BroadcastCluster(script) that Appends a Broadcast control-event to the shared changes feed; every pod tails it, runs its local Broadcast; returns (queued bool, err), NOT a synchronous global count (a sync global count needs a blocking scatter-gather census). Exact count only via app.ClusterTabCount(ctx) — explicitly best-effort, RTT-bounded, from heartbeat keys. CUT both cluster helpers from v1 (orthogonal to StateAppEvents; local Broadcast already works).

### #5 delete — RESOLVED

Tombstone EVENT (ChatEvent{Kind:"clear"} or Delete{ID} whose Fold removes that ID), ordered so every pod converges on the deletion; storage reclaimed later by snapshot-then-Compact. No out-of-band purge that could race the fold. GDPR right-to-erasure: crypto-shred (store PII encrypted, drop the key, tombstone+snapshot renders ciphertext unreadable, Compact removes it).

### #6 event-schema versioning (the main new cost) — RESOLVED

Mandatory versioned self-describing envelope {t:TypeTag, v:version, d:body}; TypeTag is a STABLE user-declared string (decoupled from Go type name → rename free). DECODE-only upcaster chain (RegisterEvent[E](v, Upcaster{From,To,Fn})) runs stored-version→current BEFORE unmarshal, so Fold only ever sees current-shape E — version logic at the codec boundary, never smeared through Fold. Additive-first discipline (godoc) covers the 90% with zero upcaster code. New variants free (tagged struct, Fold default→identity). Drop-on-undecodable: ErrUndecodable → fold no-ops + via.events.undecodable metric, NEVER panics the pod. V (snapshot) is a pure disposable cache ONLY for UNCOMPACTED keys → a codec-hash mismatch discards + re-folds from offset 0, so evolving V is free (the common case; only E needs upcasters). **T2-GO-4 caveat:** once a key COMPACTS, the deleted prefix `[0,beforeOffset)` is unrecoverable, so the snapshot becomes durable GENESIS state — the snapshot codec is therefore typed `Codec[V]` + version-tagged (the checkpoint is `Checkpoint{epoch, coveredOffset, codecHash, vbytes}`), and a codec-hash mismatch on a compacted key runs a SEEDED migration (decode old V → seed, fold the retained tail on top, rewrite the checkpoint) and MUST NOT discard (discarding would silently truncate to the uncompacted tail). A retained-event floor — `Compact(before)` clamped below the 2nd-newest snapshot's `coveredOffset` (and below every consumer checkpoint) minus a safety window — always keeps ≥1 re-foldable snapshot generation on disk (~2× steady-state disk, accepted). A fold-MEANING change (≠ V wire shape) bumps `epoch` and re-folds from the 2nd-newest snapshot; an unbridgeable bump → `ErrEpochUnbridgeable`, projector halts (roll-forward-only). `WithFoldVerify` is mandatory before a key may compact (compaction makes an impure-fold corruption permanent — the evidence is deleted). Compaction retires ancient upcasters once no live record predates them.

### #7 reconnect-rehydrate — RESOLVED

Same mechanism as #3. Cold start per key: LoadSnapshot→(V0,coveredOffset) (or Zero(),0); Subscribe(from=coveredOffset) folds the tail to live head. A returning SSE tab's first Read reflects the pod's offset-current projection; if the POD reconnected, Subscribe-from-offset rehydrated it first. The tab never sees a gap. First View on a fresh pod blocks only until the projector catches the tail (bounded by snapshot cadence, not log age).


## Per-dimension, 4 architects by lens

**User API shape**
- api → `StateAppEvents[E EventReducer[E,V], V any]`. Two type params; fold is METHOD on E (compile-bound). Read=method `Read(rc) V`. Most type-safe, but `[E,V]` double-param ugly at field decl (`StateAppEvents[ChatEvent,[]Message]`).
- correctness → `StateAppEvents[E]` field, V inferred from a `Fold(P,E)P` method on a SEPARATE composition-level `StateFolder` iface, checked at Mount. Clean field decl; fold lives off the event type → can capture (impurity hole vs api).
- adapters → `StateAppEvents[E]` field + `via.Project(&l, foldClosure)` in OnInit + free fn `via.ReadLog[V](&l, ctx)`. Closure fold = MOST impurity-prone; ReadLog free-fn is ugly call site, breaks "sibling of StateApp" feel.
- versioning → `StateAppEvents[E]` + `via.OnFold(ctx,&l,seed,reduce)` in OnInit; `View` type erased to `any`, Read returns `View`. Cleanest E-only decl; explicit View-vs-E split named in types. Closure fold (impure-prone) but OnFold-in-OnInit idempotent guard.

**Fold location** (THE divergence)
- api → method on E. Pure by construction (receiver+acc only inputs). WINNER on safety.
- correctness → method on composition (StateFolder). Mount-checked but can close over struct fields.
- adapters/versioning → runtime closure via Project/OnFold. Flexible, impurity invisible.
- Consensus: ALL 4 say fold MUST be pure+deterministic, none can compiler-enforce; only api makes impurity a visible code-smell vs invisible closure capture.

**Append semantics**
- ALL 4: plain `Append(ctx, e)` unconditional, no CAS → kills #2 structurally. Panic on nil ctx (parity w/ StateApp.Update). All return offset (api/correctness/adapters) except versioning returns only err.
- AppendIf (optimistic expected-offset, claim-ticket): api keeps + `ReadAt`→(V,head); correctness keeps `AppendExpecting`+sugar `AppendIfStable` retry-loop; adapters CUTS to v1; versioning omits. Backend prim split: api→`EventLog.AppendIf`; correctness/versioning→`EventLog.AppendExpecting`/CAS-on-Store.

**Snapshot/compaction**
- ALL: snapshot=Encode(V) to Store keyed by coveredOffset; snapshot-DURABLE-FIRST then Compact (mandatory ordering). Cold-start = LoadSnapshot→Subscribe(from:coveredOffset)→fold tail. Bounded replay.
- Compact placement: api/correctness/versioning → `EventLog.Compact` (core). adapters → SPLIT into OPTIONAL `Compactor` iface, type-asserted, backend may decline (Kafka event-log can't native-compact). Best leak-honesty.
- Cadence: WithLogSnapshotEvery(N) default 512–1000. correctness adds T-seconds. versioning CUTS snapshot from v1 (full-replay first).

**Coexistence (value-StateApp + log share backplane)**
- ALL: ONE Backplane instance, value-shaped = CAS Store + value-LESS `Change{key,rev}` on EventLog → notify-and-pull (reuse broadcastRender→SyncNow→Read, confirmed path). EventLog-shaped = full events folded.
- Subject layout DIVERGES: api/correctness → value uses ONE SHARED change subject (`changes`/`app:change`), log uses PER-KEY subject. versioning → ONE stream per key for BOTH, discriminate by envelope TypeTag (`via.change` vs user tag). adapters → per-key for both, derived names `via.events.<k>`/`via.change.<k>`.
- Shared-change-subject (api/correctness) = fewer subscriptions, decouples hot-log compaction from cold-value notifies. WINNER.

**E-versioning (#6, the new cost)**
- ALL: mandatory versioned ENVELOPE `{t/typeTag, v, d}`; upcaster chain at DECODE only (never rewrite EventLog); additive-first discipline; tagged-struct (NOT sealed iface) so unknown variants decode; Fold `default:`→identity for forward-compat.
- api: most complete — additive-first→upcaster→compaction-retires-old-upcasters ladder; `RegisterEvent[E](v, Upcaster{From,To,Fn})`.
- correctness: snapshot-epoch escape hatch; schema-registry Codec (Avro) as prod swap.
- adapters: snapshot=pure CACHE, invalidate-on-codec-hash-mismatch → re-fold (no snapshot upcasters EVER) — best snapshot-versioning insight.
- versioning (deepest, as expected): stable user-declared TypeTag decoupled from Go type name (rename free); `EventType()` method or `event=` tag; **drop-on-undecodable** (Decode→ErrUndecodable→fold treats as no-op, NEVER panics pod) — load-bearing, a poison event can't wedge the cluster; forward-incompat guard (newer-version event halts that key's projector vs mis-fold).


## Remaining risks

- FOLD-DETERMINISM DRIFT is the single most likely production incident and is UNENFORCEABLE at compile time. Correctness rests on every pod folding the same offset range to the same V. Method-on-E makes impurity awkward (no reachable clock/ctx/closure) but a determined user can read a package-global or time.Now() inside Fold → two pods silently render divergent HTML, no error, surfacing only under cluster+restart+deploy-skew (exactly the conditions this feature is for, hardest to test). Mitigate: (a) strongest-terms godoc on Fold; (b) dev-mode WithFoldVerify that re-folds from last snapshot per Append and panics on mismatch; (c) ship-later go-vet analyzer flagging time/rand/global reads in any Fold method. Residual risk real.
- FOLD-MEANING CHANGE ACROSS DEPLOYS: if v2 Fold re-folds the SAME old events to a different V than the v1 snapshot wrote, pod-A(v1 snapshot) and pod-B(v2 cold-replay) diverge. A v2 that changes projection meaning MUST snapshot+epoch; the runtime cannot force it. Document bluntly; snapshot-as-pure-cache (invalidate on codec hash) limits but does not eliminate.
- COMPACT-BEFORE-SNAPSHOT-DURABLE in a third-party backend permanently loses events (Compactor turns a recoverable read bug into data loss). Mitigate: Compactor godoc demands snapshot-durability-first; ship a backend conformance test suite that asserts the ordering and gap-free resumable delivery.
- ENVELOPE-OMISSION IS UNRECOVERABLE: an event appended without a version envelope can never be evolved (records immortal). The envelope MUST be in the very first byte ever appended — default JSON codec enforces it; a type with no registered TypeTag fails fast at Mount, not in prod replay.
- FORWARD-INCOMPAT ROLLBACK: a rolled-back deploy reading events written at a newer version. Decode whose version exceeds the binary's max returns a hard error that STOPS that key's projector rather than silently mis-folding — roll forward, not back, past an E schema bump. Operational constraint, must be documented.

## Phased plan

- PHASE 0 — binding seam (additive, no behavior change). Add isStateAppEvents() marker + roleStateAppEvents in walker.go classifyField; scopeSlot gains a kind flag (value vs log); bindScopeKeys binds StateAppEvents via the existing scopeBinder path + a new bindApp. nil backplane everywhere → zero observable change. v0.4.0 typed API untouched. Tests: existing suite green + a nil-backplane in-process StateAppEvents folding in RAM (today's StateAppSlice semantics).
- PHASE 1 — in-process StateAppEvents[E,V] core. EventReducer constraint + Read (cached projection, O(1), trackRead) + plain Append (broadcastRender reuse, nil-ctx panic) + Text. App gains logProjection map + per-key incremental fold under lock. NO backplane yet — proves the user API and the fold/projection cache against the chat+counter call sites. Migrate internal/examples/chat to StateAppEvents behind nothing (still single-pod), delete the trim-Update.
- PHASE 2 — Backplane interface + ONE reference backend (NATS JetStream+KV: durable ordered resumable EventLog AND CAS KV in one dependency, maps the interface most directly). Store(LoadSnapshot/CAS) + EventLog(Append/AppendIf/Subscribe/Head) + io.Closer + per-field Codec (default versioned-envelope JSON). WithBackplane Option. Cold start = LoadSnapshot→Subscribe(from:coveredOffset)→fold. Ship the backend CONFORMANCE TEST SUITE (ordering, gap-free resume, CAS conflict, snapshot-before-compact). This phase alone closes #3/#7.
- PHASE 3 — value-shaped coexistence. StateApp/StateSess.Update (UNCHANGED API) gains cluster survivability: CAS the Store at val:<key> then Append value-less Change{key,rev} to the shared changes feed; other pods tail, re-pull from Store, broadcastRender→SyncNow→Read (confirmed path). Local kvStore stays as L1 cache, Store is L2/durable. Session-scoped Changes namespaced by sid, filtered by broadcastRender's sess arg.
- PHASE 4 — E-versioning hardening (#6). Versioned envelope MANDATORY (enforced at Mount: no TypeTag → fail fast), RegisterEvent[E](v, Upcaster{From,To}) decode-chain, drop-on-undecodable + via.events.undecodable metric, forward-incompat projector-halt guard. Snapshot=pure disposable cache for UNCOMPACTED keys (invalidate-on-codec-hash → re-fold from 0, evolving V free); COMPACTED keys treat the snapshot as durable genesis — typed `Codec[V]`, seeded migration on mismatch, retained-event floor, `WithFoldVerify` mandatory before compaction (T2-GO-4). Additive-first godoc.
- PHASE 5 — snapshot + compaction. WithLogSnapshotEvery(N) default 512–1000 + on Shutdown; snapshot-DURABLE-FIRST then optional Compactor (type-asserted, backend declines → snapshot-only). Tombstone-delete reclamation (#5). Compact-trails-snapshot invariant + min(consumer-checkpoints) floor so no live consumer is truncated out.
- PHASE 6 — side effects + remaining backends. OnEvent(name, fn) offset-tracked consumer (#1). Add Redis Streams, Postgres, Kafka backends against the conformance suite. THEN AppendIf/ReadAt (optimistic first-writer-wins) once a real claim-ticket case appears. FOLLOW-UP/orthogonal: BroadcastCluster + ClusterTabCount (#4); dev WithFoldVerify; go-vet purity analyzer.

## Appendix: the four architect theses

### api

StateAppEvents[E]: an event-log sibling of StateApp where Read is a pure fold over an append-only EventLog, killing the CAS retry-storm and making side-effects offset-once — the fold is a method on E's reducer, not a func field, so it's deterministic by construction

- biggest risk: PROJECTION DRIFT FROM A NON-DETERMINISTIC FOLD that slips past review. The whole correctness model rests on "every pod folds the same offset range to the same V." We make impurity awkward (method-on-E, no reachable clock/ctx) but we CANNOT prove purity at compile time — a determined user can read a package-global map or time.Now() inside Fold and two pods will silently diverge, producing different rendered HTML per pod with no error. Mitigations: (a) godoc states the determinism contract in the strongest terms on Fold itself; (b) a debug/test mode WithFoldVerify that, on each Append, re-folds from the last snapshot and diffs against the incremental projection — a mismatch panics in dev, surfacing accidental statefulness before deploy; (c) a `go vet`-style analyzer (ship later) flagging time/rand/global reads inside any Fold method. But the residual risk is real and is the single thing most likely to cause a confusing production incident. Second risk: snapshot/compact ordering bugs in third-party backends (compact-before-snapshot-durable loses events) — mitigated by making Compact's godoc demand snapshot-durability-first and providing a conformance test suite backends must pass.
- what to cut: - CUT AppendIf / ReadAt from v1 if it slows shipping: plain Append covers counter + chat + most queues. The optimistic first-writer-wins case (claim-ticket) is real but rarer; ship plain Append first, add AppendIf when a user hits the conflict case. Don't let the 5% case complicate the 95% API.
- CUT the V second type param IF it muddies the mental model in early docs — could default V=the fold-of-a-slice-of-E and force projections to be a separate read-side concern. REJECTED that simplification though: a counter folding to int and chat folding to []Message is the core demo; collapsing V loses the "value differs from event" win. Keep V.
- CUT BroadcastCluster + ClusterTabCount from the initial cut — they're #4 nice-to-haves, orthogonal to StateAppEvents, and the honest async-count story can land in a follow-up. Local Broadcast already works.
- CUT the go-vet purity analyzer from v1 (ship WithFoldVerify dev-mode check instead — cheaper, catches the same class at test time).
- DO NOT CUT: the versioned envelope, upcaster registry, snapshot+compact ordering, and the method-not-func-field fold. Those are load-bearing — cutting any one reintroduces an open question (#6, unbounded storage, or non-determinism).

### correctness

StateAppEvents[E]: an append-only sibling of StateApp whose value is a deterministic fold over a durable, ordered, offset-resumable EventLog. Append events (never CAS), fold to project, Read subscribes the render exactly like StateApp.Read. One Backplane interface (Store+EventLog+Codec) backs NATS/Redis/PG/Kafka. The EventLog is the source of truth; the Store holds only fold-snapshots+offsets so cold start is replay-from-snapshot, not replay-from-zero.

- biggest risk: Fold-determinism drift across deploys is the silent killer. The whole model assumes every pod, on every version, folds a given log prefix to a byte-equal projection — but a developer ships a Fold that sorts differently, or includes a time-derived field, or changes Fold's logic in v2 such that v2 re-folding the SAME old events yields a different projection than the snapshot v1 wrote. Now pod-A (v1 snapshot) and pod-B (v2 cold-replay) render divergent HTML to two users in the same room, intermittently, with no error. It is the event-sourcing equivalent of a non-reproducible build, and it surfaces only under cluster + restart + deploy-skew — exactly the conditions this feature is for and exactly the conditions hardest to test. Mitigations (FoldChecker double-fold in dev, schema-epoch snapshots, "Fold is pure & changing it requires a new epoch" godoc) reduce but cannot eliminate it; a v2 that changes projection meaning MUST snapshot+epoch, and there's no way for the runtime to force that. This is the cost of choosing fold-over-log instead of CAS, and it's the right cost for the high-churn cases — but it must be screamed about in the godoc, not whispered.
- what to cut: - CUT auto-retry-fold from v1 of AppendIf. Ship plain Append + AppendExpecting (raw ErrConflict) only; the AppendIfStable retry-loop sugar is a fast-follow once real conditional use-cases exist. Most high-churn cases (counter, chat) are plain appends and never need it — don't gold-plate the conditional path before it has users.
- CUT the schema-registry/Avro Codec from v1. Ship JSONCodec (tolerant, debuggable) + the upcaster mechanism. Registry codecs are a Kafka-shop optimization; the upcaster chain covers correctness for everyone and the Codec interface leaves the door open.
- CUT app.Census / cross-pod broadcast count from this feature entirely. It's a real question (#4) but orthogonal to the EventLog; bundling it bloats scope. Document the local-count semantics honestly and ship Census separately if demand appears.
- CUT pluggable compaction policy knobs beyond WithSnapshotEvery(n, t). One cadence dial; let the backend's native retention do the physical truncation. Per-subject tunable compaction is premature.
- DO NOT add StateSessLog in v1 — session-scoped event logs are a real but rare want, and per-session subjects multiply the subject/snapshot bookkeeping. App-scoped only; revisit if asked.

### adapters

StateAppEvents[E] is StateApp's append-a-fact sibling: you Append immutable events, declare a pure fold, and Read the projection — same bindWireKey/notify-and-pull/subscribed plumbing, but the wire key now names a EventLog subject (events) plus a snapshot key in the Store, not a single CAS cell. ONE backend interface (Store CAS-cell + EventLog offset-resumable stream + Codec) satisfies NATS/Redis/PG/Kafka; the only real leak is compaction, which I push behind an explicit optional Compactor capability the backend may decline.

- biggest risk: The fold-determinism + snapshot-compatibility contract is unenforceable at compile time and is the single point where a deploy silently corrupts shared state. Two failure modes compound: (1) a non-deterministic or non-pure fold makes pods diverge — pod A's chat window differs from pod B's for the SAME key, nothing errors, it just looks like a flaky UI. (2) A developer changes E's JSON shape WITHOUT a matching upcaster (or forgets to bump the envelope version), so old events in the durable log decode into a zero-ish struct — history quietly rots, and because snapshots are derived, the rot bakes into the next snapshot, and then compaction deletes the original correct bytes. The Compactor turns a recoverable read bug into PERMANENT data loss. Mitigations (a startup self-check that replays the last K events through the current codec and diffs against the snapshot; refusing to Compact until a snapshot round-trips; the forward-incompatible version guard) reduce but don't eliminate it — the architecture trades CAS retry-storms for a correctness surface living in developer discipline. The trade is right for high-churn shared state, but must be sold honestly: StateAppEvents is more powerful AND more dangerous than StateApp, and the danger is mostly invisible until production.
- what to cut: - CUT optimistic expected-offset append (AppendIf) and View.Offset() from v1. The headline win is killing CAS retry-storms for append-only cases (counter, chat/ticket queue) — those use plain Append and never need it. Cutting it removes the offset-threading API and lets ReadLog return V directly instead of a View[V]. Ship AppendIf later behind real "claim-if-unclaimed" demand.
- CUT the #4 broadcast census entirely — document the count as local-only and stop. The opt-in census is speculative telemetry; it adds a cross-pod synchronous path to a fire-and-forget primitive for a number nobody acts on programmatically.
- CUT per-variant snapshot versioning cleverness: since V is always re-derivable from E, a snapshot is pure cache — version it with a single opaque codec hash and INVALIDATE-on-mismatch (re-fold from events). No snapshot upcasters, ever. Shrinks the Codec contract to envelope + event upcasters only. **[SUPERSEDED by T2-GO-4 — holds for UNCOMPACTED keys only; "no snapshot upcasters ever" is rescinded for compacted keys, where V is NOT re-derivable (prefix deleted) and the snapshot needs a typed `Codec[V]` + seeded migration. See #6.]**
- DEFER the OnEvent side-effect consumer to a follow-up. It's a clean addition (another Subscribe + offset commit) but orthogonal to the state-projection core; landing Read/Append/fold first lets the side-effect story bake against real usage.
- NON-NEGOTIABLE, stays: Append + Project + ReadLog, the Store+EventLog+Codec interface, envelope+upcaster versioning, snapshot+optional-Compactor, and nil-backplane == today's exact behaviour.

### versioning

StateAppEvents[E] = sibling of StateApp that APPENDs immutable events and Reads a fold; both ride ONE Store+EventLog+Codec backplane. StateApp is the degenerate single-key compacting case of the same EventLog, so no special-casing. The forever-persistence of E is paid for by a mandatory versioned envelope + a codec that upcasts on decode, never on store — and a hard rule: a fold that can't decode an old event must DROP it, never panic the pod.

- biggest risk: Reducer-purity is unenforceable by the compiler yet load-bearing for cluster correctness: an impure reducer (reads time.Now, ranges a map, depends on external state) makes pods diverge silently — pod A's live View ≠ pod B's replayed View — and the divergence may only surface after a restart or scale-out, far from the code that caused it. Snapshots make it WORSE by freezing a wrong View. Mitigations: (a) the cold-start replay path can run in a shadow "verify" mode that re-folds the last N events from genesis and diffs against the snapshot, emitting `via.fold.divergence` if they differ — a built-in canary; (b) godoc states the law and the failure mode bluntly; (c) the OnEvent side-effect seam removes the single biggest reason people reach for impurity inside a reducer. But there is no airtight fix — this is the inherent tax of event-sourcing folds and the design should own it loudly rather than imply the types make it safe.
- what to cut: Cut for v1, in order: (1) Compaction/snapshot of StateAppEvents — ship append+fold+full-replay first; backends that keep full history (Postgres seq table, Kafka log) work without it, and a counter/chat-of-the-day rarely outgrows RAM in the demo window. Snapshots are a scale optimization (#9-#10 territory), not correctness. (2) The shadow divergence verifier — nice canary, not needed to ship; document the law first. (3) ClusterBroadcast census — keep Broadcast local-only with corrected godoc; a real cross-pod broadcast is a separable follow-up. (4) Kafka and Postgres backends — ship ONE reference backend (NATS JetStream+KV: it gives durable ordered resumable EventLog AND CAS KV in one dependency, matching the interface most directly) to prove the Backplane interface, then add Redis, then the rest. (5) Multi-version upcaster CHAINS — v1 ships with single-step upcasters (vN→vN+1) only; if someone needs v1→v5 they register four steps. Do NOT cut: the Envelope+TypeTag+Version (retrofitting versioning onto an unversioned EventLog is impossible since records are immutable forever — this MUST be in the very first byte ever appended), drop-on-undecodable, the pure-reducer contract, and nil-backplane degradation.

