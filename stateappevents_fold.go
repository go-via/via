package via

import (
	"errors"
	"reflect"
)

// projectRecord folds one delivered Record into the cached projection under the
// key's lock, returning whether the projection advanced (and a re-render is due).
// It is the single fold path — shared by the live projector loop. broadcastRender
// is the caller's job (it is I/O, kept outside the lock — see logState).
func (a *App) projectRecord(ls *logState, key string, rec Record) (advanced bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.halted {
		return false
	}
	// Epoch / offset-space-reset detection (T1-SRE-3). The first record sets the
	// baseline; a later record on a DIFFERENT epoch means the stream was
	// recreated/trimmed/restored and its offsets restarted — a bare offset
	// high-water-mark would skip every new record, so re-snapshot from genesis.
	if !ls.epochSeen {
		ls.epoch = rec.Epoch
		ls.epochSeen = true
	} else if rec.Epoch != ls.epoch {
		ls.projection = ls.seed
		ls.cursor = 0
		ls.epoch = rec.Epoch
		a.metricsOrNoop().Counter("via.events.epoch_reset", "key", key)
	}
	if rec.Offset <= ls.cursor {
		return false
	}
	next, ferr := ls.foldBytes(ls.projection, rec.Data)
	switch {
	case ferr == nil:
		if a.cfg.foldVerify && !ls.diverged {
			// Re-fold the SAME record from the SAME accumulator: a deterministic
			// reducer must produce an identical result. A mismatch (or a verify
			// error) proves impurity — flag the key so it is never compacted, so
			// the bad projection can't be crystallized into durable genesis.
			if next2, verr := ls.foldBytes(ls.projection, rec.Data); verr != nil || !reflect.DeepEqual(next, next2) {
				ls.diverged = true
				a.metricsOrNoop().Counter("via.fold.divergence", "key", key)
			}
		}
		ls.projection = next
		ls.cursor = rec.Offset
		ls.foldsSinceSnap++
		return true
	case errors.Is(ferr, ErrForwardIncompatible):
		// A newer binary wrote this record. FREEZE this key — do NOT advance the
		// cursor, so a roll-forward redeploy resumes here.
		ls.halted = true
		a.metricsOrNoop().Counter("via.events.forward_incompatible", "key", key)
		return false
	case errors.Is(ferr, ErrErased):
		// The data subject was erased (crypto-shred): the payload is permanently
		// unreadable BY DESIGN. Skip + advance, exactly like a poison record, but
		// record it as an intentional erasure, not corruption.
		ls.cursor = rec.Offset
		a.metricsOrNoop().Counter("via.events.erased", "key", key)
		return false
	default:
		// Poison / undecodable: skip it (advance past so it is not retried
		// forever), never wedging the key for any pod.
		ls.cursor = rec.Offset
		a.metricsOrNoop().Counter("via.events.undecodable", "key", key)
		return false
	}
}
