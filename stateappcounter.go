package via

// counterTick is the unexported increment event behind StateAppCounter. Its
// fold adds one per event; keeping it unexported means callers cannot Append it
// directly, so Inc is the only way to advance the counter.
type counterTick struct{}

func (counterTick) Fold(acc int64, _ counterTick) int64 { return acc + 1 }

// StateAppCounter is the shipped specialization for the ubiquitous monotonic
// shared counter (precedent: StateAppNum / StateAppSlice). Where StateApp[int]
// becomes a per-key compare-and-swap that retry-storms under churn (N tabs
// clicking the same counter all CAS the one cell), StateAppCounter is an event
// log: each Inc appends an immutable tick that never conflicts, and the value
// is the fold (the count). Reach for it on a high-churn shared counter; keep
// StateApp[int] for low-churn current-value state.
//
//	type Dashboard struct {
//	    Hits via.StateAppCounter
//	}
//
// The surface is Inc + Read (and Text/Key, promoted): the increment event and
// its fold stay inside the library, so there is no event type to declare, no
// Fold to write, and no append offset to discard.
type StateAppCounter struct {
	StateAppEvents[counterTick, int64]
}

// Inc appends one increment. Like every StateAppEvents append it never
// conflicts and does not fold inline — the per-(pod,key) projector folds it and
// re-renders every subscribed tab. Panics on a nil ctx (the authorization gate,
// parity with StateApp.Update / StateAppEvents.Append).
func (c *StateAppCounter) Inc(ctx *Ctx) { _, _ = c.Append(ctx, counterTick{}) }
