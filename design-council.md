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

---

## Tick 19 — 2026-06-04 — Phase 1 START: design-gate (T1-GO-6) + converge P1.0 (in-memory Backplane foundation)

P0 committed (`feat(state): add StateAppEvents binding seam`). Phase 1 = the
in-process core. Sub-sliced; this tick builds the foundation.

### DESIGN FEEDBACK GATE — T1-GO-6 (import cycle in the memevents framing)
- **Claim in spec:** the base in-memory impl is `memevents.Backplane`, and
  `via.InMemory()` (+ the nil-resolution) returns it (spec lines 39, 136, 337,
  775).
- **Disproved by Go import rules:** a separate `memevents` package MUST import
  `via` to implement the `via.Backplane` interface (so `WithBackplane(b
  Backplane)` accepts it). `via.InMemory()` / the nil-resolution live IN package
  `via` and must CONSTRUCT the concrete impl → `via` imports `memevents` →
  CYCLE.
- **Resolution:** the BASE in-memory impl lives in package `via` itself
  (unexported types; `via.InMemory()` returns it). The `memevents` package
  (Phase 2) still exists for the `Faulty` fault-injecting DECORATOR + the
  conformance suite — those only WRAP a `via.Backplane`, the cycle-free
  `memevents`→`via` direction. Spec patched at all four sites (T1-GO-6 note
  added). No behavioral change; package-placement only.

### Converged slice — P1.0: the in-memory Backplane foundation (NO StateAppEvents wiring)
Smallest meaningful, fully-unit-testable unit; zero via-runtime wiring (the
App.backplane field + WithBackplane Option + nil-resolution + the projector +
StateAppEvents.Append/Read land in the NEXT P1 sub-tick, where they have
observable consumers).

- **Files:** new `backplane.go` (public contract) + `inmemory.go` (impl) +
  `inmemory_test.go`.
- **Public types (package via):** `Offset/Rev/Epoch uint64`, `Record`,
  `Store` (LoadSnapshot + CAS), `EventLog` (Append + Subscribe + Head),
  `Backplane` (Store + EventLog + io.Closer), `InMemory() Backplane`.
  Errors added only as consumed: `ErrCASConflict`, `ErrClosed`.
- **v1-scope exclusions (recorded):** `EventLog.AppendIf` is DEFERRED — it is the
  backend primitive for the Advanced `StateAppEvents.AppendIf` (post-v1 per
  guardrail), exercised by nothing in v1; adding it to the interface later is
  safe (no external backend impls exist yet). `Compactor` is P5. `Codec` lands
  with the encode path (next sub-tick). The other error sentinels
  (ErrLogConflict/ErrUndecodable/ErrForwardIncompatible/ErrEpochUnbridgeable)
  land with their first code path.
- **Acceptance (load-bearing guarantees, tested through the public Backplane):**
  (1) Append assigns a monotone per-key Offset starting at 1; Head reports it.
  (2) Subscribe(from:K) replays every record with Offset>K in per-key total
  order, then live-tails new Appends — the resumability that retires #3/#7.
  (3) Per-key streams are independent (no cross-key ordering).
  (4) Store CAS: Rev(0)=must-not-exist; matching expectedRev advances rev;
  stale expectedRev → ErrCASConflict, cell unchanged.
  (5) After Close, Append/Subscribe return ErrClosed and never block; live
  subscribers' channels close. No goroutine leak (subscriber goroutine exits on
  ctx-cancel or Close).
- **Concurrency design:** per-key `memLog` guarded by its own mutex; a
  broadcast via a "changed" channel closed+replaced on each Append; the
  subscriber goroutine selects on {changed, ctx.Done()} and copies the new tail
  under lock — standard race-clean broadcast idiom, validated with -race.

Next: TDD this slice, then the wiring sub-tick (App.backplane + WithBackplane +
nil→InMemory + projector + StateAppEvents.Append/Read + Codec + scopeKind/bindApp).

### Tick 19 — BUILT + result
- NEW `backplane.go`: public contract — `Offset/Rev/Epoch`, `Record`, `Store`,
  `EventLog` (Append/Subscribe/Head; AppendIf deferred), `Backplane`,
  `InMemory()`, `ErrCASConflict`/`ErrClosed`.
- NEW `inmemory.go`: `inMemoryBackplane` — per-key CAS cell + per-key append log
  with a broadcast `changed` channel (closed+replaced under `lg.mu` on append)
  and a per-subscriber goroutine selecting on {changed, ctx.Done(), closeCh}.
- Tests: `inmemory_test.go` (8 public) + `inmemory_internal_test.go` (1).
  Guarantees locked: monotone per-key offset from 1 + Head HWM; Subscribe(from:K)
  replay-Offset>K-in-order + genuine live-tail-blocks (expectQuiet) + genesis
  full-replay; per-key independence; CAS Rev(0)=must-not-exist + stale→
  ErrCASConflict-unchanged; ctx-cancel unwind; Close→ErrClosed + subscriber
  close. `go test -race ./...` ALL GREEN, vet clean.
- Yellow: caught a hardcodable Subscribe + assert/require misuse → fixed
  (expectQuiet block-proof, genesis full-replay, require on offsets, ctx-cancel
  test added).
- Blue: flagged Epoch return / defensive copies / Close-idempotence as
  load-bearing-but-untested → locked each with a test instead of deleting needed
  safety (TestAppendOwnsACopyOfTheRecordBytes, TestCloseIsIdempotent,
  TestHeadReportsGenesisEpoch).
- Audit: found + FIXED a real Close-vs-Append race (an Append racing a
  concurrent Close on an existing log could commit a lost write after Close
  completed). Fix: `memLog.closed` re-checked under `lg.mu` in `memLog.append`;
  `Close` now calls `lg.close()` per log. Re-verified green.

### Next step — P1.1: wire the in-memory Backplane into the runtime
App.backplane field + `WithBackplane(b Backplane) Option` + nil→InMemory()
resolution at New/Mount; then the encode path (Codec[E], default JSON) +
StateAppEvents.Append (Codec.Encode→EventLog.Append, no local fold) + the
per-(pod,key) projector goroutine (Subscribe→fold→broadcastRender) + Read
(cached projection) + Text. This is where scopeKind + bindApp + threading the
App through newCtx finally land (the projector is their first consumer). Sub-
slice further if needed (e.g. wiring+Append+synchronous-fold Read first, then
the async projector tail).

---

## Tick 20 — 2026-06-04 — P1.1a: App-level Backplane DI (WithBackplane + nil→InMemory + Shutdown drain)

P1.0 committed (`ff5a864`). Phase 1 continues: wire the backplane into the
runtime. Sub-sliced; this tick does the App-level dependency injection only.

### Council validation (evidence)
- `config.go:19-45` config struct + `config.go:49` `Option func(*config)`; the
  WithX family (`config.go:52+`) is the additive pattern to match.
- `app.go:280-313` `New` applies opts then sets defaults — the spot to resolve a
  nil backplane to `InMemory()`. App fields live on the struct (`app.go:30-69`,
  e.g. `appStore kvStore`); add `backplane Backplane` there.
- `server.go:79-122` `Shutdown` is the graceful-drain path (handles srv==nil in
  tests) — the design's "io.Closer: graceful drain on App.Shutdown". Add the
  backplane Close near the tail (after sweep stop), guarded for nil.
- `broadcast.go:60` `broadcastRender(skip,sess,key)` + `ctx.go:414/426`
  trackRead/subscribed are the projector's render path — NEXT slice, not this one.

### Converged slice — P1.1a (smallest with a clean observable)
- **Files:** `config.go` (field + WithBackplane), `app.go` (App.backplane field +
  nil→InMemory in New), `server.go` (Shutdown drains it). New test in
  `backplane_wire_test.go`.
- **Acceptance:** (1) `WithBackplane(bp)` stores bp and `Shutdown(ctx)` Closes it
  (a caller's reference to bp returns ErrClosed on Append afterward). (2) A
  default `via.New()` (no WithBackplane) Shutsdown cleanly (its resolved InMemory
  is closed) — nil→InMemory resolution. (3) Existing suite green.
- **Deferred to P1.1b:** `bindApp` + `scopeKind` + threading the App through
  `newCtx` + the per-key projector + `Read`/`Text`/`Append`. Their observable is
  `Read`, which doesn't exist yet — so per TDD they land with the projector slice.

### Open design wrinkle to resolve in P1.1b (flagged, not yet blocking)
The spec says the in-memory hot path is "identity-coded (no JSON in-process)"
(lines 40/135/775) but `EventLog.Append` moves `[]byte` — an arbitrary E can't
become []byte without serialization. Resolution candidates for next tick: (a)
runtime always Codec[E].Encode→[]byte (uniform one-code-path; JSON default;
treat "no JSON in-process" as a deferred perf optimization), vs (b) the typed
handle registers typed closures at bindApp so the in-memory path folds E
directly. Also unresolved: HOW the runtime obtains a typed Codec[E]/Fold from a
reflection-detected, type-erased field — likely the `bindApp` method on the
typed handle registering typed closures with the App. To be converged in P1.1b
with code evidence (DESIGN FEEDBACK GATE candidate).

### Tick 20 — BUILT + result
- `config.go`: `backplane Backplane` field + `WithBackplane(b Backplane) Option`.
- `app.go`: `App.backplane` field; New() resolves `a.cfg.backplane`, nil→`InMemory()`.
- `server.go`: Shutdown drains `a.backplane.Close()` (guarded) after the existing
  dispose/sweep/sessions steps.
- Tests `backplane_wire_test.go`: `TestWithBackplaneIsDrainedOnShutdown`
  (caller's bp → ErrClosed after Shutdown — proves stored+drained) +
  `TestDefaultAppShutsDownCleanlyWithBackplaneDrain` (regression guard).
  `go test -race ./...` GREEN, vet clean.
- Yellow: caught that the default-app test couldn't prove nil→InMemory black-box
  (no public App.Append; deferred to P1.1b) → reframed it honestly as a Shutdown
  regression guard; nil→InMemory is DI-wiring verified by build + P1.1b.
- Blue: all reachable code COVERED or DI-wiring-exempt; no dead code.
- Audit: no bugs, no changes. Future-safety NOTES (not present bugs): (a)
  Shutdown closing the backplane LAST is correct now and future-safe (disposeCtx
  runs before Close); (b) plugin `Register` runs BEFORE the nil→InMemory
  resolution — fine today (no plugin reads a.backplane), but if a future plugin
  needs the backplane at Register time, move the resolution before the plugin
  loop. Double-Shutdown is safe (InMemory.Close idempotent).

### Next step — P1.1b: the typed projector + Read/Append (the heart of P1)
Converge the two open design questions FIRST (with code evidence): (1) how the
runtime obtains a typed Codec[E]/Fold from a reflection-detected, type-erased
StateAppEvents field — likely the typed `bindApp` method registering typed
closures with the App; (2) the "no JSON in-process / identity-coded" claim vs
EventLog moving []byte — decide uniform-Codec-encode (one code path) vs an
in-mem identity fast-path. Then build: scopeKind + bindApp + thread App through
newCtx, the per-(pod,key) projector goroutine (Subscribe→fold→broadcastRender),
StateAppEvents.Append (encode→EventLog.Append, no local fold), Read (cached
projection + trackRead), Text. Observable: an action Appends an event, the
projector folds it, a subscribed tab re-renders showing the folded value.

---

## Tick 21 — 2026-06-04 — P1.1b convergence: typed projector + Append/Read (heart of P1)

P1.1a committed (`1ccd779`). This is the irreducible in-process core. Two open
design questions from tick 20 RESOLVED with code evidence; no spec contradiction
(both are under-specified mechanics, not disproven claims — recorded as notes).

### Validated against code
- Spec is firm (lines 469-505, 646; T1-SRE-2): **Append never folds** — only
  `Encode + EventLog.Append`; the per-(pod,key) projector tailing
  `EventLog.Subscribe` is the SOLE fold path and drives intra-pod render via
  `broadcastRender(skip=nil, sess, key)` (`broadcast.go:60`). No synchronous
  fold-in-Append (it would diverge peers on a non-commutative fold).
- `broadcast.go:60-75`: skip=nil includes all tabs; sess=nil = app-wide; skips
  `!subscribed(key)`. `ctx.go:414/426` trackRead/subscribed reused verbatim by
  Read (parity with `stateapp.go:36`). `vt.AwaitFrame` (stateapp_test.go:107)
  makes the async projector deterministically testable.
- `render.go:18`+`recover.go:142` are the ONLY newCtx callers; each sets
  ctx.app=a immediately after → threading `a` into newCtx is safe & behavior-
  preserving (existing render/rebootstrap tests are the characterization).

### T1-GO-7 — in-mem codec / "no JSON in-process"
P1 uses plain `encoding/json` Marshal (Append, for the durable EventLog bytes) +
Unmarshal (projector decode → E, then E.Fold). The full versioned `Codec[E]`
ENVELOPE + upcaster chain is Phase 4 (versioning) — NOT built now. The
"identity-coded / zero-serialization in-process" promise (spec 147) is a
backward-compat guarantee for the EXISTING StateApp value path (P3) and a future
StateAppEvents optimization; new StateAppEvents may JSON-encode in v1. No spec
edit — the claim stands for its actual scope (value path).

### T1-GO-8 — typed bridge from a type-erased reflected field
The walker detects the field type-erased (P0). The TYPED `bindApp(*App)` method
on `StateAppEvents[E,V]` (it has E,V in scope) registers with the App, keyed by
wireKey: the seed (zero V) + a `foldBytes(acc any, data []byte) (any,error)`
closure (json.Unmarshal→E, then `e.Fold(acc.(V), e)`), and starts the per-key
projector via `sync.Once`. So the App holds only type-erased `any`/closures; all
generic work is captured in the closure at bind time. Append/Read are typed
handle methods (encode/cast directly). Projector lifetime = until
backplane.Close (Subscribe channel closes → goroutine exits; Shutdown drains).

### Converged slice — P1.1b (one coherent, fully-testable unit)
- **Files:** `descriptor.go` (scopeSlot.kind + scopeKind type), `walker.go`
  (set kind=scopeLog for roleStateAppEvents), `runtime.go` (thread `a *App` into
  newCtx; bindScopeKeys binds app on scopeLog slots via an appBinder iface),
  `render.go`+`recover.go` (pass `a` to newCtx, drop the separate ctx.app=a),
  `stateappevents.go` (add `app *App`, bindApp, Append, Read, Text), new
  `applog.go` (App.logState map + registerLog + startProjector + logProjection),
  `app.go` (logState map field), test `stateappevents_runtime_test.go`.
- **Acceptance:** a composition with `StateAppEvents[E,V]`; an action calls
  Append(ev); a SECOND subscribed client's SSE frame (AwaitFrame) shows the
  folded V; a fresh page GET also shows it (projection is app-wide, survives the
  tab). nil ctx → Append panics (AUTH parity w/ StateApp.Update). Existing suite
  green + race-clean.
- **Scope guard:** no AppendIf/ReadAt/OnEvent; no Codec envelope; no
  StateAppCounter yet (next sub-tick); no snapshot/compaction.

### Tick 21 — BUILT + result
- `descriptor.go`: scopeKind (scopeValue/scopeLog) + scopeSlot.kind + appBinder iface.
- `walker.go`: log slots get kind=scopeLog.
- `runtime.go`: newCtx(*App,...) sets ctx.app; bindScopeKeys(*App) calls bindApp
  on scopeLog slots. `render.go`/`recover.go`: pass a, drop the redundant ctx.app=a.
- `app.go`: `logs map[string]*logState` + logsMu, initialized in New.
- NEW `applog.go`: logState (projection/cursor/seed/foldBytes/once), registerLog
  (get-or-create + sync.Once projector start), startProjector (Subscribe→cursor-
  gated fold→broadcastRender(nil,nil,key)), logProjection.
- `stateappevents.go`: app field; bindApp (typed seed+foldBytes closure → registerLog,
  T1-GO-8 bridge); Append (json.Marshal→backplane.Append, no fold, panics on nil
  ctx); Read (trackRead+logProjection→V); Text. JSON codec inline (Codec envelope=P4).
- Tests `stateappevents_runtime_test.go` (5): empty-projection zero-value;
  appended-event folds + reaches a live SSE subscriber (AwaitFrame); projection
  app-scoped + outlives writer (two appends → "hello,hello", which also catches
  double-fold); Append nil-ctx panic; Text renders projection.
  `go test -race ./...` ALL GREEN, vet clean.
- Yellow: added the missing empty-projection test; noted projector-vs-local-fold
  is NOT black-box distinguishable single-pod (locked by the P2 two-Apps test);
  confirmed double-fold IS caught by the two-append test.
- Blue: Text was dead-for-slice → locked with a test (v1 surface). Remaining
  untested branches are defensive I/O/contract-violation guards (parity w/
  StateApp's own untested nil guards) — acceptable.
- Audit: CLEAN, no bugs/changes. Verified: no projector goroutine leak (Shutdown
  →Close→channel close→range exits); projection RW-guarded; broadcastRender
  called OUTSIDE ls.mu; offset gate has no off-by-one (offset 1 folded once from
  zero-V seed); registerLog idempotent across tabs (create-only + once); ctx.app
  early-set is the sole assignment, no double-set.

### Next step — P1.2: StateAppCounter specialization
Built-in `via.StateAppCounter` = `StateAppEvents[tick, int64]` with an UNEXPORTED
tick event + fold, exposing Inc(ctx) + Read (no event type / Fold / offset
ceremony for the user). Then P1.3: migrate internal/examples/chat to
StateAppEvents + delete the trim-Update.

---

## Tick 22 — 2026-06-04 — P1.2: StateAppCounter specialization

P1.1b committed (`031ce1a`) — StateAppEvents works end-to-end in-process. This
tick ships the built-in counter so the ubiquitous shared-counter case needs no
user-defined event type / Fold (T1-DX-2).

### Validated against code
- `shape_num.go:102` `StateAppNum[T] struct{ StateApp[T] }` is the EXACT
  embed-and-promote precedent; walker.go:131-133 comment confirms markers
  "promote across embedded specialized wrappers for free". So
  `StateAppCounter struct{ StateAppEvents[counterTick,int64] }` reuses the proven
  path: `implements(StateAppCounter, stateAppEventsMarkerType)` is true via
  pointer-receiver promotion (StateAppCounter is in viaPkgPath ✓), so the walker
  classifies a `Hits via.StateAppCounter` field as roleStateAppEvents and binds
  it (bindWireKey + bindApp promoted). No walker/runtime change needed.
- The increment event is UNEXPORTED → `Append(ctx, counterTick{})` is promoted
  but un-callable from outside the package (caller can't name the type), so Inc
  is the only append path. Read/Text/Key promote and are the intended surface.

### Converged slice — P1.2
- **File:** new `stateappcounter.go`. `counterTick struct{}` (unexported) +
  `func (counterTick) Fold(acc int64, _ counterTick) int64 { return acc + 1 }`;
  `type StateAppCounter struct{ StateAppEvents[counterTick, int64] }`;
  `func (c *StateAppCounter) Inc(ctx *Ctx) { _, _ = c.Append(ctx, counterTick{}) }`.
- **Acceptance:** a page with `Hits via.StateAppCounter`; an Inc action; Read
  defaults to 0, increments by 1 per Inc; a live SSE subscriber sees the bump;
  a fresh session sees the accumulated count (app-scoped). Existing suite green.
- **Scope:** no Dec/Add (monotone counter is the v1 surface per the spec name);
  those can be later StateAppEvents with a signed-delta event if wanted.

### Tick 22 — BUILT + result
- NEW `stateappcounter.go`: `counterTick struct{}` (unexported) +
  `Fold(acc int64,_)int64 = acc+1`; `StateAppCounter struct{ StateAppEvents
  [counterTick,int64] }`; `Inc(ctx)`. Zero walker/runtime change — the embed
  promotes the markers exactly like StateAppNum (confirmed by Audit: walker's
  `implements` tests PointerTo(t), and pointer-receiver methods of the embedded
  value promote to *StateAppCounter).
- Tests `stateappcounter_test.go` (4): reads 0 before any Inc; 3 Inc → fresh
  session sees 3 (app-scoped + exact +1 fold, defeats per-tab/wrong-fold/
  hardcoded-0); Inc reaches a live SSE subscriber; Inc panics on nil ctx.
  `go test -race ./...` ALL GREEN, vet clean.
- Yellow: per-test "weak" ratings defeated by the suite collectively (fresh
  session seeing 3 proves app-scope, kills hardcoded-0 + per-tab + always-1).
- Blue: no dead code; flagged Inc-nil-ctx panic untested → added the test.
- Audit: CLEAN, no bugs. Confirmed embed promotion-through-pointer, `{}` json
  round-trip = exactly +1 (no skip path), per-key projection isolation despite
  the shared counterTick type, Inc's `_,_=` error-drop acceptable (append-only,
  no conflict; nil-backplane is a documented no-op).

### Phase 1 status — P1.0✅ P1.1a✅ P1.1b✅ P1.2✅. Remaining: P1.3.
### Next step — P1.3: migrate internal/examples/chat to StateAppEvents + delete the trim-Update
The chat example currently models its message list as a value-shaped StateApp
with a trim-on-Update (the "worst advertisement" the council flagged). Replace
with StateAppEvents[ChatEvent, []Message] (append a message event; fold builds
the list), deleting the trim-Update. Validate the chat example builds + its
tests pass; verify in-browser if feasible. This closes Phase 1.

---

## Tick 23 — 2026-06-04 — P1.3: migrate chat example to StateAppEvents + delete trim-Update (CLOSES PHASE 1)

P1.2 committed (`e469e2e`). Final P1 slice: replace the chat example's value-
shaped `StateAppSlice[Message]` + `Op.Append` + trim-`Update` (the council's
"worst advertisement") with `StateAppEvents[ChatEvent, []Message]`.

### Validated against code
- `internal/examples/chat/main.go`: Room.Log is `via.StateAppSlice[Message]`
  (=StateApp[[]Message], shape_slice.go:122); Send does `Log.Op(ctx).Append(msg)`
  then a trim-`Update` capping at recentWindow=50, then `Draft.Write(ctx,"")`.
  View `h.Each(r.Log.Read(ctx), ...)`.
- Existing test `TestChat_messageFansOutAcrossSessions` exercises the full
  fan-out path (alice Send → bob's SSE frame shows it) — the characterization
  guard. Baseline GREEN.
- Behavior is PRESERVED (live cross-session fan-out, bounded render window), so
  this is a refactor: keep the fan-out test green throughout; add a fold-ordering
  test for the new event-sourced list-building.

### Converged slice — P1.3
- **Files:** `internal/examples/chat/main.go` (Room.Log → StateAppEvents; add
  `ChatEvent{From,Body}` + Fold that copies acc, appends, trims to recentWindow;
  Send → `Log.Append(ctx, ChatEvent{...})`, delete the trim-Update + Op.Append),
  `internal/examples/chat/main_test.go` (update the stale "StateApp fan-out"
  comment; add a multi-message ordering/accumulation test).
- **Note:** the SENDER's own re-render becomes eventual (projector hop) vs the
  old synchronous value-update — single-pod in-memory the hop is ~instant; the
  fan-out test asserts the receiver (bob) which is unaffected. The event LOG now
  grows unbounded (trim is only on the projection); true log bounding is P5
  (snapshot+compaction) — fine for the example.

### Tick 23 — BUILT + result → ✅ PHASE 1 COMPLETE
- `internal/examples/chat/main.go`: Room.Log is now
  `via.StateAppEvents[Posted, []Message]`; added `Posted{From,Body}` + a pure
  `Fold` (copies acc, appends, trims to recentWindow); Send Appends a Posted and
  drops the old `Op.Append` + trim-`Update`. Doc comment updated to the
  event-log framing.
- `internal/examples/chat/main_test.go`: fixed the stale "StateApp fan-out"
  comment; added `TestChat_messagesAccumulateInOrder` (two senders → fresh reader
  sees both in send order, proving the fold appends + app-scope).
- Handled as a REFACTOR (behavior preserved: live cross-session fan-out + bounded
  render), NOT the full tdd skill: the existing fan-out test (real HTTP+SSE via
  vt, same path a browser hits) stayed green throughout; the new ordering test
  strengthens coverage. `go vet` + `go test -race ./...` ALL GREEN (11 pkgs).
- Browser verify skipped: the vt fan-out test drives the identical real-SSE
  projector path; marginal added confidence not worth the cost this tick.

### ✅ PHASE 1 COMPLETE — in-process StateAppEvents core ships green
P1.0 in-mem Backplane · P1.1a App DI · P1.1b projector+Append/Read+Text ·
P1.2 StateAppCounter · P1.3 chat migration + trim-Update deleted. The
StateAppEvents event-log model works end-to-end single-pod on via.InMemory(),
race-clean, and the flagship chat example demonstrates it.

### Next step — PHASE 2: Backplane interface hardening + fault injection + conformance + NATS
P2 is large; sub-slice. Likely order: (2a) the parameterized conformance suite
run against via.InMemory() (codifying the EventLog/Store guarantees as a reusable
suite); (2b) `memevents.Faulty` fault-injecting decorator (reorder-within-
allowance, redelivery, mid-Subscribe disconnect) + run the suite against it;
(2c) the two-Apps-one-backplane in-process cross-pod CONVERGENCE test (the
T1-TEST keystone — this is what actually proves the projector, not a local fold,
is the cross-pod mechanism); (2d) the NATS JetStream+KV reference backend
(RELEASE-GATING, real-network) + run the conformance suite against it. NOTE 2d
needs a real NATS server (external dep) — flag to maintainer; 2a-2c are infra-
free and can proceed now.

---

## Tick 24 — 2026-06-04 — P2a: parameterized Backplane conformance suite (infra-free)

Phase 1 done (`b816b86`). Phase 2 begins. P2 is large; this tick builds the
reusable conformance harness and runs it against via.InMemory().

### Validated against code/spec
- `design:155-166`: Offset is OPAQUE — comparable/ordered WITHIN a key, NOT
  gap-free, NOT interchangeable across backends; Offset(0)=before-first. ∴ the
  suite must assert monotone-INCREASING + Head==last-appended + Offset(0)=empty +
  resumable-from-a-RETURNED-offset + gap-free in-order delivery — NEVER hardcoded
  offset values (that would bake in InMemory's dense-from-1 detail and reject a
  valid Kafka/JetStream backend).
- `design:785` lists conformance coverage: ordering, gap-free resume, CAS
  conflict, snapshot-before-compact, offset-space reset. Snapshot/compact is P5
  and Epoch-reset is P4 — those conformance checks are ADDED when those features
  land. P2a covers: ordering, gap-free resume + live-tail, per-key independence,
  CAS conflict, Close→ErrClosed.

### Design refinement (T1-TEST-1, package placement)
Tick-19 loosely put the conformance suite in `memevents`. Refined: the suite
lives in its OWN package `github.com/go-via/via/backplanetest` (idiomatic like
fstest/iotest/httptest — a neutral harness any Backplane author imports);
`memevents` is reserved for the `Faulty` decorator (2b). Cleaner for a NATS
author than importing an "in-memory-events" package to test their backend. No
cycle (backplanetest→via only). No spec text contradicted (spec never named the
package); recorded here.

### Converged slice — P2a
- **Files:** new `backplanetest/conformance.go`
  (`RunConformance(t *testing.T, newBackplane func() via.Backplane)` + helpers,
  imports testing like fstest) + `backplanetest/conformance_test.go`
  (`TestInMemoryConformance` → RunConformance(t, func()via.Backplane{return
  via.InMemory()})`).
- **Acceptance:** the suite's subtests (monotone offsets + Head + empty-Head;
  genesis replay in order; resume-after-offset; live-tail; per-key independence;
  CAS must-not-exist/conflict/advance; Close→ErrClosed) all pass against
  via.InMemory(); whole module green + race-clean. Backend-agnostic (offsets read
  from Append returns, never hardcoded).
- This is TEST-HARNESS code (imports testing, no app behavior) → written
  directly + Explore-reviewed for contract-fidelity, not the full tdd skill.

### Tick 24 — BUILT + result
- NEW package `backplanetest` (`conformance.go` + `conformance_test.go`):
  `RunConformance(t, newBackplane func() via.Backplane)` — 9 backend-agnostic
  subtests: increasing offsets + Head tracking + empty-Head=0; genesis replay in
  order; resume strictly after a returned offset; live-tail; per-key
  independence; CONCURRENT appends get distinct offsets + Head=max (#2 total
  order); independent subscribers see the same stream/offsets; CAS
  must-not-exist/conflict/advance; Close→ErrClosed. `TestInMemoryConformance`
  runs it against via.InMemory(). vet + `go test -race ./...` GREEN (12 pkgs).
- Offsets read from Append returns, never hardcoded → a non-dense
  Kafka/JetStream/Postgres backend conforms.
- Explore review confirmed: contract-faithful (no baked-in dense-from-1), CATCHES
  all 5 broken-backend classes (out-of-order, no-resume, CAS-bypass, Head-stale,
  no-live-tail), all reads timeout-bounded (recvTimeout=3s, fine for real
  network). Acted on its two HIGH/MEDIUM gaps → added the concurrent-appends +
  multi-subscriber subtests. Deferred (correctly): Epoch checks (P4), durability-
  across-close (P5/integration).

### Next step — P2b: memevents.Faulty fault-injecting decorator
A decorator over any via.Backplane (wrapping via.InMemory() in tests) injecting
controllable redelivery, mid-Subscribe disconnect, and reorder-within-allowance
— then run RunConformance against it (the suite must still pass under
at-least-once redelivery + reconnect, proving the runtime's offset-dedup/resume
assumptions). Package `memevents`. After that: P2c two-Apps-one-backplane
cross-pod convergence test (the keystone). P2d NATS = real-network, RELEASE-
GATING, needs an external server → FLAG TO MAINTAINER before attempting.

---

## Tick 25 — 2026-06-04 — P2b: memevents.Faulty redelivery decorator + at-least-once-tolerant conformance

P2a committed (`2d33586`). This tick adds the fault-injecting decorator and runs
the conformance suite against it.

### DESIGN FEEDBACK GATE — T1-TEST-2 (conformance suite was over-strict)
- **Finding:** P2a's RunConformance asserts EXACTLY-once delivery (genesis sees
  a,b,c exactly). But the EventLog contract is AT-LEAST-once (`design:233-235`:
  "Redelivery after reconnect is possible (hence at-least-once); the runtime
  dedupes by Offset") and line 785 mandates the SAME suite pass against
  faulty-base + NATS, which redeliver. So the suite over-asserted — it would
  reject a conforming at-least-once backend. The runtime already tolerates this:
  the projector cursor-gates (`applog.go:49 rec.Offset > ls.cursor`).
- **Resolution:** make the Subscribe-consuming conformance assertions dedupe by
  offset and assert FIRST-delivery-in-increasing-order (a `collectDistinct`
  helper), tolerating in-order duplicates. InMemory (exactly-once) is a subset →
  still green. No spec edit (the spec already says at-least-once); the suite is
  brought into line. Recorded as T1-TEST-2.

### Scope decision — redelivery only this tick
Spec line 785 lists Faulty faults: reorder-within-allowance, redelivery,
mid-Subscribe disconnect. P2b implements REDELIVERY (in-order duplicate of each
record) — the clean, conformance-suite-relevant fault. DISCONNECT is deferred: a
closed channel is TERMINAL per the EventLog godoc ("closes on ctx-cancel or
unrecoverable error"; transient reconnect is the backend's own concern), so it
is a runtime-resilience test, not a raw-backend conformance case. REORDER within
a key would violate the hard per-key total-order guarantee, so it is not
injected (the "within-allowance" is the redelivery window, modeled as in-order
dups). Both deferred faults recorded for a later slice.

### Converged slice — P2b
- **Files:** refactor `backplanetest/conformance.go` (collectDistinct helper;
  make genesis/resume/livetail/perKey/multiSubscriber tests dedupe-by-offset).
  New package `memevents` (`faulty.go`: `Faulty` decorator over a via.Backplane
  with a Redeliver count; wraps Subscribe to emit each record Redeliver+1 times
  in order; delegates Store/Append/Head/Close) + `faulty_test.go`
  (RunConformance against Faulty{InMemory(), Redeliver:1}).
- **Acceptance:** RunConformance passes against BOTH via.InMemory() AND
  Faulty(InMemory, Redeliver:1); whole module green + race-clean. The Faulty
  Subscribe wrapper leaks no goroutine (exits when the underlying channel closes
  or ctx cancels).

### Tick 25 — BUILT + result
- Refactored `backplanetest/conformance.go`: added `collectDistinct` (dedupe by
  offset, assert first-delivery-in-increasing-order); genesis/resume/livetail/
  perKey/multiSubscriber tests now at-least-once-tolerant. InMemory conformance
  stayed GREEN (exactly-once is a subset).
- NEW package `memevents` (`faulty.go`): `Faulty struct{ via.Backplane;
  Redeliver int }` — embeds+delegates, overrides Subscribe to emit each record
  Redeliver+1 times in order via one goroutine (select on out/ctx.Done,
  defer close → no leak).
- Tests `faulty_test.go` (5): RunConformance passes against
  Faulty{InMemory,Redeliver:1}; redelivery actually duplicates (one Append →
  same offset twice); Redeliver:0 exactly-once passthrough (no spurious 3rd);
  ctx-cancel unwinds the wrapper goroutine; Subscribe propagates underlying
  ErrClosed. `go test -race ./...` GREEN (13 pkgs), vet clean.
- Yellow: added the ctx-cancel leak test; confirmed test1-looseness acceptable
  (test2 proves redelivery), Redeliver:2 unnecessary.
- Blue: flagged the Subscribe err-passthrough branch untested → added the
  Subscribe-after-Close→ErrClosed test.
- Audit: CLEAN, no bugs. No goroutine leak within the documented Subscribe
  contract (caller cancels/Closes); race-clean; order preserved (dups in place);
  `for range Redeliver+1` valid on go1.26; negative Redeliver out-of-contract
  (no guard, matches repo style). Faulty satisfies via.Backplane via embed.

### Next step — P2c: two-Apps-one-backplane cross-pod CONVERGENCE test (the keystone)
Wire TWO via.App instances to ONE shared backplane (a single via.InMemory(), or
Faulty over it for realism). An Append/Inc on App-A's StateAppEvents must
converge on App-B's projection (App-B's projector tails the SHARED backplane's
Subscribe). This is THE test that proves the projector — not a local fold — is
the cross-pod mechanism (a local-fold impl would pass single-pod P1 tests but
FAIL here). Validate: how to inject ONE backplane into two Apps
(via.WithBackplane(shared) on both) and assert convergence (App-B fresh GET /
SSE shows App-A's appended value). Infra-free. Flag P2d (NATS, real server) to
maintainer after.

---

## Tick 26 — 2026-06-04 — P2c: two-Apps-one-backplane cross-pod convergence (THE KEYSTONE)

P2b committed (`7d14bb8`). This tick adds the infra-free cross-pod test that
proves the projector — not a local fold — is the cluster mechanism.

### Validated against code
- `applog.go:42` each App's projector Subscribes to `a.backplane` (its own
  field); `broadcast.go:64` broadcastRender iterates `a.snapshotContexts()` (its
  own ctx registry). ∴ two Apps wired `via.WithBackplane(shared)` each run an
  independent projector tailing the SHARED log and fold into their OWN per-App
  logState — they converge because they fold the same log. A local-fold-in-Append
  impl would update only the writer App's projection and FAIL this test.
- Two via.App in one process sharing ONE Go backplane object faithfully
  simulates two pods sharing one NATS (the design's stated infra-free cross-pod
  test). Real pods are separate processes each with their own backplane CLIENT to
  the same server; the shared in-mem object stands in.
- `feedPage` (StateAppEvents[addItem,[]string], Add appends "hello") is reusable
  from stateappevents_runtime_test.go (same package via_test).
- Lifecycle note: the test uses httptest server.Close() (NOT App.Shutdown), so
  the shared backplane is never closed mid-test (App.Shutdown would close the
  shared object, breaking the peer — a real concern only for the unusual
  two-Apps-in-one-process setup; real pods each Close their own client).

### Converged slice — P2c (test-only; existing behavior, cross-App)
- **File:** new `backplane_crosspod_test.go` (package via_test).
- **Acceptance:** shared := via.InMemory(); App-A and App-B both
  via.New(WithTestServer, WithBackplane(shared)) + Mount[feedPage]; client A on
  server A, client B (live SSE) on server B. A fires Add → B's SSE frame
  (AwaitFrame) shows the folded "hello" → cross-pod convergence. Plus a
  bidirectional / fresh-GET convergence assertion. Whole module green + race-clean.
- Written directly (no behavior change — exercises the P1.1b projector across two
  Apps), then Explore-confirmed it genuinely distinguishes projector from
  local-fold (would fail a writer-only-fold impl).

### Tick 26 — BUILT + result
- NEW `backplane_crosspod_test.go` (2 tests): two via.App sharing one
  via.InMemory() converge — A's Add surfaces folded in B's live SSE frame
  (TestTwoAppsShareOneBackplaneAndConverge); after appends on both pods, fresh
  readers on EITHER pod see the same ordered two-item feed
  (TestCrossPodProjectionsAgreeForFreshReaders). `go test -race ./...` GREEN
  (13 pkgs), vet clean.
- Keystone property holds BY CONSTRUCTION: Append does no broadcast/fold and each
  App's broadcastRender only iterates its OWN registry, so B observing A's append
  is possible ONLY via B's projector tailing the shared log — a local-fold impl
  cannot pass. (Explored not needed; the impossibility is structural.)
- This validates the design's central correctness claim (the projector is the
  cross-pod mechanism; #3/#7 closed by resumable Subscribe, not census).

### Phase 2 status — 2a✅ conformance · 2b✅ Faulty+at-least-once · 2c✅ cross-pod keystone.
### Remaining: 2d NATS reference backend — RELEASE-GATING, real-network.
2d (NATS JetStream+KV backend + run RunConformance against it) needs an external
NATS server (or an embedded nats-server test dep) + a new module dependency.
This is the FIRST step requiring a maintainer decision: (a) add github.com/
nats-io/nats.go (+ nats-server for tests) as deps? (b) where does the backend
live — a separate module github.com/go-via/vianats (keeps via dep-free) or an
internal package? (c) CI must run a real nats-server (RELEASE-GATING per spec
785). FLAG TO MAINTAINER before proceeding. Meanwhile Phase 3 (value-path:
StateApp/StateSess cluster survivability) is infra-free and could proceed first
if the maintainer prefers to defer NATS — but the phase map orders P2 before P3.

---

## Tick 27 — 2026-06-04 — Phase 3 CONVERGENCE + design gate (T-GO-9). 2d-NATS still maintainer-blocked.

P2c committed (`52fd0f9`). Phase 2 infra-free work DONE; 2d-NATS awaits the
maintainer (deps/module/CI). Phase 3 rewrites the CORE value-path
(StateApp/StateSess), so per the loop's "convergence gates the code" this tick is
convergence + design-gate; build starts next tick.

### Validated against code (the rewrite surface + green guard)
- `stateapp.go:59-76` StateApp.Update = `ctx.app.appStore.Update(key, fn)` (kvStore
  pessimistic per-key-mutex RMW) → markStateDirty → broadcastRender(ctx,nil,key).
  Read (`:27-43`) hits appStore (any, zero-serialization).
- `statesess.go:67-86` StateSess.Update = `sess.data.Update(key, fn)` (per-session
  kvStore) → broadcastRender(ctx, sess, key). Read hits sess.data.
- `sess.go:24-26` session.data is a kvStore (any). `kvstore.go` = sync.Map +
  per-key-mutex Update.
- Green guard for the rewrite (single-pod byte-for-byte): statesess_test.go (9),
  app/shape/op/render/sess tests — a substantial existing StateApp/StateSess
  suite that MUST stay green.

### DESIGN FEEDBACK GATE — T-GO-9 (value serialization consequence)
Store.CAS/LoadSnapshot move []byte, so making val:<key> the SoT forces
StateApp[T]/StateSess[T] values to SERIALIZE (default JSON) on Update→CAS (the L1
kvStore keeps live T for zero-serialization READS). Since nil→InMemory(), this
holds single-pod too. So "v0.4.0 byte-for-byte" = identical OBSERVABLE behavior
for serializable T (the universal case for shared state); a non-serializable T
(func/chan) in a pod-local kvStore is the one narrow break. Accepted consequence
of the converged Store-as-SoT design (not a new decision). Spec patched at the
Store godoc (val:<key> bullet). RE-VALIDATED: no other spec claim contradicts.

### Converged P3 sub-slice plan
- **P3a (next tick) — StateApp value CROSS-POD convergence (the valuable core):**
  Update: load current (val, rev) → fn(T) → CAS(`val:`+wireKey, expectedRev,
  json(newT)) with retry-on-ErrCASConflict (replaces the kvStore mutex RMW); on
  success update L1 (appStore + an l1Rev[key] map) + Append a value-less
  Change{key,rev} to a shared changes feed (EventLog key e.g. `via:changes`,
  runtime-internal codec). One App-level changes-tailer (Subscribe) re-pulls
  val:<change.key> to Store HEAD per Change, gated storeRev≥change.rev, L1-monotone
  (apply only if storeRev>l1Rev[key]), then broadcastRender(nil,nil,key). Read
  unchanged (L1, zero-serialization). Tests: existing StateApp suite GREEN
  (single-pod byte-for-byte incl. the 200-increment concurrent test now exercising
  CAS-retry) + a two-Apps StateApp.Update cross-pod convergence test (mirrors the
  P2c keystone). Files: stateapp.go, app.go (l1Rev map + changes-tailer +
  startChangesTailer at New/first-use), a new applogval.go or fold into applog.go,
  runtime-internal Change codec.
- **P3b — periodic Store-head reconcile sweep** over each pod's subscribed value
  keys → feed becomes a pure latency optimization; closes crash-between-CAS-and-
  Append (T4-SRE-1) AND cold-start (a pod that joined after writes, saw no Change).
- **P3c — StateSess** value cross-pod convergence: same notify-and-pull, session-
  scoped; Changes carry the FULL 256-bit sid; receiver EXACT-matches sid and DROPS
  fail-closed on unknown sid (never broadcast-to-all). Net: no sticky sessions.

### Next step — BUILD P3a (StateApp cross-pod value convergence) under TDD.
Re-flag each tick: 2d-NATS maintainer-blocked.

---

## Tick 28 — 2026-06-04 — P2d-1: NATS backend UNBLOCKED (maintainer: "do the nats") — module scaffold + embedded-JetStream harness

Maintainer green-lit Phase 2d and chose the NESTED module ./vianats (keeps core
`via` dependency-free). Test server: EMBEDDED nats-server (no external container/
CI infra) — my call, recorded.

### Built + verified
- NEW nested module `./vianats` (own go.mod, `replace github.com/go-via/via =>
  ../`); deps fetched OK (network works): nats.go v1.52.0, nats-server v2.14.2.
  Core `via` go.mod untouched → consumers of via never pull nats.
- `vianats/embedded_test.go`: `startEmbeddedJetStream(t)` boots an in-process
  JetStream server (random port, t.TempDir store, t.Cleanup shutdown) →
  ClientURL. Smoke test `TestEmbeddedJetStreamPrimitivesWork` PROVES the
  primitives the backend rests on work in this sandbox: KV bucket create +
  CAS-by-revision (stale rev → conflict) + Get; stream publish returns a
  monotone Sequence; a subject-filtered DeliverAll consumer replays in order.
  `go test` GREEN; `go vet` clean; core `via build ./...` unaffected.

### Backend design mapping (validated against nats.go jetstream API in the smoke test)
- Store ← JetStream KV: LoadSnapshot=kv.Get→(Value,Revision); CAS(expectedRev=0)
  =kv.Create (must-not-exist), else kv.Update(key,data,expectedRev); revision
  mismatch → ErrCASConflict. Rev = KV revision (uint64).
- EventLog ← one JetStream stream, subject per key (`<prefix>.<wireKey>`):
  Append=js.Publish(subject)→PubAck.Sequence (the opaque Offset; per-STREAM seq,
  non-dense per key — fine, conformance treats offsets opaque). Subscribe(from)=
  subject-filtered consumer with DeliverByStartSequence=from+1 → Offset>from in
  order, then live-tails. Head(key)=GetLastMsg(stream,subject).Sequence (0 if
  none). Close=nc.Close (drain).
- Epoch stays 0 for v1 (offset-space reset detection is P4).

### Next step — P2d-2: implement vianats.Backplane Store (KV) + EventLog
(stream/consumer) + Close, then run backplanetest.RunConformance against an
embedded server (RELEASE-GATING). Sub-slice if needed: Store first (targeted KV
tests), then EventLog (Append/Head/Subscribe-with-resume + live-tail), then the
full conformance gate. After P2d ships green, RESUME P3a (StateApp cross-pod
value convergence) which was converged in tick 27.

---

## Tick 29 — 2026-06-04 — P2d-2: implement vianats.Backplane + RELEASE-GATING conformance

P2d-1 scaffold committed (`0b5e481`). This tick implements the backend and gates
it with backplanetest.RunConformance on an embedded server.

### Validated against the fetched nats.go v1.52.0 jetstream API (module cache)
- KV: `Get(ctx,key)→(KeyValueEntry{Value()[]byte, Revision()uint64}, ErrKeyNotFound)`;
  `Create(ctx,key,val)→(rev, ErrKeyExists)`; `Update(ctx,key,val,rev)→(rev,err)`
  where a WRONG-revision error ALSO satisfies `errors.Is(err, jetstream.ErrKeyExists)`
  (KV CAS uses expected-last-subject-seq → code StreamWrongLastSequence, the same
  sentinel). ∴ map BOTH conflict paths via errors.Is(ErrKeyExists)→via.ErrCASConflict.
- Stream: `GetLastMsgForSubject(ctx,subject)→(RawStreamMsg{Sequence}, ErrMsgNotFound)`
  → Head (Offset 0 on ErrMsgNotFound). `js.Publish(ctx,subj,data)→PubAck{Sequence}`
  = the opaque Offset. OrderedConsumer(FilterSubjects, DeliverPolicy/OptStartSeq) +
  Messages() iterator for resume+live-tail; msg.Metadata().Sequence.Stream = offset.

### Converged slice — P2d-2 (one coherent backend + the gate)
- **Files (nested module ./vianats):** `vianats.go` (the Backplane impl + JetStream
  constructor) + `conformance_test.go` (RunConformance gate). Reuse the embedded
  harness from embedded_test.go.
- **Constructor:** `JetStream(nc *nats.Conn, opts ...Option) (*Backplane, error)` —
  ensures a KV bucket + one stream (subjects `<prefix>.>`, default prefix "via.ev")
  at construction (context.Background+timeout). Option for name prefix (test
  isolation). Returns via.Backplane.
- **Store:** LoadSnapshot=kv.Get (ErrKeyNotFound→ok=false); CAS(expectedRev 0 →
  kv.Create, else kv.Update(key,data,expectedRev)); errors.Is(ErrKeyExists)→
  via.ErrCASConflict; Rev=uint64 revision.
- **EventLog:** key→subject `<prefix>.<sanitized wireKey>`. Append=js.Publish→
  Offset(ack.Sequence). Head=GetLastMsgForSubject. Subscribe(from): OrderedConsumer
  with DeliverAll (from==0) or DeliverByStartSequence OptStartSeq=from+1; a goroutine
  drains Messages() → out chan (Record{Key,Offset,Data}), exits on ctx-cancel or
  conn-close (Messages errors) → close(out). Epoch 0.
- **Close:** nc is owned by the caller; Backplane.Close drains its consumers/iters
  (stop them) — does NOT close the caller's nc unless constructed to. (Decide: the
  backplane should stop its own subscriptions on Close so projector goroutines
  unwind; the conformance Close→ErrClosed subtest requires Append after Close to
  fail — so Close must mark closed and reject Append. Add a `closed` flag.)
- **Gate:** RunConformance(t, factory) where factory creates a fresh backplane with
  a UNIQUE name prefix per call (nuid) on a per-test embedded server → isolation.
  RELEASE-GATING: this is the real-network conformance the spec mandates.

### Tick 29 — BUILT + result → ✅ PHASE 2 COMPLETE (incl. RELEASE-GATING NATS)
- NEW `vianats/vianats.go`: `JetStream(nc, opts...) (*Backplane, error)` +
  `WithPrefix`. Store=JetStream KV (LoadSnapshot/CAS, both conflict modes →
  via.ErrCASConflict via errors.Is(ErrKeyExists)); EventLog=one stream,
  subject-per-key (`<prefix>.ev.<sanitized>`; Append→PubAck.Sequence,
  Head→GetLastMsgForSubject, Subscribe→OrderedConsumer DeliverAll/
  DeliverByStartSequence(from+1) + Messages() drained by a goroutine that unwinds
  on ctx-cancel or Close); Close marks closed + closes done (doesn't close the
  caller's nc); AllowDirect stream for fast Head; sanitize() maps arbitrary wire
  keys to safe, collision-free subject/KV tokens.
- Tests: `conformance_test.go` runs backplanetest.RunConformance against a fresh
  uniquely-prefixed backplane per subtest on ONE embedded JetStream server —
  ALL 9 subtests PASS, `-race` clean (this is the RELEASE-GATING real-backend
  conformance). + `sanitize_internal_test.go` locks the non-alnum/empty/
  collision branches (pure logic).
- Yellow: test sound; durability-across-reconnect flagged as post-v1 (correct).
- Blue: only sanitize's non-alnum/empty branches untested → added the internal
  unit test. Close-idempotent + watcher it.Stop() are reachable-defensive (kept).
- Audit: CLEAN, no bugs. Verified Subscribe closes `out` exactly once (only the
  drain loop closes it; watcher only receives b.done — no race), it.Stop()
  idempotent (nats.go CAS-guarded), Close idempotent + doesn't touch nc, CAS
  mapping has no context/transient misclassification, offsets are one
  stream-seq space so OptStartSeq=from+1 resumes strictly-after even with
  interleaved keys (probed), ordered consumers server-GC'd via it.Stop().

### ✅ PHASE 2 COMPLETE — 2a conformance · 2b Faulty+at-least-once · 2c cross-pod keystone · 2d NATS reference backend (RELEASE-GATING green).
The Backplane abstraction is proven: in-memory base, fault-injected, cross-pod
convergent, AND a real ordered/durable/resumable NATS JetStream backend passing
the identical contract suite. #3/#7 (stranding/reconnect) closed.

### Next step — RESUME P3a (StateApp cross-pod value convergence), converged in tick 27.

---

## Tick 30 — 2026-06-04 — P2 DONE (NATS green); BUILD P3a (StateApp cross-pod value convergence)

P2d-2 committed (`5eaa3f7`) — NATS RELEASE-GATING conformance green. Phase 2 fully
complete. Now building P3a per the tick-27 convergence.

### Re-validated against code
- `appStore` used ONLY in stateapp.go:37 (Read) + :66 (Update) + the app.go:52
  decl → safe to replace with a value-runtime. All existing StateApp test types
  are serializable (StateApp[int], StateAppNum[int], StateAppSlice[int]) → the
  T-GO-9 JSON-serialization gate breaks NO existing test.
- `runtime.go:210-213` bindScopeKeys already splits value/log (calls bindApp only
  for scopeLog) → add: call bindApp for any scope handle implementing appBinder
  (StateApp will implement it).
- KEY byte-for-byte insight: Update sets the SHARED valCell.l1 SYNCHRONOUSLY on
  CAS success, so every session/tab on ONE App sees it immediately — identical to
  appStore today. The changes-tailer is a no-op single-pod (monotone gate) and
  only populates L1 on a PEER App (cross-pod). ∴ existing single-pod StateApp
  suite stays green; the new two-Apps test is red today (pod-local appStore).

### P3a build plan (handed to tdd-rygba)
- App: `valStates map[string]*valCell` + mutex; valCell{mu RWMutex; l1 any; l1Rev
  via.Rev; decode func([]byte)(any,error)}; changes-tailer started once
  (sync.Once) at first value-bindApp, Subscribe(changesKey="via.changes", from 0).
  Internal Change{Key string; Rev via.Rev} json codec.
- StateApp.bindApp(a) [NEW; StateApp becomes an appBinder]: register valCell with
  a json→T decode closure; ensure changes-tailer running. bindScopeKeys calls
  bindApp for value handles too.
- StateApp.Read: trackRead + valCell.l1 cast to T (zero if absent). (drops appStore)
- StateApp.Update: CAS-retry loop on Store `val:`+wireKey — LoadSnapshot→decode
  cur T→fn→json(next)→CAS(expectedRev); retry on via.ErrCASConflict; on success
  set valCell.l1=next + l1Rev=newRev (under cell mu), Append Change{key,newRev} to
  changesKey, markStateDirty, broadcastRender(ctx,nil,key). Panic on nil ctx
  (unchanged).
- changes-tailer: per Change, vc:=valStates[Key]; if Rev>vc.l1Rev: LoadSnapshot(
  val:Key)→(data,storeRev,ok); if ok && storeRev>=Rev && storeRev>vc.l1Rev: vc.l1=
  decode(data), vc.l1Rev=storeRev; broadcastRender(nil,nil,Key) (T1-SRE-5 stale-
  drop + T3-SRE-1 monotone gate).
- Tests: existing StateApp suite GREEN throughout (byte-for-byte single-pod) + a
  two-Apps shared-backplane StateApp.Update cross-pod convergence test (mirror P2c
  keystone). P3b = periodic reconcile sweep (cold-start/crash-strand); P3c =
  StateSess + full-sid Change matching.

### Tick 30 — BUILT + result → P3a SHIPPED (StateApp cross-pod value convergence)
- NEW `appval.go`: valCell (L1 + decode + l1Rev under RWMutex), changesKey/change,
  registerValCell (+ valTailerOnce), startChangesTailer, applyChange (stale-drop
  + monotone re-pull), valProjection, valKey.
- `stateapp.go`: StateApp gains `app` + bindApp (registers typed decode + starts
  tailer); Read hits valProjection (L1, O(1)); Update is a CAS-retry loop on the
  Store cell `val:`+key (LoadSnapshot→decode→fn→Marshal→CAS, retry on
  ErrCASConflict up to 100 → errCASExhausted), sets the SHARED L1 synchronously
  (single-pod byte-for-byte), appends a value-less change hint, broadcastRenders.
- `app.go`: appStore → valStates map + mutex + once. `runtime.go`: bindScopeKeys
  calls bindApp on ANY appBinder (value + log). Removed the now-dead scopeKind
  (descriptor.go/walker.go) superseded by the appBinder type-assert.
- Tests: existing StateApp suite GREEN throughout (byte-for-byte single-pod) +
  NEW `backplane_crosspod_value_test.go` (two Apps/one backplane → StateApp.Update
  on A converges on B's live SSE + fresh reader) + NEW internal
  `appval_internal_test.go` (applyChange stale-drop T1-SRE-5, monotone T3-SRE-1,
  undecodable-snapshot drop). `go test -race ./...` GREEN (13 pkgs), vet clean.

### DESIGN FEEDBACK — T3-SRE-2 (silent writes suppress the Change hint)
The first green run regressed TestSyncOff_skipsStateAppBroadcastAcrossSessions:
a silent (sync-off) Update appended a Change → the changes-tailer's
broadcastRender(skip=nil) bypassed the silent-writer suppression. Resolution: a
silent Update writes the Store + L1 but does NOT append the Change hint — no
fan-out (local or cross-pod) for that write; the value persists and propagates on
the next loud write or the reconcile sweep (P3b). Consistent with SyncOff's
"write, don't notify" semantic. Recorded; spec value-path note already says the
feed is a liveness hint only, so no contradiction — added the silent gate.

- Yellow: cross-pod test is a watertight proof (backplane is the only shared
  resource); bidirectional deferrable.
- Blue: impl sound; flagged applyChange SRE gates as load-bearing-but-implicit →
  added the internal test. errCASExhausted/marshal-error = acceptable-defensive
  backstops (retry happy+conflict paths covered by concurrent-200); note for P3b.
- Audit: CLEAN, no bugs. CAS-retry converges (-race -count=10), all l1 access
  guarded, broadcastRender outside vc.mu (no deadlock w/ SyncNow→Read→RLock),
  self-redelivery harmless (monotone no-op), tailer no leak, Marshal-before-CAS
  so a bad T leaves the Store untouched.

### Next step — P3b: periodic Store-head reconcile sweep
Closes cold-start (a pod that joined after writes / saw no Change) AND the
crash-between-CAS-and-Append strand (T4-SRE-1) AND propagation of silent writes —
making the changes feed a pure latency optimization. Then P3c: StateSess value
cross-pod convergence with full-256-bit-sid Change matching (drop fail-closed on
unknown sid).

---

## Tick 31 — 2026-06-04 — P3b: periodic Store-head reconcile sweep SHIPPED

P3a committed (`397817b`). This tick makes the changes feed a pure latency
optimization.

### Built + result
- `config.go`: `reconcileInterval` + `WithReconcileInterval(d)` (0 disables;
  default 5s).
- `app.go`: default 5s in New; sweep guard widened to include
  `reconcileInterval>0` (so stopSweep is created); starts
  `go runSweep(interval, interval, a.reconcileValues)` under the existing
  stopSweep lifecycle (Shutdown closes it).
- `appval.go`: `reconcileValues` (snapshot keys under valStatesMu, release, then
  per-key) + `reconcileKey` (LoadSnapshot val:key → monotone gate storeRev>l1Rev
  → decode → update L1 → broadcast ONLY when L1 advanced).
- Tests: NEW `backplane_reconcile_test.go` —
  TestReconcileSweepConvergesPeerWithoutAChangeHint (A SILENT write emits no
  hint → B's tailer never fires → B converges ONLY via the 50ms sweep) +
  TestChangesFeedAloneConvergesWithReconcileDisabled (WithReconcileInterval(0) →
  a loud write still converges B via the tailer, documented sweep-off mode).
  Extended `appval_internal_test.go` with TestReconcileKeyAdvancesOnlyForwardAnd
  SurvivesPoison (advance / no-op-no-regression / poison-survival / absent-cell).
  `go test -race ./...` GREEN (13 pkgs), vet clean.
- Yellow: confirmed the silent-write path genuinely bypasses the tailer (only the
  sweep can pass the test); flagged the disabled-mode + no-op-broadcast gaps.
- Blue: flagged WithReconcileInterval(0) untested → added the changes-feed-only
  test; broadcast-only-on-change covered via the internal changed-flag/l1 proxy
  (acceptable-defensive). No dead code.
- Audit: CLEAN, no bugs. Goroutine lifecycle safe (stopSweep created when
  reconcileInterval>0, closed once on Shutdown); lock order consistent
  valStatesMu→vc.mu everywhere, broadcasts outside vc.mu; steady-state tick
  no-ops (monotone gate) → no render storm; empty valStates → zero LoadSnapshot;
  sweep+tailer idempotent; SyncOff "no frame" tests unaffected (5s default >
  300ms window AND writer already set l1Rev → sweep no-op).

### Phase 3 status — P3a✅ (StateApp cross-pod) · P3b✅ (reconcile sweep). Remaining: P3c.
### Next step — P3c: StateSess value cross-pod convergence + full-256-bit-sid Change matching
StateSess.Update → CAS Store cell (session-scoped key incorporating the FULL sid)
+ Append a session Change carrying the full 256-bit sid; the tailer/sweep
reconcile per-session, and a receiving pod EXACT-matches the sid and DROPs
fail-closed on an unknown sid (never broadcast-to-all). statesess.go currently
uses sess.data (per-session kvStore) + broadcastRender(ctx,sess,key). Validate
the session/sid plumbing (sess.go) before building. After P3c, Phase 3 done →
P4 versioning.

---

## Tick 32 — 2026-06-04 — P3c CONVERGENCE (StateSess cross-pod) — security-sensitive; build next tick

P3b committed (`daa43a9`). P3c is the session-scoped value path: the most
security-sensitive slice (sid handling + fail-closed drop), larger than P3a.
Convergence tick (validate + design + record); build next.

### Validated against code
- `sess.go:24-27` `session{id string; data kvStore; lastAccess}` — `id` is the
  FULL session id. `app.go` `sessions map[string]*session` + `sessionsMu`
  (RWMutex). `sess.go:159` `a.sessions[sess.id]=sess`.
- `statesess.go:36,68` Read/Update use `ctx.session.Load()` (*session) then
  `sess.data` (per-session kvStore, live T, zero-serialization) +
  `broadcastRender(ctx, sess, key)`.
- `broadcast.go:60,68` broadcastRender(skip, sess, key): when sess != nil, only
  ctxs with `c.session.Load() == sess` (POINTER equality) re-render. ∴ within a
  pod, scoping is by the *session object; CROSS-POD the SAME sid is a DIFFERENT
  *session object on pod B, so B's tailer must resolve sid → B's a.sessions[sid]
  and broadcast to THAT object (never nil sess → that would fan out app-wide).
- StateSess does NOT implement appBinder today; bindScopeKeys (runtime.go) calls
  bindApp on any appBinder → adding StateSess.bindApp wires it in.

### Converged P3c plan (build next tick, TDD)
- **change struct** gains `Sid string json:"s,omitempty"`; ONE shared changes
  feed. Tailer routes: Sid=="" → applyChange (StateApp, existing); else →
  applySessionChange.
- **session struct**: add `revs map[string]Rev` + a mutex (per-wireKey monotone
  rev for that session). Init on session create (sess.go getOrCreateSession +
  Rotate's fresh session).
- **App**: `sessDecoders map[string]func([]byte)(any,error)` + mutex (wireKey →
  json→T decoder), registered by StateSess.bindApp; reuse valTailerOnce to start
  the shared changes-tailer.
- **sessValKey(sid, wireKey) = "val:s:" + sid + ":" + wireKey** — FULL sid, no
  truncation.
- **StateSess.Update**: CAS-retry on sessValKey(sid,key) (LoadSnapshot→decode
  cur→fn→Marshal→CAS); on success set sess.data[key]=next (sync RYW) +
  session.revs[key]=newRev (monotone); Append change{Sid:sid,Key:key,Rev:newRev}
  UNLESS ctx.silent (T3-SRE-2 parity); broadcastRender(ctx, sess, key). Panic on
  nil ctx (unchanged).
- **StateSess.Read**: unchanged (sess.data, zero-serialization) — Update keeps it
  populated; the tailer/sweep populate it on peers.
- **applySessionChange(c)** [SECURITY-CRITICAL]: sess := a.sessions[c.Sid] under
  sessionsMu.RLock; if sess == nil → DROP, return, NO broadcast (fail-closed:
  the session isn't on this pod; NEVER broadcast-to-all on an unknown sid). Else
  re-pull sessValKey → monotone gate (storeRev > session.revs[key]) → decode via
  sessDecoders[key] → set sess.data[key] + revs[key]; broadcastRender(nil,
  sessObj, key) (scoped to that session only).
- **Reconcile sweep**: extend reconcileValues to also iterate a.sessions (snapshot
  under sessionsMu) × registered sessDecoder keys → reconcileSessionKey(sessObj,
  key). NOTE (perf): O(sessions × sessKeys) per tick — acceptable v1; a future
  optimization could track only dirty (sid,key) pairs. Record.
- **Security invariants (load-bearing, must be tested):** (1) a session Change
  for sid X reaching a pod WITHOUT session X is DROPPED (no broadcast, no
  cross-session leak); (2) convergence reaches ONLY the same sid's tabs, never a
  different session's; (3) the full sid is in the Store key (no truncation /
  cross-session aliasing).
- **Tests:** keep statesess_test.go (9) green (single-pod byte-for-byte) + a
  two-Apps/one-backplane test: StateSess.Update on session S via pod A converges
  on session S's tab on pod B, but a DIFFERENT session S2 on pod B does NOT see
  it (the security scoping). Mirror P3a/P3b shape; vt clients carry session
  cookies (separate clients = separate sessions; Fork = same session).

### Next step — BUILD P3c under TDD (security-sensitive: assert the fail-closed
drop + cross-session isolation explicitly). Then Phase 3 DONE → P4 versioning.

---

## Tick 33 — 2026-06-04 — P3c BLOCKED on a security decision (T-SEC-3: cross-pod session adoption)

Building P3c (StateSess cross-pod), validation hit a gap between the converged
spec and the code.

### Gap (evidence)
- `sess.go:145-160` getOrCreateSession: a presented via_session cookie whose sid
  is NOT in this pod's `a.sessions` → MINTS A NEW session (fresh genSecureID()),
  does NOT adopt the presented sid. So the SAME logical session does not exist on
  a second pod a client visits.
- Spec line 786 promises "Net: state correctness no longer needs sticky
  sessions." That REQUIRES a session to be adoptable across pods (any pod serves
  a presented sid). The spec explicitly specifies only the CHANGES path (drop
  fail-closed on unknown sid in applySessionChange); it is SILENT on the
  cookie-adoption path — yet adoption is the precondition for the no-sticky
  promise.
- ∴ P3c's headline (cross-pod-same-session convergence) is UNBUILDABLE end-to-end
  until getOrCreateSession adopts presented sids.

### Why this is a maintainer decision (not an autonomous design-gate fix)
Adopting client-presented sids changes a CORE AUTH primitive:
- It is the standard 256-bit-bearer distributed-session model (signed/opaque
  session cookies work this way — any server trusts the bearer sid).
- BUT it opens a SESSION-FIXATION surface that the current mint-new behavior
  closes: an attacker who plants a known sid cookie on a victim could share the
  session post-auth. Mitigation is Rotate-on-privilege-change (Session.Rotate
  exists, sess.go:101) — an APP responsibility/discipline.
- It interacts with the via_tab-as-CSRF-token model (memory: via_tab IS the CSRF
  token) — which session a via_tab binds to.
- Minor junk-session-creation vector (attacker spams random sids → records reaped
  by the TTL sweep).
This is security-sensitive + hard-to-reverse + outward-facing → FLAG, don't
assume, even though the converged spec implies it.

### Options put to the maintainer
- A) Adopt a presented well-formed sid in getOrCreateSession (enables cross-pod
  sessions = the design's promise; relies on Rotate-on-login; standard model).
- B) Keep mint-new (sessions stay pod-affine); descope StateSess-cross-pod for v1
  — build only the value-path mechanics + drop-on-unknown-sid (no end-to-end
  cross-pod session test); document that StateSess clustering needs sticky LB.
- C) Adopt ONLY if the cluster shows the session exists (a session registry / a
  backplane existence check) — closes the junk-session + blind-fixation vector,
  but more complex.

### Status: P3c BUILD PAUSED pending the maintainer's choice. Phases 0-3b shipped
& pushed (commit c12089b). Everything else green. Could proceed to P4 (versioning,
StateAppEvents-path, independent of the value/session path) while this is decided.

---

## Tick 34 — 2026-06-04 — T-SEC-3 RESOLVED: session sid adoption (maintainer-approved); unblocks P3c

Maintainer chose "Adopt presented sid". Built the cross-pod session-adoption
unblocker (security-sensitive auth change).

### Built + result
- `util.go`: `validSessionID(s)` — strict: len==64 && all [0-9a-f] (exactly
  genSecureID's format). An attacker cannot adopt a non-conforming/garbage token.
- `sess.go`: `adoptSession(sid)` (Lock + LoadOrStore re-check → one *session per
  sid, race-safe) + getOrCreateSession adoption branch: cookie present, sid NOT
  in a.sessions, validSessionID → adopt the SAME id (no sticky sessions); known
  sid still early-returns (above the branch, unchanged); malformed → mint-new
  (unchanged). r.AddCookie so the same request resolves it.
- Tests `sess_adopt_internal_test.go` (package via): validSessionID format
  (short/long/non-hex/uppercase/empty rejected); unknown-well-formed adopted +
  registered; known-sid returns SAME object w/ data intact (no clobber);
  adoptSession idempotent (deterministic re-check guard); malformed not adopted;
  concurrent 16-racer adoption → ONE shared *session pointer. `go test -race
  ./...` GREEN (13 pkgs).
- Yellow: added the known-sid-unchanged + concurrent-pointer-equality assertions.
- Blue: re-check ok path was only probabilistic → extracted adoptSession +
  deterministic idempotency test.
- Audit (SECURITY): CLEAN. validSessionID strict (uppercase/non-hex/null/wrong-len
  rejected, no strconv/hex.Decode leniency); same key throughout → never clobbers
  a different session; known-sid early-return wins (data survives); race-clean
  LoadOrStore; via_tab/CSRF + Session.Rotate + sessionFromRequest unaffected
  (duplicate via_session cookie resolves to the same sid, harmless); adopted
  session's empty data is correct until the tailer/sweep populate it (P3c).

### Next step — BUILD P3c (StateSess cross-pod value path), now UNBLOCKED. Per
tick-32 converged plan: change.Sid + tailer routing + applySessionChange
(fail-closed drop on unknown sid) + session.revs + sessDecoders + StateSess
bindApp/Update CAS on "val:s:"+sid+":"+key + session reconcile. Tests: cross-pod
same-session converge + cross-session ISOLATION + drop-on-unknown-sid; keep
statesess_test.go green. Then Phase 3 DONE → P4.

### Tick 34b — P3c test strategy refinement (harness wrinkle)
vt cookie jars are host-scoped + private, so a two-servers/same-via_session e2e
is awkward. Test strategy for P3c: (1) DETERMINISTIC internal test (package via,
loadStub-style) for applySessionChange — the cross-pod mechanism + SECURITY:
known-sid converges that session's data; UNKNOWN-sid DROPS fail-closed (no panic,
no effect, no broadcast-to-all); monotone gate; decode-survival. (2) vt single-app
CROSS-SESSION ISOLATION: two clients (= two sessions) on one app; StateSess.Update
on session A's tab re-renders A's tabs but NOT session B's (broadcastRender scopes
by *session). (3) keep statesess_test.go (9) green single-pod. This proves the
load-bearing security + convergence behavior deterministically without
cross-server-cookie gymnastics.

---

## Tick 35 — 2026-06-04 — P3c SHIPPED (StateSess cross-pod) → ✅ PHASE 3 COMPLETE

P3c built on the now-in-place session adoption (9400d48). StateSess[T] is now
cross-pod convergent, session-scoped, fail-closed.

### Built + result
- `appval.go`: change.Sid + tailer routing (Sid==""→applyChange else
  applySessionChange); sessValKey(sid,key)="val:s:"+sid+":"+key (FULL sid);
  applySessionChange (SECURITY: unknown sid → DROP fail-closed BEFORE any
  LoadSnapshot/broadcast; else stale-replica drop storeRev>=c.Rev + decode +
  atomic advanceRev monotone gate + broadcastRender(nil, sess, key) scoped to
  that one session); reconcileSessions/reconcileSessionKey (sweep, O(sess×keys),
  noted).
- `sess.go`: session.revs + revsMu + loadRev/advanceRev (atomic monotone gate).
- `statesess.go`: StateSess.app + bindApp (register sessDecoder + start tailer →
  appBinder); Update = CAS-retry on sessValKey + sync L1 + advanceRev + silent-
  gated hint{Sid} + broadcastRender; Read unchanged.
- `app.go`: sessDecoders map + init.
- Tests: existing statesess_test.go (9, incl cross-session no-fanout) GREEN
  single-pod byte-for-byte; NEW internal sess_change_internal_test.go —
  applySessionChange fail-closed-NO-LOAD on unknown sid + full-sid-key assertion
  + stale-replica drop + monotone + poison-survival; reconcileSessionKey
  advance/no-op/poison. `go test -race ./...` GREEN (13 pkgs), vet clean.
- Blue: flagged reconcileSessionKey sweep path untested → added it. Cross-pod
  StateSess e2e + silent-StateSess deferred (vt cross-server-cookie wrinkle /
  parity with StateApp's tested silent path).
- Audit (SECURITY): CLEAN. Cross-session isolation verified (full-sid key, per-
  session broadcast pointer-scoping, decoder shared-by-wireKey but value/broadcast
  per-session); fail-closed (unknown sid returns before load/broadcast; sid never
  "" — always 64-hex genSecureID/adopted); CAS-retry byte-identical to StateApp;
  race-clean (advanceRev atomic gate; no lock nesting across sessionsMu/
  sessDecodersMu/revsMu); lazy-init safe; single-pod byte-for-byte + SyncOff
  preserved.

### ✅ PHASE 3 COMPLETE — P3a StateApp cross-pod · P3b reconcile sweep · P3c
StateSess cross-pod (+ T-SEC-3 session adoption). The value path is now
cluster-survivable & cross-pod convergent for BOTH StateApp and StateSess, with
the Store as source of truth, no sticky sessions, and fail-closed session
scoping. Single-pod behavior byte-for-byte.

### Next step — PHASE 4 (versioning hardening): the StateAppEvents Codec envelope
(TypeTag+Version+Payload), upcaster chain, drop-on-undecodable (ErrUndecodable →
fold no-op + via.events.undecodable), ErrForwardIncompatible (envelope version >
binary → projector halts), Epoch/offset-space-reset detection. Independent of
the value path; StateAppEvents-side. Sub-slice (envelope+default-codec first,
then upcasters, then forward-incompat/epoch).

---

## Tick 36 — 2026-06-04 — Phase 4 CONVERGENCE (versioning hardening) + envelope/TypeTag design gate

Phase 3 done (3d8fb94, pushed). Phase 4 = StateAppEvents E-versioning. It is a
USER-FACING-API phase + the envelope is "unrecoverable if omitted" (spec 786),
so converge carefully; build next tick.

### Validated against code
- `stateappevents.go:116` Append = `json.Marshal(ev)` (NO envelope); `:53-63`
  bindApp foldBytes = `json.Unmarshal(data,&ev)` then e.Fold. `:109` Append path.
- `applog.go` projector ALREADY skips the fold on a foldBytes error and STILL
  advances the cursor → drop-on-undecodable BEHAVIOR mostly exists; P4 formalizes
  it (envelope + metric + the forward-incompat HALT which must NOT advance).
- `metrics.go:23` Metrics{Counter/Gauge/Histogram}; `a.metricsOrNoop()`. So
  via.events.undecodable = a.metricsOrNoop().Counter("via.events.undecodable", ...).
- Current StateAppEvents event types in-repo: chat `Posted`, `counterTick`,
  test `addItem`/`tickEvent` — none declare a TypeTag today.

### DESIGN GATE — T-DX-3 (TypeTag is over-specified for v1's one-E-per-key model)
Spec mandates envelope {t:TypeTag, v, d} with a STABLE USER-DECLARED TypeTag
(EventType() method / event= tag) enforced at Mount (no tag → fail fast). But:
- The load-bearing axis for v1 is the VERSION (enables upcasters + forward-incompat
  detection). TypeTag's stated rationale is (a) rename-freedom and (b) multi-variant
  discrimination. For the CURRENT one-E-per-key model decoded with plain JSON, the
  Go TYPE NAME is NOT on the wire (only field names), so type-rename is ALREADY
  free; and there's a single E per key, so NO discrimination is needed. ∴ a
  user-declared TypeTag is NOT load-bearing for v1 — and requiring it would add a
  breaking user-facing API (every event type needs EventType()) + break all 4
  current in-repo usages.
- RESOLUTION (record, will patch spec at build time): the v1 default codec emits
  envelope {t, v, d} with TypeTag AUTO-DERIVED (reflect type name of E) — NO new
  user API, Mount-enforcement auto-satisfied, the tag is present for diagnostics +
  future discrimination. The VERSION is the load-bearing field. A user-overridable
  stable TypeTag (event= tag) + tagged-union multi-variant is a post-v1 refinement
  (only matters when a key carries multiple event variants). This keeps the
  converged envelope SHAPE ({t,v,d}) while not imposing a breaking API. Honors
  "DO NOT CUT the versioned envelope" (we keep it) — only the user-declared-tag
  requirement is softened to auto-derived for v1.

### Converged P4 sub-slices
- **P4a — versioned envelope + drop-on-undecodable metric + forward-incompat halt:**
  default codec wraps Append payloads in {t:typeName(E), v:1, d:json(ev)}; decode
  reads the envelope: v==currentMax(1) → unmarshal d into E; v>currentMax →
  ErrForwardIncompatible → projector HALTS that key (stop folding, do NOT advance
  cursor — roll-forward-only) + metric; bad envelope/payload / no upcaster →
  ErrUndecodable → projector NO-OPS the record (advance cursor, skip fold) +
  via.events.undecodable Counter. Migrate the 4 in-repo event types (transparent —
  auto-derived tag, no code change to them; only the codec wraps). Tests: a normal
  event still folds; a poison record is dropped (projection intact, cursor advances,
  metric fires); a forward-version record halts the key (projection frozen, cursor
  NOT advanced) + metric. Touch: stateappevents.go (encode/decode envelope), applog.go
  (projector: distinguish ErrForwardIncompatible-halt from ErrUndecodable-skip;
  emit metric), a small envelope codec.
- **P4b — upcaster registry:** RegisterEvent[E](fromV, toV, fn) decode-chain run
  stored-version→current BEFORE unmarshal into current-shape E; additive-first
  godoc. (Fold only ever sees current E.)
- **P4c — Epoch/offset-space-reset detection:** projector tracks last-applied epoch;
  on Head epoch change or Head<lastApplied → re-snapshot from genesis (Subscribe
  from 0) + via.events.epoch_reset. (Record/Head already carry Epoch from tick-15.)
  NOTE: full snapshot/compaction is P5; P4c is just the reset DETECTION + re-fold.

### Next step — BUILD P4a (envelope + drop-on-undecodable + forward-incompat halt)
under TDD. Patch the spec's envelope godoc to the auto-derived-TypeTag v1 decision
(T-DX-3) as part of the build's design-gate step.

---

## Tick 37 — 2026-06-04 — P4a SHIPPED: versioned event envelope + drop-on-undecodable + forward-incompat halt

Phase 4 converged (bc894ae). This tick builds P4a; spec Codec godoc patched to the
T-DX-3 auto-derived-TypeTag v1 decision (done in this tick's design-gate step).

### Built + result
- NEW `eventenvelope.go`: `eventEnvelope{T,V,D json.RawMessage}`,
  currentEventVersion=1, eventTypeTag[E]()=reflect.TypeFor[E]().String() (T is
  diagnostic-only in v1; VERSION is load-bearing).
- `backplane.go`: ADDED ErrUndecodable + ErrForwardIncompatible (first code path —
  P1.0 had deferred them; the carry-forward note wrongly assumed they existed).
- `stateappevents.go`: Append wraps payload in {T,V:1,D}; foldBytes decodes the
  envelope — bad envelope/payload → ErrUndecodable, env.V>1 → ErrForwardIncompatible,
  else fold.
- `applog.go`: logState.halted; projector switch — fold→advance+broadcast;
  ErrForwardIncompatible→halt (FREEZE, do NOT advance cursor, roll-forward-only)+
  via.events.forward_incompatible; ErrUndecodable→skip (advance cursor)+
  via.events.undecodable. Keeps DRAINING the channel when halted (no Subscribe-
  sender leak). Broadcast only on a real fold (tightening — no regression).
- Tests: NEW eventenvelope_internal_test.go — foldBytes 4 cases (fold / garbage→
  Undecodable / v2→ForwardIncompat / bad-payload→Undecodable); projector
  poison-SKIP (projection [1,2], cursor advanced to 3, undecodable metric) +
  forward-incompat HALT (projection frozen [1], cursor frozen at 1, metric). Existing
  StateAppEvents/chat/counter suites GREEN (round-trip end-to-end). `go test -race
  ./...` GREEN (13 pkgs), vet clean.
- Yellow: cursor not directly asserted → added cursor==3 (poison advance) + cursor==1
  (halt freeze) assertions (the roll-forward invariant); switched to require.Eventually
  + t.Parallel. Round-trip covered by existing runtime tests (encode≠decode would break
  them).
- Blue: full coverage; T-field untested-diagnostic (acceptable, documented); Marshal-
  error branches reachable-defensive (infallible given inner success).
- Audit: CLEAN, no bugs. Gates correct (forward-incompat no-advance, undecodable
  advance, errors.Is identity-match); no leak on halt (drains, exits on close); no
  broadcast regression; RawMessage round-trip consistent; ZERO coupling to value
  path/conformance (envelope only in stateappevents/eventenvelope); V<1/pre-envelope
  edge → ErrUndecodable (safe).

### Next step — P4b (upcaster registry): RegisterEvent[E](fromV,toV, fn) decode-chain
run stored-version→current BEFORE unmarshal, so Fold only sees current-shape E;
additive-first godoc. Then P4c (Epoch/offset-space-reset detection +
via.events.epoch_reset). After P4 → P5 (snapshot+compaction). v1 = through P5.

---

## Tick 38 — 2026-06-04 — P4b: upcaster registry + design gate (T-DX-4 RegisterEvent API)

P4a shipped (1b22fb5). This tick adds the decode-only upcaster chain so stored
older-version events decode into the current E shape.

### Validated against code
- `stateappevents.go` bindApp foldBytes (~53-68) captures E in a closure + decodes
  the envelope; Append (~116-130) stamps `V: currentEventVersion` (const 1). The
  closure is the natural seam to run a per-type upcaster chain (E in scope).
- `eventenvelope.go` currentEventVersion is a global CONST (1). With upcasters,
  the current version is PER-TYPE → derive it from the registry (default 1 when no
  upcaster registered → identical to P4a, fully additive).

### DESIGN GATE — T-DX-4 (RegisterEvent API + per-type version)
Converged (the spec's "RegisterEvent[E](v, Upcaster{From,To,Fn})" is the agreed
feature; this pins the concrete API):
- `func RegisterEvent[E any](fromVersion int, upcast func(old json.RawMessage)
  (json.RawMessage, error))` — registers a single-STEP upcaster migrating a
  version-`fromVersion` payload of type E to `fromVersion+1`. Called at init/setup
  BEFORE Mount/Append. The CURRENT version of E = 1 + max(fromVersion registered),
  or 1 if none.
- Global registry keyed by `reflect.TypeFor[E]()` (upcasters are per event TYPE,
  not per key), guarded by a RWMutex (writes at init, reads at runtime → race-safe).
- Append stamps env.V = currentVersionFor[E]() (was the const). foldBytes: read
  env.V; cur := currentVersionFor[E](); env.V>cur → ErrForwardIncompatible; env.V<cur
  → run the chain env.V→cur (any missing step → ErrUndecodable, reusing drop-on-
  undecodable); then unmarshal the upcasted payload into E. No upcasters → cur=1 →
  identical to P4a.
- additive-first godoc: most evolutions need ZERO upcasters (JSON ignores added
  fields, zeroes missing ones); register an upcaster only for a RESHAPE
  (rename/split/type-change).
- Not a maintainer-blocking decision (agreed feature, reasonable API) — recorded
  via the design gate.

### Converged slice — P4b
- **Files:** eventenvelope.go (registry + RegisterEvent + currentVersionFor[E] +
  runUpcasters[E]; keep currentEventVersion as the default-unregistered=1 floor),
  stateappevents.go (Append uses currentVersionFor[E]; foldBytes runs the chain).
- **Acceptance:** an event type with a registered v1→v2 upcaster: a v1-stored
  payload decodes via the upcaster into current-shape E and folds correctly; a
  v2-stored payload folds directly; a stored version with NO chain to current →
  ErrUndecodable (dropped). No-upcaster types behave exactly as P4a (env.V=1).
  Existing StateAppEvents/chat/counter suites GREEN. -race clean.
- Tests: internal (package via) — register an upcaster for a test type, build the
  foldBytes via bindApp, feed a v1 envelope + assert it upcasts+folds to the v2
  shape; feed a version with a gap (no registered step) → ErrUndecodable; confirm
  Append stamps the type's current version.

### Tick 38 — BUILT + result
- `eventenvelope.go`: per-event-type upcaster registry (RWMutex-guarded
  map[reflect.Type]*eventVersionInfo{current,steps}); `RegisterEvent[E](
  fromVersion, upcastFn)` (current=max(current,from+1)); `currentVersionFor[E]()`
  (registered current, else 1); `runUpcasters[E](from,to,d)` (run chain; missing
  step/failing fn → ErrUndecodable).
- `stateappevents.go`: Append stamps `currentVersionFor[E]()`; foldBytes —
  env.V>cur→ErrForwardIncompatible, env.V<cur→runUpcasters then unmarshal, else
  direct. Unregistered types: cur=1 → identical to P4a (fully additive).
- Tests `eventupcaster_internal_test.go`: v1→v2 reshape upcasts+folds (field
  rename — a direct unmarshal would give ""); v2 folds direct; missing-step +
  failing-upcaster → ErrUndecodable; unregistered type stays v1; MULTI-STEP
  v1→v2→v3 runs the full chain; current=1+MAX(fromVersion) order-independent;
  concurrent Register+upcast race-clean. `go test -race ./...` GREEN (13 pkgs).
- Yellow: tests genuinely require the upcaster to run (rename), distinct types,
  no t.Parallel (global registry) — clean.
- Blue: flagged the multi-step chain + max() guard as real-but-untested → added
  both. The info==nil&&from==to passthrough is dead-in-practice defensive (kept).
- Audit: found + FIXED a REAL DATA RACE — runUpcasters read info.steps AFTER
  releasing the RLock, racing a concurrent RegisterEvent's map write; fixed by
  snapshotting the needed steps under the RLock (user fns run after unlock, off
  the map) + added TestConcurrentRegisterAndUpcastIsRaceClean. Version math
  off-by-one-free; additivity confirmed; ErrUndecodable drop-not-misfold correct.

### Phase 4 status — P4a✅ envelope+drop+forward-incompat · P4b✅ upcaster registry.
### Next step — P4c: Epoch/offset-space-reset DETECTION. The projector tracks the
last-applied epoch; on a Head epoch CHANGE or Head < lastApplied offset (a Redis
XTRIM-to-empty / recreated stream / PG restore), re-snapshot from genesis
(Subscribe from 0, reset the projection to seed + cursor 0) + via.events.epoch_reset.
Record/Head already carry Epoch (tick-15). NOTE the in-memory backplane never
resets (epoch stays 0), so test via a small epoch-bumping backplane decorator or
an internal projector test. After P4c → P5 (snapshot+compaction). v1=through P5.

---

## Tick 39 — 2026-06-04 — P4c: Epoch/offset-space-reset detection (in-band)

P4b shipped (dc3e895). This tick adds reset detection so a recreated/trimmed/
restored stream re-snapshots instead of silently skipping every new record.

### Validated against code
- `applog.go` startProjector: `for rec := range ch` → cursor-gated fold (post-P4a
  halt/skip switch, broadcast-on-advanced outside the lock). logState{projection,
  cursor,seed,foldBytes,halted} — NO epoch field yet.
- `backplane.go:24-30` Record carries Epoch; Head returns (Offset,Epoch,error).
  InMemory always sets Epoch 0 (never resets).

### Converged P4c slice
- logState gains `epoch Epoch` + `epochSeen bool`.
- Extract the per-record logic into `(a *App) projectRecord(ls,key,rec) (advanced
  bool)` (testable without a network): under ls.mu — if halted return false; epoch
  detection: first record sets the baseline epoch; a LATER record whose Epoch !=
  ls.epoch → RESET (projection=seed, cursor=0, epoch=rec.Epoch, via.events.epoch_reset
  Counter), so the new-epoch records (offsets restart at 1) re-fold from genesis
  rather than being skipped by a bare HWM; then the existing offset-cursor +
  halt/skip fold. The goroutine becomes `for rec := range ch { if a.projectRecord(...)
  { a.broadcastRender(nil,nil,key) } }` (broadcast still outside the lock).
- In-band epoch-change is the primary signal (a reset is followed by writes). The
  Head<lastApplied backstop (for a reset with NO subsequent write yet) is noted as a
  later refinement — it needs a periodic Head poll the projector doesn't do; the
  reconcile-sweep pattern could host it post-v1.
- InMemory unchanged (epoch always 0 → epochSeen sets 0, no reset). Existing
  StateAppEvents/chat/counter suites stay green.
- Test (internal, package via): feed projectRecord crafted Records — same-epoch
  records fold [1,2]; a new-Epoch record (offset restarts at 1) RESETS → projection
  becomes [9] (NOT [1,2,9]), cursor 1, epoch 1, via.events.epoch_reset fired. Reuse
  goodEnv/spyMetrics.

### Tick 39 — BUILT + result → ✅ PHASE 4 COMPLETE
- `applog.go`: logState gains epoch+epochSeen; per-record logic extracted into
  `projectRecord(ls,key,rec) (advanced bool)` (the shared fold path); epoch
  detection before the offset gate — first record sets baseline, a DIFFERING
  epoch re-snapshots from genesis (projection=seed, cursor=0, via.events.epoch_reset)
  so a recreated/trimmed/restored stream's restarted offsets aren't skipped by a
  bare HWM. Goroutine: `for rec := range ch { if projectRecord(...) { broadcast }}`.
- Tests: NEW epochreset_internal_test.go — same-epoch folds [1,2] (no spurious
  reset), epoch-1 reset → [9] (genesis re-snapshot, NOT [1,2,9] / NOT frozen) +
  cursor 1 + epoch_reset metric, [9,10] post-reset fold, BACKWARD epoch reset → [5].
  `go test -race ./...` GREEN (13 pkgs), vet clean.
- Yellow: added baseline-no-spurious-reset + backward-epoch assertions.
- Blue: all branches covered (the existing P4a/P4b/runtime tests now flow through
  projectRecord — the extracted shared path); no dead code; halted-early-return
  still hit by the forward-incompat test. No HIGH gaps.
- Audit: CLEAN, no bugs. Extraction byte-identical for the no-epoch path (InMemory
  epoch always 0 → never resets); reset uses ls.seed (genuine zero V); broadcast
  OUTSIDE the lock (defer-unlock then broadcast); halted key never epoch-resets
  (intended — roll-forward-only, an epoch reset doesn't make this binary newer);
  -race clean; Offset(0) never delivered (contract) so the cursor-0 gate is a
  harmless guard.

### ✅ PHASE 4 COMPLETE — P4a envelope+drop+forward-incompat · P4b upcaster registry
· P4c epoch/offset-space-reset detection. StateAppEvents is now version-hardened:
events evolve via upcasters, poison records drop (never wedge), newer-binary
records halt (never mis-fold), and stream resets re-snapshot (never silently skip).

### Next step — PHASE 5 (the LAST v1 phase): snapshot + compaction.
Snapshot-durable-FIRST (write a fold snapshot to the Store, THEN optionally
Compact), the OPTIONAL Compactor (type-asserted on the Backplane — backends that
can't truncate decline + run snapshot-only), the retained-event floor (Compact
clamped below the 2nd-newest snapshot + consumer checkpoints), T2-GO-4
compacted-key migration (snapshot becomes durable genesis; typed Codec[V] +
seeded migration on codec-hash mismatch). Cold start = LoadSnapshot →
Subscribe(from:coveredOffset) → fold the tail. Sub-slice: (5a) snapshot write +
cold-start-from-snapshot (Store-only, no Compactor) first; (5b) optional Compactor
+ retained-event floor; (5c) T2-GO-4 compacted-key migration. After P5 → v1 GREEN
→ STOP + PushNotification.

---

## Tick 40 — 2026-06-04 — P5a: snapshot write + cold-start-from-snapshot (Store-only)

Phase 4 done (ca417ab, pushed). Phase 5 (LAST v1 phase) begins; 5a = the snapshot
cache + cold-start. Compactor (5b) + compacted-key durable-genesis migration (5c)
follow.

### Validated against code
- `applog.go` registerLog(key,seed,fold) creates logState{projection:seed,...};
  startProjector Subscribes from 0 ALWAYS + ranges → projectRecord. logState has
  projection/cursor/seed/foldBytes/halted/epoch/epochSeen. `bindApp` (stateappevents.go)
  builds the typed fold closure (V in scope) — the natural place to also capture
  the snapshot encode/decode (the App is type-erased).
- `backplane.go` Store LoadSnapshot/CAS (bytes+rev). The snapshot rides the same
  Store under a distinct key.

### Converged P5a
- config: `snapshotInterval int` (default 64; WithSnapshotInterval(n); <=0 disables
  writes). Snapshot is a pure disposable CACHE for uncompacted keys — never
  required for correctness (cold-start without one re-folds from 0).
- Checkpoint{Epoch, CoveredOffset Offset, CodecHash string, V json.RawMessage};
  snapKey(wireKey)="snap:"+wireKey. CodecHash = reflect.TypeFor[V]().String()
  (a V-type cache key — a type change invalidates → re-fold; serviceable for v1).
- logState gains encodeSnap(any)→[]byte, decodeSnap([]byte)→any, codecHash string,
  snapRev Rev, foldsSinceSnap int. bindApp captures encodeSnap=json(V),
  decodeSnap=json→V, codecHash.
- registerLog signature carries the snap codec (encode/decode/hash).
- startProjector COLD-START: LoadSnapshot(snapKey) → decode Checkpoint → if ok &&
  CodecHash==ls.codecHash → seed ls.projection=decodeSnap(cp.V), cursor=cp.CoveredOffset,
  epoch=cp.Epoch, epochSeen=true, from=cp.CoveredOffset; else from=0. Subscribe(from).
- WRITE (off the hot path): projectRecord increments foldsSinceSnap on advance; the
  GOROUTINE, after an advanced fold, if foldsSinceSnap>=interval → writeSnapshot
  (RLock to read projection/cursor/epoch, encodeSnap, marshal Checkpoint,
  CAS(snapKey, ls.snapRev) best-effort — on ErrCASConflict a peer snapshotted, refresh
  snapRev + skip; on success snapRev=newRev), reset foldsSinceSnap. CAS is OUTSIDE
  ls.mu (no backplane I/O under the projection lock). NOTE: a per-fold snapshot would
  reintroduce write-amplification — the interval keeps the event-log's no-CAS-per-event
  win; tune via WithSnapshotInterval.
- Tests (internal): (1) cold-start seeds from a pre-written CONTRIVED snapshot +
  replays only the tail — append 5 real events (offsets 1-5), write a Checkpoint
  {CoveredOffset:5, V:[99] (distinct from the real fold), CodecHash:ok}, bind → assert
  projection==[99] (seeded, NOT the from-0 re-fold of 1-5) + cursor 5; append event 6 →
  folds → [99,<e6>] (tail only). (2) codec-hash MISMATCH → ignore snapshot, Subscribe
  from 0, re-fold all 5 → projection==fold(1..5) (NOT [99]). (3) write: with
  WithSnapshotInterval(1), append events → a Checkpoint appears in the Store at the
  right CoveredOffset with the folded V. Keep existing StateAppEvents/chat/counter green.

### Built (P5a — shipped green)
- config.go: `snapshotInterval int` (default 64 in New's cfg literal) + `WithSnapshotInterval(n)` (<=0 disables; godoc says disposable cache, never required for correctness).
- applogsnap.go (new): `checkpoint{Epoch,CoveredOffset,CodecHash,V json.RawMessage}`; `snapKey="snap:"+wireKey`; `maybeSnapshot` (interval gate, resets foldsSinceSnap under ls.mu); `writeSnapshot` (read projection/cursor/epoch/snapRev under RLock, encode, marshal, CAS OUTSIDE ls.mu — ErrCASConflict→LoadSnapshot refresh snapRev+skip, success→setSnapRev); `setSnapRev`.
- applog.go: logState +encodeSnap/decodeSnap/codecHash/snapRev/foldsSinceSnap; registerLog carries them; startProjector cold-starts via LoadSnapshot(snapKey) seeding projection/cursor/epoch/epochSeen/snapRev when CodecHash matches else from=0; projectRecord increments foldsSinceSnap on advance; goroutine calls maybeSnapshot after an advanced fold.
- stateappevents.go bindApp: encodeSnap=json(V), decodeSnap=json→V, codecHash=reflect.TypeFor[V]().String() (StateAppCounter inherits via embed — no change).

### Tests added (applogsnap_internal_test.go, all green -race)
- TestColdStartSeedsFromSnapshotAndReplaysOnlyTheTail — contrived [99] snapshot @offset5 + tail-fold [99,6] proves seed-not-refold.
- TestColdStartIgnoresSnapshotOnCodecHashMismatch — wrong hash → re-fold [1..5].
- TestProjectorPersistsSnapshots — WithSnapshotInterval(1) → checkpoint @offset3, V=[1,2,3], hash matches.
- TestSnapshotWritesDisabledWhenIntervalNonPositive — WithSnapshotInterval(0) → 20 folds, NO snapshot (covers the interval<=0 branch flagged by Blue).
- TestRoundTripPersistedSnapshotSeedsAFreshProjector — pod A persists, fresh pod B (shared backplane) cold-starts from it + folds the tail. The real end-to-end (write+cold-start interoperate), not two halves vs a fixture.

### TDD-rygba record
Red (3 tests, undefined checkpoint/snapKey/WithSnapshotInterval) → Yellow (Explore: suite watertight, no changes) → Green (impl above) → Blue (Explore: flagged interval<=0 + round-trip as real testable gaps; ErrCASConflict path needs a conflict-injecting mock → deferred to P5b) → Audit (general-purpose: no bugs; verified V/CoveredOffset read as a consistent pair under one RLock, no off-by-one in Subscribe(from:CoveredOffset), cold-start snapRev set so first write CASes the right rev, snap:/val: no collision; -race clean). Closed Blue's two gaps with the two extra tests. Full suite + vianats green & race-clean.

### Next step — P5b: optional Compactor (type-asserted) + retained-event floor.
Then P5c (T2-GO-4 compacted-key durable-genesis migration) → v1 (P0–P5) COMPLETE → STOP loop + PushNotification.

## Tick 41 — 2026-06-04 — P5b: optional Compactor + retained-event floor (one-generation lag)

### Validated against code (file:line evidence)
- Compactor specced (state-backplane.md:300) but ABSENT from code → added to backplane.go (optional, NOT in Backplane; type-asserted).
- in-memory log used DENSE 1-based offsets, direct-indexed: Subscribe `records[cursor:]` (inmemory.go:118), Head=`len(records)` (102), append off=`len+1` (44). A physical front-trim would break the offset↔index identity → memLog needed a `base Offset`.
- Spec EXPLICITLY anticipates compaction holes ("not guaranteed gap-free ... compaction ... create holes", state-backplane.md:162) → NO design-feedback-gate trip; the base-offset model matches the spec's opaque-offset contract.
- writeSnapshot CAS-success branch (applogsnap.go:75) = the snapshot-FIRST point; compact-SECOND hangs there.

### Converged P5b slice
- backplane.go: `Compactor interface { Compact(ctx, key, beforeOffset Offset) error }` — discard Offset<beforeOffset, retained offsets UNCHANGED, idempotent+monotone+clamped-to-head.
- inmemory.go: memLog `base Offset`; append off=base+len+1; Head=base+len; Subscribe start=max(0,cursor-base); compact(before) (clamp to head+1, no-op <=base+1, reslice into a FRESH backing array, base=before-1); inMemoryBackplane.Compact.
- applog.go: logState.prevSnapOffset.
- applogsnap.go: writeSnapshot CAS-success → maybeCompact(ls,key,cp.CoveredOffset). Floor = LAG ONE GENERATION: Compact(before:prevSnapOffset) then prevSnapOffset=covered. First snapshot → Compact(before:0)=no-op. Guarantees the current durable snapshot's offset is never truncated (cold start always resumes) + ≥1 generation of tail retained for in-flight subscribers. Decline path: backend without Compactor → snapshot-only.

### Built + tests (applogcompact_internal_test.go — all green -race)
- TestCompactDropsPrefixButKeepsRetainedOffsetsStable — Compact(before:4) → Subscribe(0) yields {4,5} (offset STABILITY, no renumber).
- TestCompactIsIdempotentAndClampedToCommitted — before:0/2/2/1/999, head invariant.
- TestAppendAfterCompactContinuesMonotoneOffsets — append after compact → Offset 6 (continues from head, not base-relative) [Blue gap 1].
- TestSequentialCompactionsAdvanceMonotonically — floor rises 4→7, backward(5)=no-op, head invariant [Blue gap 2].
- TestProjectorAutoCompactsTrailingTheDurableSnapshot — lowestRetained>1 AND <=cp.CoveredOffset (snapshot-FIRST).
- TestFreshProjectorColdStartsAfterPrefixCompacted — payoff: fresh pod reaches [1..5] from the snapshot though offsets 1..3 are gone.
- TestSnapshotOnlyWhenBackendDeclinesCompaction — embedded-Backplane wrapper hides Compact → snapshot-only, prefix intact.

### TDD-rygba record
Red (Compactor undefined) → Yellow (Explore: tests airtight; flagged before:0 no-op → added) → Green → Blue (Explore: flagged append-after-compact + sequential-monotone as real testable invariants → added 2 tests; ErrClosed/Compact-error-ignore = acceptable defensive) → Audit (general-purpose: no bugs, 20x -race stress on projector-compacts-while-tailing clean; verified no batch/backing-array aliasing, beforeOffset=0 underflow unreachable by guard order, snapshot-first invariant holds). Conformance suite + memevents.Faulty green (offset model transparent to both).

### KNOWN LIMITATION (follow-up, post-v1 / P6 real backends)
prevSnapOffset is NOT reset on an epoch reset (projectRecord resets cursor/projection, not prevSnapOffset). For an external backend that BOTH resets epochs AND implements Compactor, a stale large floor across an epoch boundary has undefined semantics. UNREACHABLE in shipped code (in-memory — the only Compactor — never resets epoch; its clamp keeps it safe regardless). Flag when an epoch-resetting Compactor backend lands.

### Next step — P5c: T2-GO-4 compacted-key durable-genesis migration.
Once a key has compacted, the snapshot is durable GENESIS (the prefix is unrecoverable), so a codec-hash mismatch must NOT discard+re-fold-from-0 (that would silently truncate to the uncompacted tail) — it must run a SEEDED migration (decode old V → seed, fold the retained tail, rewrite the checkpoint). Needs: a "has this key compacted" signal + the seeded-migration path on mismatch. Then v1 (P0-P5) COMPLETE → STOP loop + PushNotification.

## Tick 42 — 2026-06-04 — P5c: T2-GO-4 compacted-key durable-genesis migration → v1 COMPLETE

### Validated against code (file:line)
- checkpoint{Epoch,CoveredOffset,CodecHash,V} (applogsnap.go:14) — needed a Compacted flag.
- startProjector cold-start (applog.go) mismatch → always from=0 (discard+refold) — unsafe once a key compacts (the prefix is gone → "refold from 0" truncates to the surviving tail).
- Compaction floor lags one generation (prevSnapOffset); a prefix is physically dropped iff prevSnapOffset>=2 (Compact(before:N) no-ops for N<=1). So Compacted ⟺ prevSnapOffset>=2, computable in writeSnapshot.
- maybeSnapshot/writeSnapshot/maybeCompact all run on the SINGLE projector goroutine → no extra sync for prevSnapOffset; writeSnapshot CAS (durable) precedes maybeCompact (drop) → snapshot-FIRST.
- No snapshot-migration registry existed.

### Converged + Built (T2-GO-4)
- applogmigrate.go (new): RWMutex snapMigrations map[oldCodecHash]func([]byte)(any,error); RegisterSnapshotMigration[V](fromCodecHash, migrate func([]byte)(V,error)) (type-erases V, propagates error); lookupSnapMigration; deleteSnapMigration (test cleanup).
- applogsnap.go: checkpoint += Compacted bool (json "c"); writeSnapshot sets cp.Compacted = prevSnapOffset>=2 under RLock (durable-FIRST, before the drop).
- applog.go: 3-way cold-start — (match) seedFromSnapshot; (mismatch && !Compacted) from=0 refold [V-evolution free for uncompacted, unchanged]; (mismatch && Compacted) lookupSnapMigration → migrate ok → seedFromSnapshot + fold tail, else/error → haltUnbridgeable (ls.halted + via.snapshot.unbridgeable metric, roll-forward-only, NEVER truncate). Helpers seedFromSnapshot / haltUnbridgeable; reuses projectRecord's halted short-circuit.

### Tests (applogmigrate_internal_test.go — all green -race)
- TestRealProjectorMarksCheckpointCompactedAfterDiscardingPrefix — the load-bearing P5b↔P5c link: a REAL projector (interval 1, 5 events) stamps Compacted:true on the durable checkpoint once it discards a prefix [Blue HIGH gap].
- TestCompactedKeyRunsSeededMigrationOnCodecMismatch — compacted+mismatch+registered → seed [10..50] (UNREACHABLE by any fold: genesis would be [1..5], truncated tail [4,5]) + tail folds to [10..50,6].
- TestCompactedKeyHaltsWhenNoMigrationRegistered — halt + metric + require.Never(folds) → no silent truncation.
- TestCompactedKeyHaltsWhenMigrationErrors — failing migration halts (fail-closed), not truncate.
- TestUncompactedKeyStillRefoldsFromGenesisOnMismatch — locks the !Compacted branch (refold [1..5], not seed [99]).

### TDD-rygba record
Red (Compacted field + RegisterSnapshotMigration undefined) → Yellow (Explore: tests airtight; flagged registry contamination → added defer deleteSnapMigration) → Green → Blue (Explore: flagged the real-projector-Compacted-link as HIGH → added the link test; metric-is-halt-only confirmed correct) → Audit (general-purpose: durable-first invariant PROVEN by gen-trace 1..6 — "dropped ⟹ durable ckpt.Compacted" holds at every step incl. crash windows; halt never un-halts, metric once-per-cold-start; mismatched-V registration = documented footgun, no crash; -race clean across via + backplanetest + vianats).

### v1 SCOPE COMPLETE — P0–P5 all shipped green & race-clean
- P0 binding seam · P1 in-process core (via.InMemory) · P2 Backplane hardening + memevents.Faulty + conformance + NATS · P3 value path · P4 versioning (envelope/upcasters/epoch) · P5a snapshot+cold-start · P5b Compactor+floor · P5c durable-genesis migration.
- Full suite (13 pkgs) + backplanetest conformance + vianats all green under -race.

### Post-v1 (P6, NOT in scope): OnEvent side-effects; Redis/PG/Kafka backends; AppendIf/ReadAt; WithFoldVerify dev-mode + go-vet purity analyzer; the epoch-reset×Compactor floor follow-up (Tick 41). v1 build loop STOPS here.

## Tick 43 — 2026-06-04 — P6a: OnEvent side-effect consumer (council deliberation → consensus)

POST-v1. User opened P6, chose OnEvent first, and asked the council to deliberate the handler signature to consensus. Multi-lens validation against the code (Explore, file:line evidence) reached consensus:

### Consensus — handler signature (DESIGN GATE T-DX-6: spec's *Ctx is wrong)
`func (l *StateAppEvents[E,V]) OnEvent(name string, fn func(ctx context.Context, ev E, off Offset) error)`
- runtime/SRE + Go-idioms: a background tailer fires on records appended by ANY pod — no originating tab/session/request, so a via *Ctx would be nil/synthetic (a category error). context.Context is the honest type: the shutdown/cancellation seam (the tailer exits when the backplane closes), idiomatic for background work.
- DX: the handler's inputs are the decoded event + offset (for idempotency keys); DB/mail clients are captured in the closure. KEEP `off Offset` (the council #1 resolution's idempotency-key = wireKey+":"+off needs it). Drop *Ctx.
- testing: in-memory testable end-to-end (register, append, assert handler fired with ev+off, assert committed offset, assert restart resume).
Spec patched (state-backplane.md OnEvent godoc) to context.Context.

### Consensus — mechanics (validated against code)
- REGISTRATION mirrors registerLog: idempotent register-ONCE per (name,wireKey) via sync.Once, callable from a View/OnInit/action where l.app is bound (walker→bindApp sets l.app, runtime.go:215). App gains a consumers registry (parallels a.logs/a.valStates).
- DECODE-TO-E reuses the envelope+upcaster path (eventEnvelope/currentVersionFor/runUpcasters); OnEvent is typed (E,V in scope) so it builds the decode closure itself and hands a type-erased deliver() to the App. Drop-on-undecodable: poison record → skip + commit-advance + via.events.undecodable (same as the fold), never wedge.
- OFFSET COMMIT: separate tailer Subscribe(from:committed); after handler nil → CAS the Store cell "consumer:<name>:<wireKey>" with the new offset, advance. Handler error → DO NOT advance; re-subscribe from committed (head-of-line, preserves order + at-least-once). Cold-start loads the committed cell (like the projector). ErrCASConflict → adopt the peer's higher offset.
- CLUSTER: per-pod tailer + shared Store offset + at-least-once; dups deduped by the handler's idempotency key. NO leader election exists (confirmed) — intentional, documented.

### DESIGN GATE (CRITICAL, closed in this slice) — consumer-aware compaction floor
Council line 272 mandates Compact clamp to min(2nd-newest snapshot, min consumer checkpoints); the SHIPPED maybeCompact (applogsnap.go) floor = prevSnapOffset ONLY → a registered consumer lagging behind the snapshot would be TRUNCATED OUT (silent side-effect event loss). Fix in this slice: maybeCompact floor = min(prevSnapOffset, minConsumerOffset(key)); a consumer at committed 0 blocks compaction entirely (correct — can't drop what it hasn't processed).

### Slice (P6a) + TDD next: App consumers registry + startConsumer tailer + commit/cold-start + decode-drop-poison + consumer-aware floor. Tests: handler-fires-with-ev+off; out-of-Fold (projection unaffected); restart-resume-from-committed; handler-error-retries-in-order (at-least-once); poison-skip+commit; compaction-blocked-by-slow-consumer. P6b/c (more backends, AppendIf/ReadAt) later.

## Tick 44 — 2026-06-04 — P6a: OnEvent side-effect consumer (BUILT, post-v1)

Built the P6a slice converged in Tick 43. TDD-rygba.

### Built
- onevent.go: OnEvent(name, fn func(ctx context.Context, ev E, off Offset) error); consumerState (committed offset + cellRev); consumerKey="consumer:<name>:<wireKey>"; registerConsumer (idempotent once per name+key, cold-loads committed from the Store); startConsumer (per-pod tailer goroutine: Subscribe(from:committed) → deliver → commit; handler error → re-subscribe from committed (head-of-line retry, backoff); exits on backplane Close); commitConsumer (CAS the cell; ErrCASConflict → adopt the peer's higher offset, monotone); minConsumerOffset (for the compaction floor).
- eventenvelope.go: extracted shared decodeEvent[E] (envelope unmarshal → version gate → upcasters → unmarshal E); stateappevents.go fold refactored to call it (behavior-preserving, P4 tests green).
- applogsnap.go maybeCompact: floor = min(prevSnapOffset, minConsumerOffset(key)) — closes the council-line-272 design gate (compaction never discards an unprocessed consumer event).
- app.go: consumers/consumersByKey/consumersMu.

### Tests (onevent_internal_test.go, all green -race, 3x clean)
- fires-per-event-with-offset + fold-pure; restart-resume-from-committed (gated on durable commit via awaitConsumerCommitted); retry-failed-in-order (head-of-line, at-least-once) + via.consumer.error metric; skip-poison + via.consumer.undecodable; forward-incompatible BLOCKS (does not advance) + via.consumer.forward_incompatible; CAS-conflict adopt-peer (two pods converge); compaction-floor-respects-slow-consumer; compaction-advances-when-consumer-keeps-up.

### TDD-rygba record
Red → Yellow (Explore: flagged restart commit-race → added awaitConsumerCommitted barrier; goroutine cleanup → defer backplane.Close; consumer-specific metric → via.consumer.undecodable; +positive floor-advance test) → Green → Blue (Explore: found a REAL BUG — deliver() treated ErrForwardIncompatible like poison (skip+advance) → SILENT side-effect loss on deploy-skew; also flagged CAS-adopt + via.consumer.error coverage) → Green2 (fixed: forward-incompat now BLOCKS roll-forward-only + distinct metric; added forward-incompat-block test, deterministic CAS-adopt test, error-metric assertion) → Audit (general-purpose: no further bugs; goroutine exits reliably on Close (both paths), at-least-once gate correct, CAS monotone + adopt no-loop, no lock-order inversion consumersMu→cs.mu, decodeEvent parity with old fold, consumer/projector independence; 3x -race clean).

### DESIGN GATE closed: T-DX-6 (handler ctx = context.Context not *Ctx) + council-line-272 (consumer-aware compaction floor). Spec patched.

### Next P6 (later, separate slices): Redis/PG/Kafka backends (need infra modules); AppendIf/ReadAt (gated on a real claim-ticket case); WithFoldVerify dev-mode + go-vet purity analyzer; epoch-reset×Compactor floor follow-up (Tick 41); the OnEvent shutdown-ctx refinement (handler ctx is per-delivery, not force-cancelled on Shutdown — acceptable v1).
