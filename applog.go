package via

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// logState is the per-(pod,key) projector state for one StateAppEvents key: the
// cached projection plus the type-erased fold captured from the typed handle at
// bindApp. The projection has exactly ONE writer — the projector goroutine.
type logState struct {
	once sync.Once
	mu   sync.RWMutex

	projection any    // current folded V (seeded with seed on create)
	cursor     Offset // highest offset folded so far (gates re-delivery)

	seed      any                              // Go zero of V
	foldBytes func(acc any, data []byte) (any, error) // decode one record's E + fold into acc
	halted    bool                             // forward-incompatible record seen → frozen, roll-forward-only

	epoch     Epoch // last-applied stream generation; a change means the offset space reset
	epochSeen bool  // false until the first record establishes the baseline epoch

	// Snapshot cache (P5a): the projection is periodically persisted so a cold
	// start replays only the tail. encodeSnap/decodeSnap bridge V↔bytes (captured
	// from the typed handle); codecHash invalidates a stale-codec snapshot.
	encodeSnap     func(any) ([]byte, error)
	decodeSnap     func([]byte) (any, error)
	codecHash      string
	snapRev        Rev // last snapshot cell revision this pod wrote/saw
	foldsSinceSnap int // folds applied since the last snapshot write
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
// subscribed tabs. It is the SOLE fold path (T1-SRE-2): Append never folds. The
// goroutine exits when the Subscribe channel closes (backplane Close on
// Shutdown).
func (a *App) startProjector(key string, ls *logState) {
	// Cold start: seed from a durable snapshot so we replay only the tail.
	// A missing / stale-codec / undecodable snapshot leaves from=0 (re-fold from
	// genesis) — the snapshot is a disposable cache, never required for
	// correctness.
	from := Offset(0)
	if data, rev, ok, _ := a.backplane.LoadSnapshot(context.Background(), snapKey(key)); ok {
		var cp checkpoint
		if json.Unmarshal(data, &cp) == nil && cp.CodecHash == ls.codecHash {
			if v, err := ls.decodeSnap(cp.V); err == nil {
				ls.mu.Lock()
				ls.projection = v
				ls.cursor = cp.CoveredOffset
				ls.epoch = cp.Epoch
				ls.epochSeen = true
				ls.snapRev = rev
				ls.mu.Unlock()
				from = cp.CoveredOffset
			}
		}
	}
	ch, err := a.backplane.Subscribe(context.Background(), key, from)
	if err != nil {
		return
	}
	go func() {
		// Keep ranging the channel even once halted, so the backplane's
		// Subscribe sender never blocks (no goroutine leak); just stop folding.
		for rec := range ch {
			if a.projectRecord(ls, key, rec) {
				// skip=nil: the projector holds no action ctx. sess=nil: app-wide.
				a.broadcastRender(nil, nil, key)
				a.maybeSnapshot(ls, key)
			}
		}
	}()
}

// projectRecord folds one delivered Record into the cached projection under the
// key's lock, returning whether the projection advanced (and a re-render is due).
// It is the single fold path — shared by the live projector loop. broadcastRender
// is the caller's job, OUTSIDE the lock.
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
	default:
		// Poison / undecodable: skip it (advance past so it is not retried
		// forever), never wedging the key for any pod.
		ls.cursor = rec.Offset
		a.metricsOrNoop().Counter("via.events.undecodable", "key", key)
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
