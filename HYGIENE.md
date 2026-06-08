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
to the new filename. Left design-council.md as a historical record.

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

NOTE: `stateappevents_projector_internal_test.go` is now ~1260 lines — large,
but it is one concern (the projector). Splitting the projector SOURCE into
sub-concern files (and its tests with it) is a possible future refactor; the
functions are currently intertwined (projectRecord folds + verifies + handles
epoch reset), so it was out of scope here.

Result: gofmt clean; `go vet ./...` + `go test -race ./...` green across via,
vianats, viashowcase, and all plugins.

