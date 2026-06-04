# Design Council — Via State Backplane (event-log model)

Living record of a multidisciplinary SWE + DX council pressure-testing
`design/state-backplane.md`. The chair (Claude) convenes a fresh panel each tick,
**validates the load-bearing claims against the codebase**, and records the
result. The loop stops when the council converges on the best design.

- Design under review: `design/state-backplane.md` (Status in doc: "DECIDED")
- Branch: `design/state-backplane`
- Cadence: every 10 min (cron `81cfa5e7`), stop on convergence.

Convention: each issue gets a stable ID (`T<tick>-<lens>-<n>`), a status
(`OPEN` / `VALIDATED` / `RESOLVED-IN-DOC` / `REJECTED`), and a one-line
resolution when closed. The doc already exhausted 4 *SWE-internal* lenses
(api/correctness/adapters/versioning); the council adds *multidisciplinary*
lenses on top.

---

## Tick 1 — 2026-06-03 — opening panel

Panel (5 independent reviewers, each grounded in the real doc + code):
DX/ergonomics · Distributed-systems/SRE · Go-idioms/type-system ·
Security/compliance · Testing/verification.

### Chair validation of load-bearing claims (done THIS tick)

| Claim (from doc) | Verdict | Evidence |
|---|---|---|
| Cross-pod re-render "reuses the confirmed `broadcastRender→SyncNow→Read` path" | **FALSE as stated** | `broadcastRender` iterates `a.snapshotContexts()` = `a.contextRegistry`, **pod-local only** (broadcast.go:60-89). No cross-pod reach. A new per-pod tailer→broadcastRender driver is required and is **unspecified** in the doc. |
| Append panics on nil ctx, parity w/ `StateApp.Update` (stateapp.go:60) | **TRUE** | stateapp.go:60-62 panics; idioms-agent confirmed. |
| `StateApp` detected via `isStateApp` marker in walker.go | **TRUE** | `stateAppMarkerType` walker.go:127; `implements` requires pkg==`via` (walker.go:138). `isStateAppLog()` is a faithful extension. |
| Recursive generic `StateAppLog[E EventReducer[E,V], V any]` compiles & infers | **TRUE (spiked)** | idioms-agent ran a compile spike — compiles, infers at field decl + Read. **Not a compile risk.** |
| `var ev E; ev.Zero()` is safe | **FALSE for pointer E** | spike: `StateAppLog[*Tick,int]` compiles, `var ev E` is nil `*Tick`, `Zero()` nil-derefs → runtime panic in `View`. |
| Append unreachable from raw client input | **TRUE** | `handleAction` gates ctx by `via_tab` + session-pointer identity, 403 on mismatch (action.go:107-115). `via_tab`/`sid` = 256-bit crypto-rand (util.go:11). |
| chat example does Op-Append THEN a 2nd Update-trim | **TRUE** | chat/main.go; every mutable shape uses `.Op(ctx).Verb()` (shape_*.go). |
| `vt` harness is single-process; no conformance/golden/container CI | **TRUE** | vt is one `httptest.Server`/one `App`; `ci-check.sh` = `go test -race ./...` only; zero `conformance`/`testdata`/Docker. |

### Open issues raised (ranked across the whole panel)

**P0 — must change the doc before implementation**

- `T1-SRE-1` **VALIDATED** — *Cross-pod render driver is asserted but does not exist.*
  "Reuse the confirmed path" hides the real new integration point: each pod's
  per-key tailer must itself call `broadcastRender(nil, sessFor(key), key)` after
  folding a remote Record. Unspecified who invokes it, with what `skip`/`sess`,
  under what lock. If wrong → remote Appends update server projection but **never
  re-render any tab** (= #3 stranding reappearing one layer up). *Fix:* specify the
  tailer→broadcastRender contract explicitly; `skip` is always nil cross-pod.
- `T1-SRE-2` **OPEN** — *Dual fold path → ordering skew (writer vs peers).* Doc folds
  locally at `Append` AND via the tailer. Writer folds its own event synchronously
  (out of offset order) while peers fold strictly in offset order → divergence on
  non-commutative folds (chat message order), independent of fold *purity*. *Fix:*
  make the **tailer the SOLE fold path on every pod incl. the writer**; accept
  read-your-write latency or expose offset to wait on.
- `T1-SEC-1` **OPEN** — *Multi-tenant / cross-session isolation on the shared bus is
  undefined.* Today isolation = in-process pointer compare (broadcast.go:68). Phase 3
  session-Changes on shared NATS/Redis need a NEW sid-string equality test + per-tenant
  subject namespacing + per-pod creds + mTLS. Post-receive filtering ≠ isolation.
  *Fix:* add a first-class isolation section (full-sid exact match, fail-closed on
  unknown sid).

**P1 — strong, fixable without touching the correctness model**

- `T1-GO-1` **VALIDATED** — *`Zero()`-on-zero-value nil-panics for pointer E.* *Fix:* seed
  from `var v V` (both example `Zero()`s just return V's zero) or enforce value-receiver E.
- `T1-SRE-7` **OPEN** — *Fold-divergence canary deferred while the hazard ships.* All
  detectors (`WithFoldVerify`, go-vet, `via.fold.divergence`) are Phase-6/"later". *Fix:*
  ship a cheap unconditional v1 canary: per-pod `(key, appliedOffset, fnv(projectionBytes))`.
- `T1-TEST-1` **OPEN** — *#1 risk (fold determinism) has no CI gate.* `WithFoldVerify` is
  same-process → can't catch two-pod divergence. *Fix:* subprocess two-replay-converge +
  property/fuzz fold-purity (mirror existing `FuzzHtmlEscape`).
- `T1-TEST-KEYSTONE` **OPEN** — *Build an adversarial in-memory `Backplane` FIRST* (before
  the NATS adapter): controllable reorder-within-allowance, redelivery, mid-Subscribe
  disconnect, call-order recording. Unlocks 4 infra-free CI tests: conformance
  (snapshot-before-compact, gap-free resume), two-pods-one-backplane convergence,
  effectively-once OnEvent, reconnect-rehydrate (#3/#7).
- `T1-SEC-2` **OPEN** — *GDPR crypto-shred is aspirational.* Snapshots fold **plaintext** PII
  into V; Compactor is optional (Kafka keeps ciphertext forever); per-subject keying unstated.
  Tombstone ≠ erasure. *Fix:* mandate per-data-subject keys + KeyStore seam + snapshot
  invalidation on erasure; concede backups must expire the key.
- `T1-SRE-3` **OPEN** — *Dedup-by-opaque-offset unsound across offset-space resets* (Redis
  XTRIM-to-empty, recreated JetStream stream, PG restore) → stale high-water-mark silently
  skips all new records. *Fix:* pair offset with a stream epoch/generation; re-snapshot on
  `Head < lastApplied` or epoch change.
- `T1-SRE-4` **OPEN** — *Cold-start "bounded by snapshot cadence" only holds if compaction is
  mandatory*, but versioning cuts snapshot from v1 → bound becomes full log age → deploy-time
  slow-start / head-of-line block on fresh pods (ironic for a feature fixing deploy-freeze).
  *Fix:* mandatory snapshot for clustered log keys; gate readiness probe on projector-caught-up;
  serve last-snapshot + "rehydrating" rather than block View.
- `T1-SRE-5` **OPEN** — *Shared "changes" feed = single hot subject + N×-per-write Store
  re-pull thundering herd + stale-read across the independent Store-rev/Log-offset counters.*
  *Fix:* shard changes by key-hash; consumer verifies `storeRev >= change.rev`; coalesce
  per-key re-pulls.

**P2 — DX surface & consistency (no correctness impact)**

- `T1-DX-1` **OPEN** — *`StateAppLog` abandons the `.Op(ctx).Verb()` idiom* every other shape
  uses → two mutation grammars. *Fix:* route via `l.Op(ctx).Append(ev)` for surface consistency.
- `T1-DX-2` **OPEN** — *Counter case is the worst advertisement:* empty `Tick struct{}` + 2
  methods + `_, _ =` discard replaces `StateAppNum[int]`+`.Add(1)`. *Fix:* ship a counter
  specialization (precedent: StateAppNum/StateAppSlice).
- `T1-GO-2` **OPEN** — *`Codec.Encode(any)/Decode()any` reopens the in-flight no-public-any
  effort.* Codec is per-`StateAppLog[E]` → make it `Codec[E]`, the `any` vanishes.
- `T1-GO-3` **OPEN** — *User API leaks bare `uint64` for offset* while the backend newtypes
  `Offset`. *Fix:* use exported `Offset` end-to-end (a bare uint64 invites the arithmetic the
  doc forbids).
- `T1-DX-5/GO-4` **OPEN** — *v1 public surface is 7 methods*; 3 architects already voted to cut
  `AppendIf`/`ReadAt`/`OnEvent` from v1. *Fix:* present v1 = `Read`/`Append`/`Text`/`Key`.
- `T1-GO-4b` **OPEN (doc-framing)** — "method-on-E is the ONLY visible-smell option" is
  overstated (a method can still read time/globals invisibly; the signature, not the
  method-ness, gives the guarantee). *Fix:* lead with the genuine win — **compile-time
  binding** (no forgot-to-register / nil-reducer path).

**WIN to foreground**

- `T1-SEC-WIN` — append-only Log is a free tamper-evident **audit trail**; but it tensions
  with erasure (C2) → doc must separate audit-class (retained) from PII-class (shreddable) events.

### Convergence verdict — Tick 1: **NOT CONVERGED**

All 5 lenses returned "not converged." Two findings are P0 *correctness* gaps
(`T1-SRE-1` validated, `T1-SRE-2`), one is a P0 *security* gap (`T1-SEC-1`). The
core type design (two type params, method-on-E fold, plain Append) is sound and
worth its cost — no lens wants it reworked. Convergence is blocked on a handful
of *specific, bounded* fixes, not a redesign.

**Next tick:** drive the 3 P0s to resolution — feed `T1-SRE-1`/`T1-SRE-2` back as
a concrete "single-fold-path + explicit tailer→broadcastRender contract" proposal
and have the SRE + Go lenses adversarially check it; draft the `T1-SEC-1`
isolation section and have security validate. Re-validate each against the code
before declaring any P0 RESOLVED.

---

## Tick 2 — 2026-06-04 — P0 resolution + validation

Three proposer architects (runtime/SRE, security, Go-API/DX) produced concrete,
doc-ready replacement text for the tick-1 issues. Chair validated the
load-bearing new claims against code before recording.

### Chair validation (done THIS tick)

| New claim | Verdict | Evidence |
|---|---|---|
| Projector calling `broadcastRender(skip=nil,…)` is safe & not deadlock-prone | **TRUE** | broadcast.go:61 `silent` guard only fires when `skip!=nil`; `SyncNow` takes `actionMu` on its own `go` goroutine (ctx.go:319-324); projector holds no ctx mutex. |
| With `skip=nil`, a writer's `SyncOff()` no longer suppresses its own `StateAppLog` re-render | **TRUE (side-effect)** | The re-render now comes from the projector (skip=nil), bypassing the `silent` guard. Real semantic change → must document/special-case. → `T2-SRE-6`. |
| "Snapshot = pure disposable cache, re-fold from events" is consistent with Compact | **FALSE (contradiction)** | `Compact` deletes events `[0,beforeOffset)` (doc 221-224); you cannot re-fold a compacted prefix. "Evolving V is free via re-fold" (doc 567/659) holds only for *uncompacted* keys. → new `T2-GO-4`. |

### Issue status after tick 2

**P0 — all RESOLVED-IN-DOC (validated)**

- `T1-SRE-1` **RESOLVED-IN-DOC** — `broadcastRender` stays pod-local (the *intra*-pod
  leg); a new per-pod, per-key **projector** goroutine is the *inter*-pod leg: it is the
  sole writer of `logProjection[key]`, tails `Log.Subscribe(from=highWater)`, folds, then
  calls `broadcastRender(skip=nil, sess=sessFor(key), key)`. `skip=nil` justified (projector
  holds no action ctx). Redelivery deduped by monotone `highWater`.
- `T1-SRE-2` **RESOLVED-IN-DOC** — **Append never folds locally; the projector is the single
  fold path on every pod incl. the writer.** Every pod folds the identical offset sequence in
  offset order → convergence by construction. Tradeoff: **eventual** read-your-write (writer's
  View may show pre-append V for one intra-pod projector hop; sub-ms, no network). Opt-in
  `WaitFor(key,off)` for strict RYW. Rejected the sync-fold alternative (reintroduces skew).
- `T1-SRE-3` **RESOLVED-IN-DOC** — `Epoch uint64` (generation) added to `Record` + `Head`
  return; checkpoint persists `(epoch,coveredOffset,codecHash,V)`; resume re-snapshots from
  genesis on epoch change / `Head<lastApplied`; live-guard inside tail loop self-heals on
  mid-tail reset + `via.log.epoch_reset` metric. Conformance: an "offset-space reset" case.
- `T1-SEC-1` **RESOLVED-IN-DOC** — new "Multi-tenant & session isolation" section: (1) session
  Changes carry the **full 256-bit sid**, receiver does **exact-match `byID`**, unknown sid
  **DROPs fail-closed** (never broadcast-to-all); (2) per-tenant subject/key namespace + per-pod
  creds + mTLS = the *physical* boundary (broker rejects cross-namespace); (3) **AUTH-1**
  invariant stated (Append only via via_tab+session-gated ctx, action.go:107-117); (4) backplane
  = tier-0 trust, mandate authn+mTLS, `WithInsecureBackplane()` escape hatch, compromised-pod
  blast radius named, record-signing deferred.
- `T1-SEC-2` **RESOLVED-IN-DOC** — per-data-subject keys + `KeyStore{KeyFor,DropKey}` seam;
  key-drop → `ErrUndecodable` → reuses drop-on-undecodable fold no-op; **snapshot invalidation
  on erasure** (snapshots fold plaintext V → forced PII-free re-fold); tombstone ≠ erasure
  conceded; audit-class vs PII-class events separated at declaration.

**P1**

- `T1-GO-1` **RESOLVED-IN-DOC** — drop `Zero()`; seed = `var zero V` (Go zero of projection).
  Kills the pointer-receiver nil-panic; both example `Zero()`s were just V's zero anyway.
  Non-zero empty value → encode as a genesis event (keeps "value IS a pure fold" literally true).
- `T1-GO-2` **PARTIALLY RESOLVED → reopened by `T2-GO-4`** — make event codec typed `Codec[E]`
  (kills public `any`, aligns no-public-any); snapshot-of-V kept unexported (`snapshotCodec`)
  *on the assumption snapshots are disposable* — which `T2-GO-4` shows is false for compacted keys.
- `T1-GO-3` **RESOLVED-IN-DOC** — export `Offset` end-to-end (Append/AppendIf/ReadAt/OnEvent),
  removing a bare-`uint64` that invited the arithmetic the newtype forbids.
- `T1-SRE-4`/`T1-SRE-5`/`T1-SRE-7`/`T1-TEST-*`/`T1-SEC-*` residuals — **OPEN, triage next tick**
  (mandatory-snapshot-in-cluster, shared-feed sharding, v1 divergence canary, adversarial
  in-memory Backplane keystone). Mostly v1-scope decisions + backend-conformance, not code-blocking.

**P2 — RESOLVED**

- `T1-DX-1` **RESOLVED (tick-1 fix REJECTED)** — keep direct `l.Append(ctx, ev)`. Grounded
  rebuttal: `Op(ctx)` exists to *bundle a verb cluster* over `Update`; a log has ONE verb and no
  `Update`, so its true peer is `StateApp.Update` (also direct, ctx-first) — not `SliceOps.Append`.
  Add a one-line godoc making the divergence explicit.
- `T1-DX-2` **RESOLVED** — ship `StateAppCounter{StateAppLog[tick,int64]}` specialization (precedent:
  StateAppNum); hides the empty `tick` event. Counter call site → `Hits via.StateAppCounter` + `Inc`.
- `T1-DX-5` **RESOLVED** — v1 surface = `Read`/`Append`/`Text`/`Key` (+`Counter.Inc`);
  `AppendIf`/`ReadAt`/`OnEvent` → "Advanced (post-v1)".

### New issue (tick 2)

- `T2-GO-4` **VALIDATED (P1)** — *snapshot-as-pure-cache contradicts compaction.* "Evolving V is
  free, just re-fold" + "invalidate snapshot on codec-hash" assume events are always replayable —
  but `Compact` deletes `[0,beforeOffset)`. For a compacted key the snapshot V-bytes are the **only**
  source for that prefix → discarding+re-folding truncates the projection. *Implications:* (a) the
  untyped disposable `snapshotCodec` (T1-GO-2) is unsafe for compacted keys; (b) changing V's shape
  is NOT free once compacted — needs either snapshot-V upcasters, OR a retained event floor at the
  oldest snapshot's coveredOffset, OR an explicit epoch/re-snapshot on V-shape change. *Resolve next
  tick.*
- `T2-SRE-6` **VALIDATED (P3/doc)** — `SyncOff()` no longer suppresses `StateAppLog` re-renders
  (projector uses skip=nil). Arguably correct (a durable appended fact isn't a suppressible
  optimistic value write) but must be documented or special-cased.

### Convergence verdict — Tick 2: **NOT CONVERGED (close)**

All three P0s are resolved-in-doc and the resolutions were code-validated. The
core API is settled (drop Zero, typed Codec[E], Offset end-to-end, direct Append,
StateAppCounter, trimmed surface). Convergence now blocks on exactly: (1) the new
`T2-GO-4` snapshot/compaction contradiction (P1, concrete fix needed); (2) a v1
scope triage of the remaining SRE/TEST residuals (mandatory snapshot, feed
sharding, the unconditional divergence canary, the adversarial in-memory
Backplane) — decide *blocking-for-v1* vs *accepted-and-documented*. Several
proposer-introduced claims (per-backend Epoch derivability, read-your-write
latency, KeyStore snapshot-invalidation completeness) are backend-conformance
items that cannot be code-validated pre-implementation — they belong in the
conformance suite, not in convergence gating.

**Next tick:** resolve `T2-GO-4` (snapshot durability vs evolving-V); run a final
adversarial pass triaging the remaining residuals into v1-blocking vs
documented-accepted; if no new P0/P1 surfaces, declare convergence and stop the
loop.

---

## Tick 3 — 2026-06-04 — T2-GO-4 resolution + convergence audit

Two agents in parallel: a storage/codec architect resolving `T2-GO-4`, and a
convergence auditor triaging residuals + adversarially hunting new blockers. The
auditor found a NEW P1; chair code-validated it. **Convergence deferred one more
tick.**

### Chair validation (done THIS tick)

| New claim | Verdict | Evidence |
|---|---|---|
| Value-path `StateApp.Update` self-skips the writer & never re-pulls (→ writer-convergence gap cross-pod) | **TRUE** | stateapp.go:66-74: mutates **local** `appStore` synchronously, then `broadcastRender(ctx, …)` passes `ctx` as `skip` (writer excluded), re-renders from its own local mutation. No re-pull. Symmetric to the `T1-SRE-2` log-path bug, left unfixed on the value path. → `T3-SRE-1`. |
| `StateAppCounter` int64 fold-overflow is a correctness divergence | **FALSE (cleared)** | Overflow is *deterministic* — every pod wraps identically at 2^63, convergence preserved; 2^63 appends unreachable. Display quirk, not a backplane bug. Auditor explicitly cleared it. |

### `T2-GO-4` — RESOLVED-IN-DOC

Resolution (hybrid #2+#3): **a key may compact OR freely evolve V — but once it
has compacted, V-evolution costs a typed snapshot migration.** Compaction
"freezes" the snapshot from disposable cache into durable genesis state.

- Snapshot becomes `Checkpoint{epoch, coveredOffset, codecHash, vbytes}`.
- **Uncompacted key:** codec-hash mismatch → discard + re-fold from 0 (V evolution
  genuinely free — the common case, most keys never compact).
- **Compacted key:** mismatch MUST NOT discard (would truncate to the uncompacted
  tail = silent loss). Runs a **seeded migration**: `snapshotCodec.Decode(vbytes)`
  upcasts old V → seed, fold the tail on top, rewrite checkpoint. ⟹ **`snapshotCodec`
  must be typed `Codec[V]` + version-tagged** — the adapters lens's "no snapshot
  upcasters EVER" is rescinded *for compacted keys only* (partially reopens & resolves
  `T1-GO-2`: event codec typed `Codec[E]`, snapshot codec typed `Codec[V]`).
- **Fold-MEANING change** (≠ V wire shape) → user bumps `epoch`; re-fold from the
  **second-newest retained snapshot** under new Fold.
- **Retained-event floor:** `Compact(before)` clamped to
  `min(coveredOffset(2nd-newest snapshot), min(consumer-checkpoints)) − safetyWindow`
  → always ≥1 re-foldable snapshot generation on disk; steady-state disk ~2× minimum
  (accepted trade). Unbridgeable epoch bump → `ErrEpochUnbridgeable`, projector halts
  (roll-forward-only, same class as the forward-incompat guard).
- Residual: seeded migration trusts the old snapshot was written by a *pure* Fold —
  compaction makes an impure-fold corruption **permanent** (evidence deleted).
  Strongest argument yet that `WithFoldVerify` should be **mandatory before a key may
  compact**.

### Residual triage (auditor)

| Residual | Disposition |
|---|---|
| `T1-SRE-4` mandatory snapshot in cluster | **ACCEPTED-DOC** — replay-from-genesis is correct, only slow; make snapshot mandatory for clustered log keys + gate readiness on caught-up. |
| `T1-SRE-5` shared-feed | **SPLIT** — herd/hot-subject = ACCEPTED-DOC (shard later); **stale-read across independent Store-rev/Log-offset counters = BLOCKING** → needs `storeRev >= change.rev` guard (same root cause as `T3-SRE-1`). |
| `T1-SRE-7` divergence canary | **ACCEPTED-DOC** — detector not correctness; ~free (`fnv(projection)` per offset), recommend shipping in v1 anyway. |
| `T1-TEST-keystone` adversarial in-mem Backplane | **ACCEPTED** — reaffirmed as FIRST Phase-2 deliverable; not design-gating. |
| `T1-TEST-1` fold-determinism CI gate | **ACCEPTED-DOC** — contract in design, gate ships Phase-1/2. |
| `T2-SRE-6` SyncOff vs StateAppLog | **ACCEPTED-DOC** — validated correct; doc-only. |
| `R1` compromised pod | **ACCEPTED-DOC** — inherent to shared backplane; mTLS+per-pod-creds is the v1 boundary; record-signing deferred. |
| `R2` erasure≠destruction | **ACCEPTED-DOC** — conceded in T1-SEC-2. |
| `R3` snapshot-invalidation completeness | **ACCEPTED-DOC (conformance)**. |
| `R4` sid confidentiality under namespace bugs | **ACCEPTED-DOC** — defense-in-depth; namespace is the physical boundary. |

**Net:** only the `T1-SRE-5` stale-read leg + `T3-SRE-1` (same root cause) are
v1-blocking; everything else ships accepted-and-documented.

### New finding (tick 3)

- `T3-SRE-1` **VALIDATED (P1, BLOCKING)** — *value-shaped cross-pod path does not
  converge writer pods.* Phase-3 spec has peers re-pull on `Change` but writers
  self-skip (stateapp.go:74 passes `ctx` as skip) and never re-pull; Store-CAS-rev and
  Log-offset are two independent total orders with no "converge to Store head" step for
  writers. *Fix (bounded, one paragraph, symmetric to `T1-SRE-2`):* state the invariant
  — **the Store is the single source of truth; every pod incl. writers converges by
  re-pulling to Store HEAD on each received `Change`, gated by `storeRev >= change.rev`**
  (re-pull-to-head, not to-rev, so a missed intermediate still lands on head). Other
  seams checked & CLEAR: projector idle-TTL vs changes-feed, `OnEvent` vs single-fold-path
  (separate tailer, never writes projection), CAS-then-Append vs concurrent reader (atomic
  Store read), `WaitFor(key,off)` (monotone highWater, per-key off), Mount-ordering
  (zero-then-subscribed-rehydrate), nil-backplane (one folder, trivially single-path).

### Convergence verdict — Tick 3: **NOT CONVERGED**

A single bounded blocker remains: `T3-SRE-1` (value-path writer convergence) +
its sibling `T1-SRE-5` stale-read guard — both resolved by one Phase-3 invariant
(Store = single source of truth, all pods re-pull-to-head on Change gated by
`storeRev ≥ change.rev`). `T2-GO-4` is resolved. All other residuals triaged to
accepted-and-documented. The design is one paragraph from convergence.

**Next tick:** write the `T3-SRE-1`/`T1-SRE-5` value-path invariant as concrete
Phase-3 text, have the SRE lens adversarially check it converges under concurrent
cross-pod writes + Store replica lag, re-validate against stateapp.go/broadcast.go.
If it holds and no new P0/P1 surfaces → **declare convergence and stop the loop.**

---

## Tick 4 — 2026-06-04 — value-path convergence + CONVERGENCE

The SRE architect wrote the Phase-3 value-path invariant and **adversarially
red-teamed it** across four worst-case interleavings. Interleavings 1-3 converge;
interleaving 4 surfaced one final P1, whose closure is complete and self-contained.

### `T3-SRE-1` + `T1-SRE-5` stale-read leg — RESOLVED-IN-DOC

Phase-3 value-path invariant (doc-ready):
- **Store cell `val:K` is the single source of truth**; the `changes` feed carries
  value-less `Change{K,rev}` as a *liveness hint only*.
- **Writer L1 is optimistic, reconciled via the feed like any peer** — the writer no
  longer treats its local mutation as final; the `broadcastRender(skip=ctx)` self-skip
  is now only a mutex-reentry optimization for the synchronous local render, not the
  writer's authority. (Symmetric to the log-path `T1-SRE-2` fix.)
- **Consumer re-pulls to Store HEAD, never to `change.rev`**, gated `storeRev ≥ change.rev`
  (stale replica read → drop + metric + one bounded backoff re-poll, never apply-stale —
  closes `T1-SRE-5`).
- **L1 monotonicity gate** (`apply only if storeRev > l1Rev[K]`) → out-of-order /
  redelivered Changes can never regress; two pods at the same `storeRev` hold byte-identical
  value (single-cell read, no fold).
- **RYW:** writer sync-optimistic (L1 set to the rev it CASed → provably ≤ HEAD → gate can
  only confirm/advance, never contradict); peers eventual.

Red-team verdict on interleavings: **(1)** disagreeing CAS-order vs feed-order → all pods incl.
both writers converge to Store HEAD ✓; **(2)** replica lag behind change.rev → drop+backoff, no
permanent stale ✓; **(3)** out-of-order/redelivered Changes → monotone gate, no regression ✓.

### New finding (tick 4) — RESOLVED-IN-DOC

- `T4-SRE-1` **(P1)** — *crash between CAS and Append strands peers (lost-notify).*
  `CAS(val:K) ; Append(Change{K,rev})` is not atomic; a crash in the window commits the value
  to the Store but emits no notification → peers never re-pull → stranded at a stale-but-
  consistent value (the #3-class hazard, reopened on the value path by splitting commit from
  notify across two systems). **Closure (remedy 1, minimal & recommended):** each pod runs a
  **periodic Store-head reconcile sweep** over its *subscribed* value keys (gate (3)), making
  the `changes` feed a pure latency optimization — **correctness never depends on a `Change`
  being emitted.** One Phase-3 sentence closes it; it also subsumes interleaving-2's backoff
  re-poll into one mechanism. (Rejected: append-first (weaker), cross-system atomic txn (not
  all backends), fold-value-onto-Log (largest blast radius — defer).) With the sweep, **all
  four interleavings converge.**
  - *Chair note:* the sweep is the value-path analogue of the Log's "append IS commit" — it
    makes the async notify non-load-bearing, structurally eliminating the ENTIRE
    lost-notify / two-independent-orders class on the value path. This is a complete
    correctness argument with no remaining surface, not a coincidental pause → genuine
    convergence.

---

## ✅ CONVERGED — 2026-06-04 (tick 4)

The council converges on the design. Summary of the final, agreed shape:

**Core (unchanged from the doc's spine, endorsed by all lenses):** `StateAppLog[E, V]`
event-log sibling of `StateApp[T]`; fold is a method on E (compile-bound via `EventReducer`);
plain `Append` never CASes (kills the hot-key retry-storm structurally); `nil` backplane ==
today's byte-for-byte in-process behavior.

**Corrections the council made load-bearing:**
1. **Cross-pod = per-pod projector** (`T1-SRE-1`): `broadcastRender` is the *intra*-pod leg;
   a per-(pod,key) projector tailing `Log.Subscribe` is the *inter*-pod leg and the **sole
   fold path on every pod incl. the writer** (`T1-SRE-2`) → convergence by construction;
   eventual-RYW for the writer, `WaitFor(key,off)` for strict.
2. **Offset-epoch guard** (`T1-SRE-3`): `Epoch` generation token detects offset-space resets.
3. **Multi-tenant/session isolation** (`T1-SEC-1`): full-sid exact-match + fail-closed,
   per-tenant namespace + per-pod creds + mTLS as the physical boundary, AUTH-1 invariant
   (Append only via via_tab+session-gated ctx), backplane = tier-0 trust.
4. **GDPR crypto-shred** (`T1-SEC-2`): per-data-subject keys + `KeyStore` + snapshot-
   invalidation-on-erasure; audit-class vs PII-class events separated.
5. **API hygiene:** drop `Zero()` (seed = `var v V`, kills the pointer nil-panic, `T1-GO-1`);
   typed `Codec[E]` events + typed `Codec[V]` snapshots (`T1-GO-2`/`T2-GO-4`); `Offset`
   end-to-end (`T1-GO-3`); direct `Append` retained (justified divergence, `T1-DX-1`);
   `StateAppCounter` specialization (`T1-DX-2`); v1 surface = Read/Append/Text/Key (`T1-DX-5`).
6. **Snapshot/compaction** (`T2-GO-4`): a key may compact OR freely evolve V — once compacted,
   the snapshot is durable genesis state (typed `Codec[V]` + retained-event floor;
   `WithFoldVerify` should be mandatory before a key may compact).
7. **Value-path convergence** (`T3-SRE-1`/`T1-SRE-5`/`T4-SRE-1`): Store = single source of
   truth; all pods re-pull-to-HEAD on Change gated by `storeRev ≥ change.rev`, monotone L1
   gate, **+ periodic Store-head reconcile sweep** so correctness never depends on a notify.

**Accepted-and-documented (non-blocking) residuals:** mandatory snapshot in cluster (`T1-SRE-4`);
shared-feed sharding/herd (`T1-SRE-5` scale leg); v1 divergence canary (`T1-SRE-7`, ~free, ship
anyway); adversarial in-memory Backplane as first Phase-2 deliverable (`T1-TEST-keystone`);
fold-determinism CI gate (`T1-TEST-1`); SyncOff-vs-StateAppLog doc note (`T2-SRE-6`); security
R1-R4 (compromised pod, erasure-outside-KeyStore, snapshot-invalidation completeness, sid-under-
namespace-bugs).

**Single irreducible residual the council cannot design away:** fold-determinism drift is
unenforceable at compile time and is *the* production risk — narrowed (single fold path makes
any divergence purely a purity bug, not an ordering bug) and detectable (canary + WithFoldVerify
+ subprocess two-replay CI), never eliminated. The design owns this loudly rather than implying
the types make it safe.

**Loop stopped** (cron `81cfa5e7` deleted) — convergence reached at tick 4.

---

## Addendum — 2026-06-04 — council pitch: "default to an in-memory backplane impl, allow bring-your-own"

Follow-up question (not a loop tick): should Via **ship a concrete in-memory
implementation of `Backplane` as the default + let users bring their own**,
instead of the converged design's "`nil` backplane == special-cased in-process
path"? Three lenses (runtime/perf, DX/API, testing) pitched in; chair validated.

### Chair validation

| Claim | Verdict | Evidence |
|---|---|---|
| The "bring-your-own" half is already in the converged design | **TRUE** | `WithBackplane(b Backplane)`, adapters in separate modules, core zero infra deps (doc ~265). The only NEW delta is flipping the *default* from `nil`-sentinel → a concrete impl. |
| Today's value `Update` is a direct synchronous mutex write, no serialization, holds live `any` | **TRUE** | kvstore.go:12/17/35 — `sync.Map` of `any` + per-key mutex; `Read`=direct `Load`+assert. So `StateApp[T]` can hold non-marshalable `T` (func/chan/unexported) a serializing backplane would break. |
| There is no projector / projection-cache today | **TRUE** | `Read`=direct `appStore.Load`; the per-key projector + `logProjection` (converged design) is net-new machinery, not a refactor. |

### The three positions

- **Runtime/perf → HYBRID.** Routing the default single-pod path through the full
  interface costs, per op: `Codec.Encode/Decode` (JSON of an event that never leaves
  the process), offset bookkeeping, a projector goroutine + channel hop, and flips
  synchronous read-your-write into an async channel round-trip — ~1-2 orders more CPU,
  O(history) memory, and it breaks the byte-for-byte/any-`T`/zero-overhead promise for
  **value-`StateApp`**. Verdict: value-state MUST keep the fast direct path; `StateAppLog`
  (no legacy promise) may use a default in-mem Log **only if** it uses an *identity Codec*
  (skip JSON in-process) and folds *synchronously* locally (projector channel reserved for
  records arriving from a remote Subscribe).
- **DX/API → keep `nil` public, add `via.InMemory()`.** The genuinely-new part (concrete
  *default*) is net-negative on one axis: an in-mem backplane faithfully implements
  ordered Log + CAS Store, so it **looks distributed but silently isn't** — adopt
  `StateAppLog`, "works" single-pod, deploy a 2nd pod, no fan-out, no error (the exact
  silent-divergence the feature exists to kill, re-introduced at the defaults layer). `nil`
  is honestly "in-process only." Keep `nil` as the *public* default; optionally implement it
  *internally* as an unexported in-process backplane ("internal uniformity, external
  honesty"); **export `via.InMemory()`** as a named reference impl + test/local handle, with
  godoc screaming "single-process, survives nothing, spans no pods, never production."
- **Testing → adopt single path, conditional on 3 guards.** `nil`-as-separate-path is a real
  bit-rot hazard: under the no-container CI reality the `Backplane` interface is exercised by
  *zero* default-CI tests (only the NATS path touches it). One path ⟹ every single-pod user
  + every `go test` run exercises the real interface, and **two `App`s sharing one in-mem
  backplane instance = a genuine cross-pod convergence test, infra-free** (the keystone
  unlock for `T1-SRE-1/2`, `T3/T4-SRE-1`). Guards: (1) conformance MUST also run vs a real
  network backend in a release-gating tagged job (in-mem is too forgiving — perfect order, no
  lag, no redelivery, no crash window: it exercises the *signature*, not the *corrections*);
  (2) resolve sync-vs-async RYW explicitly; (3) conformance runs vs the bare base too. Artifact
  model: `memlog.Backplane` (clean base = the default) + `memlog.Faulty` (fault-injecting
  decorator over the same base = the `T1-TEST-keystone` double); suite runs vs base /
  faulty-base / real NATS.

### Synthesis — the council's converged recommendation

**Adopt a refined form of the idea. All three lenses reconcile on this shape:**

1. **One internal implementation, exercised always.** Implement the in-process path *as* a
   real, clean in-memory `Backplane` (`memlog.Backplane` / surfaced as `via.InMemory()`) —
   killing the two-code-path bit-rot (Testing F1) and making every single-pod run + default
   CI exercise the real interface.
2. **`nil` stays the PUBLIC default and resolves internally to that impl.** Zero ceremony,
   zero-value-usable, and *honest* — `via.New()` is in-process-only, named by absence. Existing
   v0.4.0 users observe nothing change (the byte-for-byte invariant holds). This is DX's
   "internal uniformity, external honesty," and it sidesteps the "looks-distributed-but-isn't"
   default trap.
3. **The in-mem impl's hot path is synchronous + identity-coded** — no JSON in-process, value
   path folds/writes synchronously — so the perf/any-`T`/byte-for-byte promise survives
   (resolves Testing's AG2 RYW tension *and* the perf lens's carve-out in one stroke: the
   in-process backplane is genuinely zero-serialization, and the projector channel hop is used
   only for records arriving from a *remote* Subscribe).
4. **`via.InMemory()` is exported** as the named reference + test/local handle (DX + the
   two-Apps-one-backplane convergence test). Its godoc states the non-durable, non-clustered
   contract explicitly.
5. **The test double is `memlog.Faulty`, a fault-injecting decorator over the same base**; the
   conformance suite runs vs base / faulty-base / **a real network backend in a release-gating
   tagged job** (non-negotiable — in-mem green CI must never stand in for real-backend
   conformance).
6. **Phase shift:** the clean in-mem `Backplane` moves into **Phase 1** (it's now the default
   runtime, where Phase 1 already builds the projector); `memlog.Faulty` + the parameterized
   conformance suite remain the first **Phase-2** deliverable, now decorating an existing base
   rather than built from scratch.

**Net:** *Yes — default to an in-memory backplane and allow bring-your-own, but keep `nil` as
the honest public default that resolves to it, make that impl's in-process hot path
synchronous + serialization-free so the byte-for-byte promise holds, and make it the same base
the fault-injecting test double and the conformance suite decorate.* The user's instinct
(one swappable backplane, in-mem by default) is right; the council's refinement is **where the
seam sits** (public `nil` → internal in-mem, value-path stays zero-cost) and **the guard that
in-mem CI never replaces a real-backend conformance job.**

---

## Addendum — 2026-06-04 — naming council: "Log" is the wrong name

Maintainer flagged `Log` as wrong. Two lenses (Go-idioms, DX/teaching) pitched in;
chair verified collisions against the codebase.

### Chair validation

| Claim | Verdict | Evidence |
|---|---|---|
| `Log` collides with Via's own observability surface | **TRUE** | `via` exports `Logger`, `LoggerFunc`, `WithLogger`, `WithLogLevel`, `LogLevel`, `SlogLogger`, `func Log`, `log.go`. "Log" already means *logging* here; metrics `via.log.undecodable`/`via.log.epoch_reset` would read as log-diagnostics. Rename mandatory, not cosmetic. |
| `Stream` is taken | **TRUE** | `func Stream` + `stream.go` = the SSE transport. `Backplane.Stream` next to an SSE `Stream` is a confusion bomb. |
| `Event*` is overloaded | **TRUE** | reactive UI: `on.Click`/`on.Key`/`OnEvent`/DOM events + E="domain event". `StateAppEvents` next to `OnEvent` muddies UI-vs-domain. |

### Both lenses agree

- Drop `Log` from the user-facing type (the chat field is even *named* `Log` — double bad).
- Interface must pair with `Store`. Reject `Stream`, `Events`, `Feed`, `Sequence`/`Seq`
  (now reads as `iter.Seq`), `Fold` (names the read mechanism, hides the `Append` verb),
  `Tape`, `History` (connotes read-only past, fights "your live write surface").
- Top two: **`Journal`** and **`Ledger`**.

### The split (the decision that's the maintainer's)

- **Go-idioms lens → `Journal`.** Deciding factor: natural `Append`-verb collocation at
  **zero ripple** ("append to / read the journal"; `Record`, `Offset`, `Append`, `Compact`
  all stay). `Ledger` carries finance/blockchain baggage and nudges `Record`→`Entry`.
- **DX/teaching lens → `Ledger`.** Deciding factor: the only everyday word that teaches
  **both** halves — append-only entries **and** a current value that is a *fold* over them
  (entries→balance ≡ events→fold). Every runner-up teaches append-only but leaves "Read
  returns a derived projection" to the godoc. Audit connotation = a property the design
  already claims (`T1-SEC-WIN`). `Journal` is its safe #2 (loses the fold-teaching edge).
- DX lens's #3 hybrid: user type `StateAppEvents[E,V]`, internal interface `EventLog`
  (keep "Log" only as a backend term-of-art no user sees).

### DECISION — `Events` + `EventLog` (maintainer pick)

Drop "Log" from the **user-facing** surface; keep it only as a qualified backend
term-of-art (`EventLog`) that no user types in a struct. Final symbol set:

| Was | Now |
|---|---|
| `Log` (interface, in `Backplane`) | **`EventLog`** — pairs as `Backplane = Store + EventLog` |
| `StateAppLog[E, V]` (user type) | **`StateAppEvents[E, V]`** |
| chat field `Log via.StateAppLog[…]` | **`Events via.StateAppEvents[ChatEvent, []Message]`** |
| `memlog` (default in-mem pkg) | **`memevents`** (`memevents.Backplane` clean base + `memevents.Faulty` test double); public handle stays `via.InMemory()` |
| metrics `via.log.undecodable` / `via.log.epoch_reset` | **`via.events.undecodable` / `via.events.epoch_reset`** (off the `via.log.*` logging namespace) |
| `Record`, `Offset`, `Append`/`AppendIf`, `Head`, `Subscribe`, `Compactor`/`Compact`, `OnEvent`, `Change`, `Epoch`, `EventReducer`, `StateAppCounter`, `Codec[E]` | **unchanged** |

**Why it holds together:** the plural-noun field (`Events ChatEvent`) is a strong
"you append to this" cue and self-documents; `OnEvent` reads coherently *as a
consumer over those events* (the "event" in `OnEvent` = the domain `E` in
`StateAppEvents`, not a UI event); `EventReducer[E,V]` already says "event," so
the family is internally consistent. The backend `EventLog` is where "Log" is a
precise event-sourcing term-of-art — and being internal, it never collides with
the user-facing `Logger`/`slog` surface.

**Residual to watch (DX lens flagged):** `StateAppEvents` shares the word "event"
with the reactive-UI vocabulary (`on.Click`/`on.Key`). Mitigated by namespace
distance (`via.StateAppEvents` vs the `on.*` package) and by godoc, but the
type's one-liner should explicitly say "domain events you append, not UI events."

**Next (not yet applied):** propagate the rename across
`design/state-backplane.md` (mechanical `Log`→`EventLog`/`StateAppLog`→
`StateAppEvents`/`via.log.*`→`via.events.*`).

---

## Tick 5 — 2026-06-04 — APPLY the rename (naming-addendum action item)

Chair convened a fresh panel; the council has been CONVERGED since tick 4 and the
*only* explicitly flagged-but-unapplied action was the naming addendum's "Next:
propagate the rename across `design/state-backplane.md`." That is the next step —
executed this tick.

### Done this tick — rename applied to `design/state-backplane.md` (79 lines)

Line-aware (not blind sed) because bare `Log` is overloaded in the doc:

| Token | → | Notes |
|---|---|---|
| `StateAppLog[E,V]` (user type) | `StateAppEvents[E,V]` | incl. markers `isStateAppLog`→`isStateAppEvents`, `roleStateAppLog`→`roleStateAppEvents` |
| `Log` interface + the event-log *concept* ("the Log", `Log.Append/Compact/AppendIf`, `Store+Log+Codec`) | `EventLog` | backend term-of-art; pairs as `Backplane = Store + EventLog` |
| chat field `Log via.StateAppLog[…]`, `r.Log.Append/Read` (lines 287/499/514/521) | `Events` / `r.Events.*` | the new design's field |
| metrics `via.log.undecodable` / `via.log.epoch_reset` | `via.events.*` | off the `via.log.*` logging namespace |

**Deliberately left bare `Log`:** lines 464-465 describe TODAY's chat
(`Log via.StateAppSlice[Message]`) — current code, renaming it would misstate
fact. (`memlog`→`memevents` had zero hits in the doc; it lives only in the council
record.) Verified: residual `\bLog\b` (excl. `EventLog`) = exactly those 2 TODAY lines.

### Convergence status — still CONVERGED; rename action item CLOSED

### Next step the council identifies for the following tick

The rename was the last *flagged* action, but a fresh read shows the doc's **body
still describes the PRE-council design** in several load-bearing places — the
converged corrections live only in this council record, not in the artifact:

- §"Borrowed from the other lenses" / Phase 4 (lines ~541, 567, 624) still assert
  **"snapshot = pure cache, evolving V is FREE, no snapshot upcasters"** with no
  `T2-GO-4` compacted-key caveat (compaction freezes the snapshot into durable
  genesis state; typed `Codec[V]` + retained-event floor).
- No **per-pod projector as sole fold path** section (`T1-SRE-1`/`T1-SRE-2`).
- No **multi-tenant/session isolation** section (`T1-SEC-1`) nor GDPR KeyStore
  (`T1-SEC-2`).
- No **value-path Store-as-SoT + reconcile-sweep** invariant (`T3/T4-SRE-1`).
- No **in-mem `Backplane` default / `via.InMemory()` / `memevents`** phase-shift
  (in-mem addendum) — Phase 0/1 text still says "nil backplane special-cased."
- Header still `Status: DECIDED` (2026-06-01) — predates all council corrections.

**Next tick:** reconcile `design/state-backplane.md`'s body with the converged
decisions (start with the highest-divergence, correctness-bearing one: the
snapshot/compaction `T2-GO-4` caveat, since the doc currently states the opposite
of the converged design). Bound each tick to one section so diffs stay reviewable.

---

## Tick 6 — 2026-06-04 — reconcile doc body §1: T2-GO-4 (snapshot/compaction)

Chair convened the panel; CONVERGED since tick 4. Tick 5 flagged the doc *body*
still describing the pre-council design. Picked the highest-divergence,
correctness-bearing item first (it stated the OPPOSITE of the converged design):
**T2-GO-4 snapshot/compaction**.

### Done this tick — `design/state-backplane.md` body now matches the tick-3 T2-GO-4 resolution

Fixed 4 places that asserted "snapshot = pure cache, evolving V FREE, no snapshot
upcasters" with no compacted-key caveat (a grep sweep caught the 4th):
- §#6 RESOLVED (the authoritative statement) — full resolution: pure disposable
  cache ONLY for uncompacted keys (mismatch → discard + re-fold from 0); once
  COMPACTED the snapshot is durable genesis → typed `Codec[V]` + version-tagged
  `Checkpoint{epoch,coveredOffset,codecHash,vbytes}`, seeded migration (never
  discard), retained-event floor (≥1 re-foldable generation, ~2× disk),
  epoch-bump for fold-MEANING change / `ErrEpochUnbridgeable`, `WithFoldVerify`
  mandatory before compaction.
- §"Borrowed from the other lenses" synthesis — added the T2-GO-4 cross-ref.
- §Phase 4 — same caveat, terse.
- §lens "what to cut" pitch (line ~659, "No snapshot upcasters, ever") —
  marked **[SUPERSEDED by T2-GO-4]** (preserved the original pitch, flagged it).

Verified: no remaining "pure cache / evolving V free" assertion lacks the
uncompacted-only qualifier.

### Convergence status — still CONVERGED; doc-reconciliation §1 of ~5 CLOSED

### Next step (following tick)

Continue reconciling the body, next-highest divergence. Candidates, ranked:
1. **Typed codecs** (`T1-GO-2`/`T2-GO-4`): the `Codec` interface block (lines
   ~243-256) still declares `Encode(v any) / Decode() any` — the converged design
   is typed `Codec[E]` (events) + `Codec[V]` (snapshots), which also closes the
   no-public-any tension. Surgical change to the code block. **← do next.**
2. Add the **per-pod projector as sole fold path** section (`T1-SRE-1/2`): doc's
   line ~537 still says "Append reuses broadcastRender→SyncNow→Read" as the
   cross-pod mechanism, which tick 1 proved FALSE-as-stated.
3. Add **multi-tenant/session isolation** + GDPR KeyStore sections
   (`T1-SEC-1/2`) — absent entirely.
4. Add **value-path Store-as-SoT + reconcile-sweep** invariant (`T3/T4-SRE-1`).
5. **In-mem `Backplane` default / `via.InMemory()` / `memevents`** phase-shift +
   header `Status` refresh.

---

## Tick 7 — 2026-06-04 — reconcile doc body §2: typed codecs (T1-GO-2 / T2-GO-4)

CONVERGED since tick 4. Per tick 6's ranked next-step list, §2 = typed codecs.

### Done this tick — `design/state-backplane.md` Codec is now generic

The `Codec interface` block declared `Encode(v any) ([]byte,error)` /
`Decode([]byte) (any,error)` — the pre-council untyped shape. Replaced with the
converged generic:
- `type Codec[T any] interface { Encode(v T)…; Decode([]byte) (T,error) }` —
  kills the public `any` (`T1-GO-2`, aligns no-public-any), Decode hands the fold
  a ready `T` with no assertion; `ErrUndecodable` → zero `T` + drop (events only).
- Runtime binds TWO per `StateAppEvents[E]`: event `Codec[E]` + snapshot
  `Codec[V]` (`T2-GO-4` — the version-tagged snapshot codec enabling the seeded
  compacted-key migration). Added the explanatory paragraph.
- Fixed the two supporting refs: `Record.Data` comment ("Codec.Decode → E or
  Change" → event `Codec[E].Decode → E`; Change is runtime-internal) and the
  per-field codec comment (now "event Codec[E] + snapshot Codec[V]").

Verified: no `Encode(any)`/`Decode()any`/bare `type Codec interface` left
(only the comment naming what it replaced).

### Convergence status — still CONVERGED; doc-reconciliation §2 of ~5 CLOSED

### Next step (following tick) — §3: per-pod projector as sole fold path

`T1-SRE-1`/`T1-SRE-2`. Doc still narrates the cross-pod mechanism as "Append
reuses broadcastRender→SyncNow→Read (confirmed path)" (line ~537/551) — tick 1
proved that FALSE-as-stated (`broadcastRender` is pod-LOCAL). The converged design:
`broadcastRender` is the *intra*-pod leg; a per-(pod,key) **projector** tailing
`EventLog.Subscribe(from=highWater)` is the *inter*-pod leg and the **sole fold
path on every pod incl. the writer** (eventual-RYW, `WaitFor` for strict). Add/
correct this; then §4 isolation+KeyStore, §5 value-path sweep + in-mem-default +
header refresh.

---

## Tick 8 — 2026-06-04 — MAINTAINER FINDING: value/sess state must not be pod-local (reconcile §5, promoted)

Maintainer raised it directly: "on multi-node distributed state, makes no sense
that app and sess state is pod-local." Correct — and it coincides exactly with
the converged value-path resolution (T3-SRE-1/T1-SRE-5/T4-SRE-1, ticks 3-4) that
had NOT yet been propagated to the doc (it was reconciliation backlog §5). The
maintainer effectively re-prioritized §5 ahead of §3/§4.

### Chair validation
- Today's code: value state IS pod-local — `kvstore.go` per-pod `sync.Map`,
  `broadcastRender` re-renders local tabs only. Split-brain in multi-node. (Already
  validated tick-1/tick-3, citing kvstore.go:12/17/35, stateapp.go:66-74, broadcast.go:68.)
- Doc Phase 3 (line 637) was the PRE-council version — "other pods tail, re-pull,
  broadcastRender", with NO writer convergence / monotone gate / sweep. It still
  encoded the two bugs the council fixed (T3-SRE-1 writer self-skip, T4-SRE-1
  lost-notify strand). Confirmed absent: grep for "single source of truth /
  reconcile / monotone / storeRev" returned zero doc hits.
- Honest tradeoff surfaced to maintainer: converged model is EVENTUAL consistency
  (L1 cache reconciled to Store-as-SoT), not strong; tab-local StateTab/Signal
  stay correctly pod-local.

### Done this tick — doc body §5 (value-path) propagated
- **Decision bullet** "Value state stays as-is" → "Value state: same API, no
  longer pod-local": Store cell = single source of truth, kvStore = L1 cache,
  writer-included re-pull-to-HEAD, reconcile sweep, removes sticky-session
  requirement, eventual.
- **Phase 3** rewritten to the full converged invariant: Store-as-SoT, L1
  optimistic cache, re-pull-to-HEAD (never to change.rev) gated storeRev ≥
  change.rev, L1 monotone gate, periodic reconcile sweep (correctness independent
  of notify), full-sid exact-match fail-closed, eventual peers / sync-optimistic
  writer RYW. Tagged T3-SRE-1 / T1-SRE-5 / T4-SRE-1.

### Convergence status — still CONVERGED; doc-reconciliation §5 of ~5 CLOSED (out of order, by maintainer priority)

### Remaining doc-reconciliation backlog
- §3 — per-pod projector as sole fold path (T1-SRE-1/2); line ~551 still says
  "Append reuses broadcastRender→SyncNow→Read" as the cross-pod mechanism.
- §4 — dedicated multi-tenant/session ISOLATION section + GDPR KeyStore
  (T1-SEC-1/2) — partially seeded by §5's sid exact-match line; still no
  standalone section.
- header `Status` refresh + in-mem-`Backplane`-default / `via.InMemory()` /
  `memevents` phase-shift (in-mem addendum), not yet in the phased plan.

---

## Tick 9 — 2026-06-04 — reconcile doc body §3: projector as sole fold path (T1-SRE-1/2)

CONVERGED since tick 4. Backlog §3 (the maintainer's §5 jump done in tick 8).

### Done this tick — doc body §3 propagated
The doc still narrated the cross-pod mechanism as "Append folds locally + this
pod marks dirty + every other pod fold-forwards and broadcastRenders" with the
code doing `appendEvent (… + local fold)` then `broadcastRender(ctx, nil, key)`
— i.e. the DUAL-fold-path bug tick 1/2 fixed. Corrected to the converged model:
- **Read godoc** — projection has exactly ONE writer, the per-(pod,key)
  projector; named the two legs (broadcastRender = INTRA-pod, projector tailing
  EventLog.Subscribe = INTER-pod).
- **Append godoc** — Append does NOT fold and does NOT render; projector is the
  SOLE fold path on every pod incl. writer → converge by construction; cross-pod
  RYW EVENTUAL, `WaitFor(key,off)` for strict; single-pod in-process MAY fold
  synchronously (in-mem note).
- **Append code** — dropped the `// + local fold` and the
  `broadcastRender(ctx, nil, key)` call (+ the writer `markStateDirty`); now just
  `appendEvent = Encode + EventLog.Append`, return offset. Projector renders.
- **nil-ctx panic rationale** — reframed: ctx is now the AUTH gate (AUTH-1),
  not the render driver (the projector renders regardless of ctx).
- **"Grounded in code" para** — replaced "Append reuses broadcastRender…
  confirmed path" with the projector-drives-render / broadcastRender-is-pod-local
  framing (cites broadcast.go:60-89, tick-1 finding).

Verified: no "local fold" / `broadcastRender(ctx` left in the doc.

### Convergence status — still CONVERGED; doc-reconciliation §3 CLOSED

### Remaining doc-reconciliation backlog
- §4 — standalone multi-tenant/session ISOLATION section + GDPR KeyStore
  (T1-SEC-1/2); partially seeded (sid exact-match in Phase 3), no section yet.
- §6 — header `Status` refresh + in-mem-`Backplane`-default / `via.InMemory()` /
  `memevents` phase-shift (in-mem addendum), not in the phased plan.
- **NEW (T1-GO-1, found this tick):** the code still uses `E.Zero()` (EventReducer
  iface line ~342, Read line ~379, both examples, cold-start ~591). Convergence
  dropped Zero() → seed = `var zero V`. Distinct API-hygiene tick (ripples to the
  interface + 2 examples), do as its own step.

---

## Tick 10 — 2026-06-04 — reconcile doc body: T1-GO-1 (drop Zero(), seed = var zero V)

CONVERGED since tick 4. Picked T1-GO-1 over §4/§6 because it is a VALIDATED
correctness bug (pointer-E `Zero()` nil-panics, tick 1) still live in the shown
API — buggy spec code outranks a missing section.

### Done this tick — `E.Zero()` removed from the doc (5 sites)
Convergence (tick 2 RESOLVED): drop `Zero()`; seed = `var zero V` (Go zero of the
projection); non-zero empty value → genesis event.
- **EventReducer interface** — removed the `Zero() V` method + its "determinism
  rule #1"; single-method `Fold` now; godoc states seed = `var zero V` and why
  Zero() was dropped (pointer-E nil-deref) + the genesis-event escape.
- **Read code** — `var ev E; zero := ev.Zero()` → `var zero V`.
- **Read godoc** — "seeded by E.Zero()" → "seeded by the Go zero of V".
- **Counter example** — deleted `func (Tick) Zero() int`.
- **Chat example** — deleted `func (ChatEvent) Zero() []Message`.
- **#7 cold-start** — "(or Zero(),0)" → "(or `var zero V`, 0)".

Verified: no `Zero()` call/decl left (only the two explanatory mentions in the
interface godoc + Read comment).

### Convergence status — still CONVERGED; T1-GO-1 CLOSED in doc

### Remaining doc-reconciliation backlog
- §4 — standalone multi-tenant/session ISOLATION section + GDPR KeyStore
  (T1-SEC-1/2); only the sid exact-match line exists (in Phase 3).
- §6 — header `Status` refresh + in-mem-`Backplane`-default / `via.InMemory()` /
  `memevents` phase-shift (in-mem addendum), not yet in the phased plan.

---

## Tick 11 — 2026-06-04 — reconcile doc body §4: isolation + crypto-shred sections (T1-SEC-1/2)

CONVERGED since tick 4. Picked §4 over §6 (security correctness > framing). The
two SEC resolutions existed only as scattered lines (#5 GDPR one-liner, Phase 3
sid exact-match); no standalone section.

### Done this tick — two new sections after #7, before the appendix
- **## Multi-tenant & session isolation (T1-SEC-1)** — physical boundary
  (per-tenant namespace + per-pod creds + mTLS, broker rejects cross-namespace)
  as the LOAD-BEARING layer; logical in-band check (full-sid exact-match,
  unknown sid DROPs fail-closed) as defence-in-depth; AUTH-1 invariant (Append
  only via via_tab+session-gated ctx, action.go:107-117); tier-0 trust posture
  (authn+mTLS mandated, WithInsecureBackplane() opt-out, compromised-pod blast
  radius named, per-event record-signing DEFERRED).
- **## GDPR / right-to-erasure — crypto-shred (T1-SEC-2)** — audit-trail-vs-
  erasure tension named (T1-SEC-WIN); KeyStore{KeyFor,DropKey} seam, DropKey →
  ErrUndecodable → reuses drop-on-undecodable fold no-op; snapshot invalidation
  on erasure (+ the compacted-key durable-genesis caveat from T2-GO-4); audit-
  class vs PII-class separated at declaration; conceded residual (backups expire
  the key out-of-band; key-drop not tombstone is the erasure mechanism).

### Convergence status — still CONVERGED; doc-reconciliation §4 CLOSED

### Remaining doc-reconciliation backlog (last item)
- §6 — header `Status` line refresh (still 2026-06-01 "DECIDED", predates the
  whole council) + the in-mem-`Backplane`-default / `via.InMemory()` / `memevents`
  phase-shift (in-mem addendum: clean in-mem Backplane → Phase 1 default,
  `memevents.Faulty` + parameterized conformance suite → Phase 2). Once done, the
  doc body fully reflects the converged design and the reconciliation pass closes.

---

## Tick 12 — 2026-06-04 — reconcile doc body §6: header refresh + in-mem-default phase shift — RECONCILIATION COMPLETE

CONVERGED since tick 4. Final backlog item (§6).

### Done this tick
- **Header** — `Status: DECIDED` (dated 2026-06-01, pre-council) → `Status:
  CONVERGED`, points at design-council.md for the T*-* issue IDs, dual date
  (scoped 2026-06-01 · council-converged + reconciled 2026-06-04).
- **Decision bullet** (`nil` backplane) — `nil` stays the honest PUBLIC default
  but resolves INTERNALLY to a real in-mem Backplane (`memevents.Backplane` /
  `via.InMemory()`); ONE code path (no nil-special-case bit-rot); hot path
  synchronous + identity-coded so byte-for-byte/zero-overhead/any-T holds;
  projector channel hop only for remote Subscribe records.
- **WithBackplane godoc** — same: nil → via.InMemory() internally, interface
  exercised on every single-pod run.
- **Phase 1** — in-process core implemented AS `memevents.Backplane`
  (via.InMemory()); nil resolves to it; the clean in-mem Backplane lands in
  Phase 1 (where the projector is already built) per the in-mem addendum.
- **Phase 2** — `memevents.Faulty` fault-injecting decorator over the Phase-1
  base + parameterized conformance suite (base / faulty-base / real NATS),
  two-Apps-one-backplane convergence test (T1-TEST-keystone), real-network job
  RELEASE-GATING (non-negotiable). NATS reference backend stays here.

### Convergence status — CONVERGED; doc-reconciliation §6 CLOSED

### ✅ RECONCILIATION PASS COMPLETE (ticks 5-12)
The design doc body now reflects the converged council design end-to-end:
- §rename (t5): Log→EventLog / StateAppLog→StateAppEvents / via.events.* metrics.
- §1 (t6): T2-GO-4 snapshot/compaction (4 sites).
- §2 (t7): typed Codec[E]/Codec[V] (T1-GO-2/T2-GO-4).
- §5 (t8, maintainer-prioritized): value-path Store-as-SoT + reconcile sweep
  (T3-SRE-1/T1-SRE-5/T4-SRE-1) — value/sess state no longer pod-local.
- §3 (t9): projector as sole fold path (T1-SRE-1/2).
- T1-GO-1 (t10): drop Zero(), seed = var zero V (5 sites).
- §4 (t11): isolation + crypto-shred sections (T1-SEC-1/2).
- §6 (t12): header + in-mem-default phase shift.

No known divergence remains between design-council.md and design/state-backplane.md.
Loop has no further reconciliation work; next tick should either (a) declare the
loop complete and stop, or (b) take new maintainer input. Recommend flagging to
the maintainer for a stop decision.

---

## Tick 13 — 2026-06-04 — post-reconciliation COHERENCE AUDIT (round 1)

New loop (cron `5a4adbbf`): "deliberate on current status and next steps, continue
until convergence." Status: design CONVERGED (t4) + reconciliation complete
(t5-12). The meaningful work now is auditing the reconciled doc for internal
contradictions introduced by 8 ticks of piecemeal edits.

### Chair audit this tick — cross-checked the highest-risk junctions
- **Single-fold-path consistency (t9):** CLEAN — every "writer folds / local
  fold / re-render" mention is the corrected form; no surviving dual-fold claim.
- **Source-of-truth contradiction:** FOUND + FIXED. T8 added "the Store cell
  `val:<key>` is the SINGLE SOURCE OF TRUTH" (value path) next to the pre-existing
  "the EventLog is the source of truth; the Store holds only fold-snapshots"
  (line 749, log path). Both correct but scoped to different STATE SHAPES; the
  doc never stated the Store plays two roles → reads as a contradiction. Fix:
  added a disambiguation block to the `Store` godoc — two key classes with
  different authority (`val:<key>` cell = SoT for value state; the EventLog =
  SoT for log state, its Store snapshot is a cache, durable-genesis once
  compacted). Resolves the apparent conflict at its source.

### Convergence verdict — tick 13: NOT YET CONVERGED (one fix this round)
One bounded contradiction found and closed. Deliberation converges when a full
sweep finds nothing new.

### Next tick — coherence audit round 2 (remaining junctions)
- AppendIf/ReadAt vs the `Offset` newtype end-to-end (any surviving bare uint64?
  Read/Append signatures still show `uint64` offset — check vs the "Offset
  end-to-end" T1-GO-3 decision).
- Phase 0 ("nil backplane everywhere → zero observable change") vs Phase 1
  ("implemented AS memevents.Backplane, nil resolves to it") — is Phase 0 still
  coherent, or does the in-mem-default shift change Phase 0's framing?
- OnEvent consumer vs single-fold-path (separate tailer, never writes projection).
- v1 surface claim (Read/Append/Text/Key) vs the code block still showing
  AppendIf/ReadAt/OnEvent — are they clearly marked post-v1/Advanced?
If round 2 is clean → declare the deliberation CONVERGED and recommend exiting
design for implementation (Phase 0).

---

## Tick 14 — 2026-06-04 — coherence audit round 2 (+ round-3 scope)

Continuing the post-reconciliation coherence audit. Two issues found + fixed,
two more found for next round.

### Fixed this tick
- **T1-GO-3 (Offset end-to-end) — was NEVER reconciled.** All four user-facing
  methods still leaked bare `uint64` for offsets (Append/AppendIf/ReadAt/OnEvent)
  while the `Offset` newtype was defined right above them and the BACKEND
  EventLog.AppendIf already used Offset. Fixed all four signatures → `Offset`.
  Verified: zero bare `uint64` left outside the two newtype definitions.
- **v1 surface demarcation (T1-DX-5).** AppendIf/ReadAt/OnEvent were shown inline
  with no marker, reading as v1 surface. Added an "ADVANCED (post-v1)" banner
  before AppendIf/ReadAt (v1 = Read/Append/Text/Key + Counter.Inc) and tagged
  OnEvent (Text sits between them, so the group isn't contiguous).

### Verified clean
- OnEvent vs single-fold-path: separate tailer, never writes the projection. ✓
- Phase 0 "nil backplane → zero observable change": coherent (binding seam,
  pre-core); the in-mem-default shift lives in Phase 1. ✓

### Found for round 3 (NOT yet converged)
- **T1-SRE-3 (Epoch) under-reflected.** `epoch` appears only inside the
  `Checkpoint{}` struct; the converged decision put `Epoch uint64` (generation)
  on the delivered `Record` AND the `Head` return to detect offset-space resets
  (Redis XTRIM-to-empty, recreated stream, PG restore). The `Record` struct
  (~164-168) has no Epoch field; `EventLog.Head` doesn't return it. → add next.
- **T1-DX-2 (StateAppCounter) absent.** The counter call site still shows raw
  `StateAppEvents[Tick,int]` + empty `Tick struct{}` + hand-written Inc + the
  `_, _ =` discard — the exact "worst advertisement" the council voted to replace
  with a `StateAppCounter{StateAppEvents[tick,int64]}` specialization. → add next.

### Convergence verdict — tick 14: NOT CONVERGED (4 issues across rounds 1-2;
2 fixed this tick, 2 queued). The audit keeps finding under-reflected converged
decisions my original §-backlog missed (it focused on the big 7 + named API
items). Round 3 closes Epoch + StateAppCounter; if a subsequent full sweep finds
nothing new → declare the deliberation CONVERGED.

---

## Tick 15 — 2026-06-04 — coherence audit round 3 (Epoch + StateAppCounter)

Closing the two round-2 finds. Both were converged decisions the reconciliation
pass had missed.

### Fixed this tick
- **T1-SRE-3 (Epoch) now on the wire types.** Added `type Epoch uint64`
  (per-key stream GENERATION) next to Offset/Rev; added an `Epoch` field to the
  delivered `Record`; changed `EventLog.Head` to return `(Offset, Epoch, error)`.
  Godocs state the offset-space-reset detection (Redis XTRIM-empty / recreated
  stream / PG restore → epoch change or Head<lastApplied → re-snapshot from
  genesis + via.events.epoch_reset). Previously `epoch` lived ONLY inside the
  Checkpoint struct, so the reset-detection mechanism had no wire carrier.
- **T1-DX-2 (StateAppCounter) shipped in the example.** Replaced the raw
  `StateAppEvents[Tick,int]` counter (user-defined empty `Tick struct{}` + Fold +
  `_, _ =` discard — the council's "worst advertisement") with the built-in
  `via.StateAppCounter` specialization (embeds an UNEXPORTED tick + fold, exposes
  Inc + Read). Added its godoc/decl sketch. Also fixed the illustrative
  "Tick→int" line in the struct godoc → "increments→int64".

### Verified clean
- No orphan user-facing `Tick` references remain (only the internal sketch).
- StateAppCounter wiring consistent with the v1-surface banner ("+ Counter.Inc").
- Epoch wired end-to-end (type + Record field + Head return).

### Convergence verdict — tick 15: NOT YET DECLARED. Rounds 1-3 found+fixed 6
under-reflected converged decisions (source-of-truth dual-role; T1-GO-3 Offset;
v1 demarcation; Epoch; StateAppCounter). The rate is now dropping. Next tick =
round 4: a FULL end-to-end read for anything still stale (candidates: does the
chat example / #5-delete / Change type mention epoch where needed? any remaining
`int` vs `int64` counter mismatch? Backplane godoc "in-memory per-key log" vs the
memevents framing?). If round 4 finds NOTHING new → declare the deliberation
CONVERGED and recommend exiting design for implementation (Phase 0).

---

## Tick 16 — 2026-06-04 — coherence audit round 4 (full sweep)

End-to-end staleness sweep across the whole doc.

### Checked
- counter int vs int64 · Backplane nil godoc vs memevents framing · Change type
  (rev/epoch needs) · StateAppSlice/StateAppNum refs · broadcastRender path
  consistency · epoch in chat/#5-delete/Change paths.

### Result — 1 cosmetic alignment, NO substantive contradiction
- **Fixed (cosmetic):** Backplane `nil` godoc (134-136) predated the tick-12
  in-mem-default framing — said "in-process kvStore + an in-memory per-key log"
  with no mention that nil RESOLVES to via.InMemory()/memevents.Backplane.
  Aligned: nil → via.InMemory(), interface exercised every single-pod run,
  byte-for-byte (synchronous + identity-coded hot path).
- **Reviewed, left as-is (correct):**
  - Appendix "counter→int" pitch lines (628/779) — historical lens theses;
    illustrative, int-vs-int64 immaterial to the pitch (user-facing example uses
    StateAppCounter/int64). Same treatment as other appendix lens records.
  - `Change{key,rev}` is a runtime-INTERNAL control message (decoded by the
    runtime-internal codec, line 178); no public type decl needed; needs no own
    epoch (its Record envelope carries Epoch; the value path's SoT is the Store
    rev via the storeRev≥change.rev gate + reconcile sweep).
  - StateAppSlice refs (554/588/763) all accurate ("TODAY"/migration/parity);
    StateAppNum/StateAppSlice cited as precedent for StateAppCounter (532) — correct.

### Then checked error types — FOUND a real gap (round 4 NOT clean)
The T2-GO-4 prose references `ErrEpochUnbridgeable` and the versioning lens
references a forward-incompat "hard error", but NEITHER was declared in the
`var (...)` error block (only ErrCASConflict/ErrLogConflict/ErrUndecodable/
ErrClosed). Referenced-but-undefined sentinels. **Fixed:** added
`ErrForwardIncompatible` (envelope version newer than the binary → projector
halts, roll-forward-only) and `ErrEpochUnbridgeable` (unbridgeable fold-MEANING
epoch bump → projector halts) to the block with godocs; named
ErrForwardIncompatible in the Phase-4 line.

### Convergence verdict — tick 16: NOT CONVERGED.
Round 4 found 2 issues (cosmetic Backplane godoc + the two undefined error
sentinels) — so the sweep did NOT come back clean. Per "converge when a full
sweep finds NOTHING new," round 5 is required. Round 5: re-read the least-touched
sections — "Current state model (grounding)", #5-delete/GDPR consistency,
Mount/binding details, the metrics named in prose (via.events.undecodable /
.epoch_reset — both emitted at a described site?), and confirm the two new
errors are referenced where they fire. If round 5 is clean → DECLARE CONVERGED,
stop the loop, recommend Phase 0.

---

## Tick 17 — 2026-06-04 — coherence audit round 5 (CLEAN) → ✅ DELIBERATION CONVERGED

Round 5 re-read the least-touched sections + verified the round-3/4 additions.

### All checks CLEAN — nothing new found
- **Metrics have described emission sites:** `via.events.undecodable` (Decode
  godoc + #6), `via.events.epoch_reset` (Epoch godoc). ✓
- **Both new error sentinels referenced where they fire:** `ErrForwardIncompatible`
  (Phase 4), `ErrEpochUnbridgeable` (#6 T2-GO-4 prose). ✓
- **#5-delete ↔ GDPR section consistent:** #5 gives the one-line crypto-shred
  summary; the dedicated T1-SEC-2 section the full treatment. No conflict. ✓
- **Grounding ("Current state model") + Mount/binding** accurate to current code. ✓
- **Full regression sweep — every prior-tick fix intact, ZERO reversions:**
  StateAppLog=0, via.log.*=0, untyped Codec=0 (the 1 hit is the explanatory
  comment), E.Zero() calls=0, bare uint64 in user API=0, Append-local-fold=0
  (the 4-5 "local fold" hits are all the CORRECTED "no local fold" text +
  line-90 value-path grounding, none a regression).

### ✅ CONVERGENCE VERDICT — DELIBERATION CONVERGED (tick 17)
A full end-to-end sweep found NOTHING new. The coherence audit (ticks 13-17,
rounds 1-5) found and fixed 8 issues introduced/left by the reconciliation pass:
source-of-truth dual-role (Store godoc), T1-GO-3 Offset end-to-end (4 sigs),
v1-surface demarcation, T1-SRE-3 Epoch on Record+Head, T1-DX-2 StateAppCounter,
Backplane nil/in-mem-default godoc, and two undefined error sentinels
(ErrForwardIncompatible / ErrEpochUnbridgeable). `design/state-backplane.md` is
now internally coherent AND fully reflects the converged council design.

### Next step — EXIT DESIGN, BEGIN IMPLEMENTATION
No design work remains. Recommended: (1) commit + push ticks 10-17; (2) start
**Phase 0** (binding seam: isStateAppEvents marker + roleStateAppEvents in
walker.go, scopeSlot kind flag, bindScopeKeys/bindApp wiring; nil backplane →
zero observable change), which is the smallest additive, fully-testable slice.

**Loop stopped** (cron `5a4adbbf` deleted) — convergence reached at tick 17.

---

## Tick 18 — 2026-06-04 — ✅ EXIT DESIGN → Phase 0 binding seam SHIPPED

First implementation tick. Design converged at tick 17; this builds the smallest
additive, fully-testable slice of Phase 0.

### Council validation (evidence)
- `walker.go:9-21` fieldRole enum, `classifyField:88-114` dispatch, marker-type
  cache `123-128`, `implements():134-142` — matched the spec's binding-seam
  description exactly.
- `descriptor.go:30-33` `scopeSlot{fieldPath,wireKey}`; `runtime.go:203-212`
  `bindScopeKeys` calls only `bindWireKey`, and runs inside `newCtx:146` BEFORE
  `ctx.app=a` is set (`render.go:19`, `recover.go:143`) — so the app pointer the
  design wants bound into the handle is NOT available at the current seam.
- `stateapp.go:27-43` StateApp reads app via the ctx at call-time;
  `StateAppEvents` (design 386-389) holds its OWN `app *App` — load-bearing for
  the P1 projector that holds no ctx. Intentional divergence, confirmed.

### Phase-boundary refinement (recorded, NOT a spec change)
The loop's PHASE MAP is authoritative on build order: P0 = binding seam, and
P1 explicitly owns "EventReducer, Read (cached projection)... the per-(pod,key)
projector." So `Read`/folding is P1, NOT P0. The design-doc line-774 P0 test
("nil-backplane in-process StateAppEvents folding in RAM") describes the P0+P1
end state; folding lands in P1 with `Read`. The `scopeKind` flag + `bindApp`
app-pointer wiring (also named in the P0 line) have NO observable consumer until
the P1 projector/`Read`, and threading `a *App` through `newCtx` speculatively
can't be TDD'd against any P0-visible behavior — so they ship in P1 with their
first consumer. Each flag lands with the code that reads it. No spec edit
required; this is a slice-granularity decision within the converged design.

### Built (TDD red→green→blue→audit)
- NEW `stateappevents.go`: `StateAppEvents[E EventReducer[E,V], V any]` handle
  (holds `wireKey`); `EventReducer[E,V]` constraint with `Fold(acc V, ev E) V`
  (+ determinism godoc); `bindWireKey`, `Key()`, distinct `isStateAppEvents()`
  marker. No Read/Update/projector/app — deferred to P1.
- `walker.go`: `roleStateAppEvents` enum value; cached
  `stateAppEventsMarkerType`; `isStateAppEventsType` helper; `classifyField`
  branch (after `isStateAppType`, before file/child); `roleStateAppEvents` added
  to the existing `case roleStateSess, roleStateApp:` → binds via the unchanged
  `scopeBinder`/`bindWireKey` path.

### Tests + result
- NEW `stateappevents_test.go`: `TestStateAppEventsKeyBindsThroughMount`
  (default key = lowercase field name "log") + `TestStateAppEventsKeyHonorsViaTag`
  (via:"events" override). The default+tag PAIR defeats any hardcoded-constant
  `Key()` (no single literal satisfies both; a no-op `bindWireKey` leaves
  wireKey="" → test 1 fails) — the pair IS the dynamic-binding proof.
- Yellow agent: flagged hardcode risk → answered by the default/tag pair;
  nested/pointer cases exercise walkStruct's pre-existing recursion (shared by
  all roles), not this slice's new branch → not added.
- Blue agent: every added symbol load-bearing (constraint + marker required for
  compile/reflection), no dead code.
- Audit agent: no mis-classification possible (3 method-name-distinct unexported
  markers, flat structs, no embedding); classifyField ordering safe; generic
  self-referential constraint + pointer-receiver marker detected on the
  instantiated type; zero observable change to existing classification.
- `go vet ./` clean; `go test -race ./...` ALL GREEN.

### Next step — Phase 0 → Phase 1 (in-process core)
P1 is the first slice with real behavior: `EventReducer` fold actually applied,
`Read` (cached projection seeded by zero V), plain `Append` (Codec encode +
EventLog.Append, NO local fold), `Text`, the per-(pod,key) projector goroutine,
and `StateAppCounter`. This is where `scopeKind` + `bindApp` + threading `a`
through `newCtx` land (the projector is their first consumer). Then migrate the
chat example and delete the trim-Update. P1 should be sub-sliced — likely
(1a) Append+Read RAM fold via projector under a nil backplane, (1b) StateAppCounter,
(1c) chat migration — each its own TDD tick.
