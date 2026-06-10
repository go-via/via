package via

import "context"

// maybeCompact reclaims the log prefix a DURABLE snapshot now covers. Called
// only on a successful snapshot CAS (snapshot-FIRST, compact-SECOND). The floor
// LAGS one snapshot generation — Compact(before:prevSnapOffset) discards only
// what the PREVIOUS snapshot already covered, so the current snapshot's offset
// is never truncated (cold start always resumes) and ≥1 generation of tail
// events survives for any in-flight subscriber. A backend that declines
// Compactor runs snapshot-only. Runs on the single projector goroutine, so
// prevSnapOffset is unsynchronized beyond ls.mu (see logState).
func (a *App) maybeCompact(ls *logState, key string, covered Offset) {
	c, ok := a.backplane.(Compactor)
	if !ok {
		return // backend declines compaction → snapshot-only
	}
	ls.mu.RLock()
	floor := ls.prevSnapOffset
	diverged := ls.diverged
	ls.mu.RUnlock()
	if diverged {
		// WithFoldVerify proved this key's fold non-deterministic. NEVER compact —
		// dropping the prefix would crystallize the bad projection into durable
		// genesis with no path back. Keep the full log so a fixed (deterministic)
		// build can re-fold from scratch.
		return
	}
	// Never discard an event a registered side-effect consumer has not yet
	// processed: clamp the floor to the slowest consumer's committed offset.
	// A consumer at offset 0 pins it at genesis.
	if cmin, ok := a.minConsumerOffset(key); ok && cmin < floor {
		floor = cmin
	}
	_ = c.Compact(context.Background(), key, floor) // best-effort; a failure just defers reclamation
	ls.mu.Lock()
	ls.prevSnapOffset = covered
	ls.mu.Unlock()
}
