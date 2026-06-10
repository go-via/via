# Hygiene Sweep Log

Branch `chore/hygiene-sweep`. One structural/hygiene improvement per tick.
Special attention to the backplane feature (commit e6ec133).

## Tick 1 — extract cold-start snapshot seeding from startProjector

What: `applog.go` `startProjector` mixed two concerns — a ~50-line inline
snapshot cold-start switch and the reconnect/fold goroutine. Extracted the
cold-start into `coldStartFrom(ls, key) Offset`.

Why: the goroutine's job (tail → fold → fan out) was buried under seeding
logic; the seeding branches (erasure-stale, codec match, migration, halt)
are independently testable reasoning that deserves its own named unit.

Result: pure refactor, behavior preserved on every branch (halt cases still
return the snapshot's covered offset). `go build`, `go vet`, and
`go test -race .` all green.

## Tick 2 — fix sanitize doc/impl mismatch + lock underscore escaping

What: `vianats/vianats.go` `sanitize` doc claimed the safe set was
`[A-Za-z0-9_-]` (underscore passes through), but the switch escapes `_` to
`_5f_`. Corrected the comment to `[A-Za-z0-9-]` and documented that `_` is the
escape delimiter (so a literal `_` must itself be escaped or "a_b" collides
with the encoding of "a.b"). Added a characterization case `"a_b" -> "a_5f_b"`
to the internal test, pinning the invariant the corrected doc describes.

Why: the comment hid the exact invariant that makes the mapping collision-free;
a future edit "simplifying" the switch to allow `_` through would silently
reintroduce subject/KV-name collisions.

Result: doc + characterization test only, no behavior change.
`go test -race ./vianats` green.

## Tick 3 — rename vianats tests to the CONVENTIONS name format

What: all 6 `vianats` test functions used freeform names
(`TestEpochIsNonZeroAndStableAcrossClientsOnTheSameStream`) instead of the
mandated `Test` + Subject + `_` + camelCase-behavior format. Renamed to
`TestEpoch_isNonZeroAndStableAcrossClients`, `TestJetStream_passesBackplane
Conformance`, `TestJetStream_primitivesWorkOnEmbeddedServer`,
`TestEpoch_differsAfterStreamDeleteAndRecreate`,
`TestHead_reportsStreamEpochForEmptyKey`, `TestSanitize_makesArbitraryKeysSafe
AndDistinct`.

Why: the convention separates *what* from *does what* so tests read as
behavioral claims; the backend module had drifted entirely off it.

Result: pure rename, no behavior change. `go test -race ./vianats/...` green.
(Deferred to a later tick: two vianats tests still use raw t.Fatalf instead of
testify — a distinct item.)

## Tick 4 — convert remaining vianats tests to testify

What: `embedded_test.go` (incl. the `startEmbeddedJetStream` helper) and
`sanitize_internal_test.go` used raw `t.Fatalf`/`t.Errorf`, which CONVENTIONS
forbids ("Do not use raw t.Error, t.Fatal … use testify"). Converted to
`require`/`assert` (require for preconditions, assert for behavioral claims).
Also tightened embedded_test's second Publish to check its error instead of
discarding it.

Why: testify gives uniform failure output and the require/assert split makes
precondition-vs-claim explicit; these two files were the last raw-t holdouts in
the backend module.

Result: test-only, behavior preserved. `go test -race ./vianats/...` green.

## Tick 5 — give marshalEvent errors a via: origin prefix

What: `stateappevents.go` `marshalEvent` (the documented error surface of
`StateAppEvents.Append`) returned bare `json.Marshal`/keystore/encrypt errors
with no origin, against the CONVENTIONS "errors carry a short origin prefix"
rule. Added a single named-return `defer` that prefixes every failure path with
`via: marshal event:` (one branch, not four scattered wraps). TDD cycle
(red/yellow/green/blue/audit); new internal test
`TestMarshalEvent_errorNamesViaOrigin`.

Why: a failed event commit surfaced `json: unsupported type` to operators with
no hint it came from the backplane write path. `%v` (not `%w`) per CONVENTIONS;
`ErrClosed` from the backplane bypasses marshalEvent so `errors.Is` still
matches.

Result: `go test -race .` + `go vet ./...` green; no existing error-string
assertions broken.

## Sweep concluded (after tick 5)

The tick-6 survey turned up no genuinely substantive hygiene item — only
intentional design choices or changes that would be churn/risky abstraction.
Recorded here so a future sweep does not re-litigate them:

- Swallowed `LoadSnapshot` errors in `appval.go` `reconcileKey` /
  `reconcileSessionKey` — intentional best-effort, idempotent sweeps that retry
  on the next tick; surfacing the error would be an observability *feature*, not
  hygiene.
- `reconcileKey` vs `reconcileSessionKey` near-duplication — deduping would
  force a leaky app-vs-session abstraction; the parallel structure is clearer.
- `context.Background()` on the event-append write path
  (`stateappevents.go`) — already documented as a deliberate deferred
  refinement (request-context cancellation wiring).

Backplane / core code is clean and well-documented. Stopping the loop;
re-run only if new code lands.

## Follow-up — rename applog* family to stateappevents_* (user-requested)

What: `applog.go` read like "application logging" (collides with `log.go`, the
slog adapter) and was actually the per-key projector RUNTIME behind the public
`StateAppEvents` handle. The whole `applog*` family was `git mv`'d to bind to
the public concept (and unify with the pre-existing
`stateappevents_runtime_test.go`):

- applog.go            → stateappevents_projector.go
- applogsnap.go        → stateappevents_snapshot.go
- applogmigrate.go     → stateappevents_migrate.go
- applog*_internal_test.go → stateappevents_<concern>_internal_test.go (7 files)

Updated two live cross-reference comments in vianats (vianats.go, epoch_test.go)
to the new filename.

Why: a file name should name its concept; `applog` was ambiguous against actual
logging and split from the `stateappevents`/`stateapp`/`statesess` family.

Result: pure file rename (package unchanged), `git mv` preserves history.
`go build ./...` + `go test -race .` (via) and `go test -race ./...` (vianats)
all green.

## Follow-up — align every test file with its source file (user-requested)

Goal: each `_test.go` should correspond to a same-named source file (one
concept/responsibility per source file + its test). Surveyed all packages,
fixed the offenders (51 → 16 unmatched, all remaining legit). Same-package
scenario test files were merged (goimports reconciled imports — no
duplicate-declaration risk since they already shared the package); internal
(`package via`) vs external (`package via_test`) tests kept as separate files
that share the base name.

Renames/merges (all `git mv` / history-preserving):

- `marshalevent_internal_test.go` → `stateappevents_internal_test.go`
  (marshalEvent lives in stateappevents.go — fixes a name I introduced in tick 5)
- `ctxr_test.go` → folded into `ctx_test.go` (CtxR is in ctx.go)
- `eventupcaster_internal_test.go` → folded into `eventenvelope_internal_test.go`
- `queue_order_test.go` → folded into `action_test.go`
- 8 projector scenario tests (canary/compact/noncontiguous/reconnect/reseed/
  epochreset/foldpurity/foldverify) → `stateappevents_projector_internal_test.go`;
  `stateappevents_runtime_test.go` → `stateappevents_projector_test.go`
- `sse_brotli_test.go` + `sse_liveness_test.go` → `sse_test.go`;
  `sse_recover_test.go` → `recover_test.go`
- `backplane_{crosspod,crosspod_value,reconcile,wire}_test.go` → `backplane_test.go`
- `sess_adopt_internal_test.go` + `sess_change_internal_test.go`
  → `sess_internal_test.go`
- vianats: `conformance/embedded/epoch_test.go` → `vianats_test.go`;
  `sanitize_internal_test.go` → `vianats_internal_test.go`
- picocss: `pico_options_test.go` → `pico_test.go`;
  `pico_fetch_test.go` → `pico_internal_test.go`
- maplibre: `multimap_test.go` → `map_test.go`; `withmarker_test.go`
  → `markers_test.go`
- echarts: `runtime_error_test.go` → `runtime_test.go`
- vt: `vt_isolation_test.go` → `vt_test.go`
- showcase: `join_test.go` → `room_join_test.go`;
  `notice_internal_test.go` → `room_host_internal_test.go` (buildNoticeScript
  lives in room_host.go)

Deliberately LEFT (legit standalone, not 1:1 with a single source file):
benchmarks (`*bench_test.go`), integration/e2e (`integration_test.go`,
`e2e_test.go`, showcase `convergence_test.go`), test-only helpers
(`helpers_test.go`), Go example convention (`example_test.go`), and genuinely
cross-cutting protocol tests that exercise several source files on purpose
(`shape_test.go` — one page over every shape by design; `patch_test.go`,
`csp_push_test.go`, `h/render_test.go`).

Result: gofmt clean; `go vet ./...` + `go test -race ./...` green across via,
vianats, viashowcase, and all plugins.

## Follow-up — split the projector source + redistribute its tests (user-requested)

The 1:1 alignment left `stateappevents_projector_internal_test.go` at ~1260
lines because the projector source was one 373-line file doing four jobs. Split
both source and tests by responsibility (package unchanged — the functions share
the `logState` struct, not control flow, so it is a clean file split, not a
package boundary; an `internal/projector` package was considered and rejected
because every function is an `*App` method woven into App's mutable state, which
would force a wide speculative interface).

Source (was `stateappevents_projector.go`, 373 lines → 7 files, 39–125 lines):

- `stateappevents_projector.go` — lifecycle (logState, registerLog,
  startProjector, shuttingDown, logProjection)
- `stateappevents_coldstart.go` — coldStartFrom, seedFromSnapshot, halt*
- `stateappevents_gap.go` — applyRecord, classifyGap, gapClass
- `stateappevents_fold.go` — projectRecord
- `stateappevents_canary.go` — emitFoldDigest (carved from snapshot.go)
- `stateappevents_compact.go` — maybeCompact (carved from snapshot.go)
- `stateappevents_snapshot.go` — maybeSnapshot/writeSnapshot/setSnapRev (slimmed)

Tests: the merged projector test file (35 funcs — 34 Test + 1 Fuzz) was
redistributed to mirror each source file, with shared doubles/helpers in one
`stateappevents_helpers_test.go`; the snapshot test file kept its own 7 funcs
and gained one moved in. The redistribution conserved the function set — the
count before and after matches, none lost or duplicated (an earlier draft of
this note said "42 → 42", conflating the 35-func merged file with snapshot's
pre-existing 7). Every source file now has a matching test file; all at/under
the ~300-line guideline (helpers_test.go excepted as test infrastructure).

Result: gofmt clean; `go vet` + `go test -race .` green.


## Council queue (charted by the hygiene-council workflow, ranked do-now)

### Q1 — fix metrics.go catalogue (phantom labels + missing names)

What: the godoc catalogue documented a `status` label on `via.action.total` /
`via.render.total` that no call site emits (action.go emits only `method`,
render.go only `route`), and listed just 7 of the 24 emitted `via.*` names.
Rewrote the catalogue to the full, grouped, accurate set and dropped the phantom
`status` labels.

Why: it is the doc ops teams read first; a phantom label and 17 undocumented
metrics are an honesty bug (same class as the earlier disconnect-reason fix).

Result: documented set now reconciles exactly against emit sites (both
set-diffs empty); doc-only, `go build ./...` green.

### Q2 — remove write-only ls.snapRev + setSnapRev

What: `ls.snapRev` was written at `seedFromSnapshot` and `setSnapRev` (3 call
sites) but read NOWHERE; its comment claimed it backed the snapshot CAS, yet
`writeSnapshot` CASes against a freshly-read `curRev`. Deleted the field,
`setSnapRev`, the `rev` param of `seedFromSnapshot`, and the now-dead
rev-resync `LoadSnapshot` on the CAS-conflict path.

Why: a write-only field on a shared-mutable struct that advertises load-bearing
synchronization it doesn't provide is a correctness trap; deleting it also
shrinks the logState surface (the council's one true decoupling lever).

Result: pure deletion (no reads existed); `go build`/`vet`/`test -race .` green.
The only behavior delta is one fewer backplane LoadSnapshot on the CAS-conflict
path — unobservable (its result fed only the deleted field).

### Q3 — convert raw t.Fatal in the projector test cluster to testify

What: 12 raw `t.Fatal`/`t.Fatalf` sites across stateappevents_{projector,fold,
gap,helpers}_test.go violated CONVENTIONS ("use testify, not raw t.Fatal").
Converted: 7 append-loop guards + 1 CAS-seed guard → `require.NoError(t, err,
…)`; 2 Shutdown-hang and 1 recv timeout + 1 child-output failure → `require.
FailNow`/`require.FailNow(t, fmt.Sprintf(…))`.

Why: uniform failure output + the testify precondition idiom, matching the
tick-3/4 vianats precedent; the cluster the split created was the last raw-t
island.

Result: test-only; no raw t.Fatal remain; gofmt + vet + `test -race .` green.

### Q4 — re-file TestColdStart* into the coldstart test file

What: the 4 `TestColdStart*` funcs (they exercise `coldStartFrom`) sat in
stateappevents_snapshot_internal_test.go, falsifying the split's 1:1 claim.
Moved them to stateappevents_coldstart_internal_test.go, and relocated their
shared helper `writeContrivedSnapshot` (used only by coldstart + migrate, no
staying snapshot test) into stateappevents_helpers_test.go.

Why: a test file should match the source file it covers; cold-start gate tests
were mis-discoverable under snapshot.

Result: test-only move; snapshot 312→151 lines, coldstart now owns all 5
cold-start tests; gofmt + vet + `test -race .` green.

### Q5 — rename cluster test funcs to Test+Subject_behavior

What: 52 stateappevents cluster test/fuzz funcs used freeform PascalCase names
(e.g. TestFoldDigestIsDeterministicForTheSameSequence) instead of the mandated
Test + Subject + `_` + camelCase-behavior form. Renamed all to the convention,
grouping by subject (FoldDigest/Fold/FoldVerify/ColdStart/Compact/Snapshot/
Projector/Consumer/Migrate/Gap/StateAppEvents).

Why: CONVENTIONS Test-Names rule; the just-created cluster was the largest
non-compliant island. Names now read as behavioral claims.

Result: pure rename (no internal callers; word-boundary global replace), 53
test/fuzz funcs unchanged in count; gofmt + vet + `test -race .` green.

### Q6 — anchor the projector concurrency contract in one doc-block

What: the split scattered the single-writer / ls.mu / I/O-outside-the-lock
invariant across projector.go, gap.go, fold.go, compact.go as partial
restatements. Wrote ONE authoritative contract block on the `logState` type
(single writer = the startProjector goroutine; ls.mu guards fields for concurrent
tab readers; backplane I/O stays outside the lock) and trimmed the three
scattered restatements to back-pointers ("see logState").

Why: the split made the invariant emergent across four files; one owning
statement + references is drift insurance and makes applyRecord's
RLock-then-Lock read as deliberate.

Result: doc-only; gofmt + build + vet + `test -race .` green.

### Q7 — correct the projector-split test-count claim

What: the projector-split follow-up entry claimed a "42-func merged file" and
"Same 42 test funcs". The merged projector test file actually held 35 funcs
(34 Test + 1 Fuzz); 42 conflated it with the snapshot test file's pre-existing
7. Reworded to the accurate numbers and the conservation claim.

Why: the council flagged the miscount; the decision ledger should be accurate.

Result: doc-only (HYGIENE.md); no code touched.

---

Council do-now queue COMPLETE (#1–#7). Deferred items (N-goroutine -race
characterization test; prevSnapOffset read/write cross-ref) and rejected items
(marshalEvent error-discrimination; (0,nil)/ErrClosed re-doc) intentionally not
done.
