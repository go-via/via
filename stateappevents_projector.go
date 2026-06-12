package via

import (
	"context"
	"sync"
)

// logState is the per-(pod,key) projector state for one StateAppEvents key: the
// cached projection plus the type-erased fold captured from the typed handle at
// bindApp.
//
// Concurrency contract (the projector's methods across this package — coldStartFrom,
// applyRecord, classifyGap, projectRecord, maybeSnapshot/writeSnapshot, maybeCompact,
// emitFoldDigest — all rely on this; they cite it rather than restate it):
//
//   - SINGLE WRITER. Exactly one goroutine ever mutates a logState: the per-key
//     projector started once in startProjector (registerLog's sync.Once). So a
//     read-then-act sequence on the projector goroutine (e.g. classifyGap's
//     read-then-reseed) is never racing another folder, and prevSnapOffset needs
//     no synchronization beyond ls.mu.
//   - ls.mu GUARDS THE FIELDS. Tabs read the projection concurrently via
//     logProjection (RLock), so every field write still takes ls.mu even though
//     the writer is single — the lock exists for the readers, not rival writers.
//   - I/O IS OUTSIDE THE LOCK. Backplane calls (LoadSnapshot, CAS, broadcastRender)
//     never run under ls.mu; the projector reads what it needs under the lock,
//     releases it, does the I/O, then re-takes the lock to apply the result.
type logState struct {
	once sync.Once
	mu   sync.RWMutex

	projection any    // current folded V (seeded with seed on create)
	cursor     Offset // highest offset folded so far (gates re-delivery)

	seed       any                                     // Go zero of V
	foldBytes  func(acc any, data []byte) (any, error) // decode one record's E + fold into acc
	halted     bool                                    // forward-incompatible record seen → frozen, roll-forward-only
	gapsBenign bool                                    // backend has non-contiguous per-key offsets → offset gaps are normal, not compaction

	epoch     Epoch // last-applied stream generation; a change means the offset space reset
	epochSeen bool  // false until the first record establishes the baseline epoch

	diverged bool // WithFoldVerify saw a non-deterministic fold → never compact this key

	// Snapshot cache (P5a): the projection is periodically persisted so a cold
	// start replays only the tail. encodeSnap/decodeSnap bridge V↔bytes (captured
	// from the typed handle); codecHash invalidates a stale-codec snapshot.
	encodeSnap     func(any) ([]byte, error)
	decodeSnap     func([]byte) (any, error)
	codecHash      string
	foldsSinceSnap int    // folds applied since the last snapshot write
	prevSnapOffset Offset // covered offset of the PREVIOUS snapshot — the compaction floor (lag one generation)
}

// registerLog records the typed seed + fold for key (idempotent across the many
// tabs that bind the same key) and starts the per-key projector exactly once.
func (a *App) registerLog(key string, seed any, fold func(any, []byte) (any, error), encodeSnap func(any) ([]byte, error), decodeSnap func([]byte) (any, error), codecHash string) {
	a.logsMu.Lock()
	ls := a.logs[key]
	if ls == nil {
		ls = &logState{
			projection: seed, seed: seed, foldBytes: fold,
			encodeSnap: encodeSnap, decodeSnap: decodeSnap, codecHash: codecHash,
		}
		a.logs[key] = ls
	}
	a.logsMu.Unlock()

	ls.once.Do(func() { a.startProjector(key, ls) })
}

// startProjector tails the backplane for key and folds every record into the
// cached projection in offset order, then fans a re-render out to this pod's
// subscribed tabs. It is the SOLE fold path (T1-SRE-2): Append never folds.
// Runs on tailLoop (the loop was extracted from here): a transient disconnect
// re-subscribes strictly after what was folded, so the stateful projection is
// gap-free across blips.
func (a *App) startProjector(key string, ls *logState) {
	from := a.coldStartFrom(ls, key)
	first := true
	a.startTailer(tailer{
		feed: "projector:" + key,
		key:  key,
		// The first connect resumes at the cold-start seed (the snapshot's
		// covered offset, which can sit past the cursor on a halted key);
		// every reconnect resumes at the fold cursor.
		resumeFrom: func(context.Context) (Offset, error) {
			if first {
				first = false
				return from, nil
			}
			ls.mu.RLock()
			defer ls.mu.RUnlock()
			return ls.cursor, nil
		},
		onRecord: func(rec Record) {
			if a.applyRecord(ls, key, rec) {
				// skip=nil: the projector holds no action ctx. sess=nil: app-wide.
				a.broadcastRender(nil, nil, key)
				a.emitFoldDigest(ls, key)
				a.maybeSnapshot(ls, key)
			}
		},
	})
}

// shuttingDown reports whether Shutdown has begun (backplaneDone closed), so a
// projector/consumer can tell a graceful stop from a transient disconnect.
func (a *App) shuttingDown() bool {
	select {
	case <-a.backplaneDone:
		return true
	default:
		return false
	}
}

// logProjection returns the current cached projection for key, or ok=false if
// no projector has been registered for it.
func (a *App) logProjection(key string) (any, bool) {
	a.logsMu.Lock()
	ls := a.logs[key]
	a.logsMu.Unlock()
	if ls == nil {
		return nil, false
	}
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.projection, true
}
