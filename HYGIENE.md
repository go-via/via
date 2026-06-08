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
