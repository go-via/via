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

