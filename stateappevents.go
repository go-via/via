package via

// StateAppEvents is an app-scoped, event-sourced reactive value: the value is
// the fold of an append-only event log shared across every session and tab.
// Unlike StateApp[T] (which CAS-writes a single value), the projected value is
// derived purely from the log via E's Fold.
//
//	type Feed struct {
//	    Posts via.StateAppEvents[PostEvent, []Post]
//	}
//
// The zero value is usable: declare the field, no init. With a nil backplane it
// degrades to today's single-pod in-process behavior, no API difference.
//
// The handle holds only the wire key (and, once Read/Update land, the bound
// app); the log itself lives in the backplane owned by the via runtime,
// populated at Mount time.
type StateAppEvents[E EventReducer[E, V], V any] struct {
	wireKey string
}

// EventReducer constrains E to be its own reducer: the fold lives on the event
// TYPE, as a single method. The fold SEED — the projected value of an EMPTY
// log — is the Go zero value of V; there is no Zero() method.
//
// Determinism rule (load-bearing): Fold MUST be a pure function of
// (acc, receiver) — no clock, no RNG, no I/O, no globals. Two pods replaying
// the same offset range must converge to the same V. A wall-clock or random
// input must be carried as a field on E, stamped at Append, never sampled
// inside Fold. Fold MUST return acc unchanged for an unknown event variant so a
// pod running old code that tails a new-variant event folds it as a no-op.
type EventReducer[E any, V any] interface {
	Fold(acc V, ev E) V
}

// bindWireKey writes the wire key into the handle at Mount time, via the same
// scopeBinder seam StateApp/StateSess use.
func (l *StateAppEvents[E, V]) bindWireKey(k string) { l.wireKey = k }

// Key returns the wire key (lowercase field name unless overridden by `via:` tag).
func (l *StateAppEvents[E, V]) Key() string { return l.wireKey }

// stateAppEventsMarker tags StateAppEvents[E, V] (and types that embed it) with
// its OWN marker — distinct from stateAppMarker — so the walker can later start
// the per-key projector only for log keys, not value keys.
type stateAppEventsMarker interface{ isStateAppEvents() }

func (*StateAppEvents[E, V]) isStateAppEvents() {}
