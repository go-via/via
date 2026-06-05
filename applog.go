package via

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"time"
)

// reconnectBackoff paces a projector's re-subscribe after a transient
// disconnect, so a backend that is briefly unavailable cannot spin the
// reconnect loop hot.
const reconnectBackoff = 10 * time.Millisecond

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

	diverged bool // WithFoldVerify saw a non-deterministic fold → never compact this key

	// Snapshot cache (P5a): the projection is periodically persisted so a cold
	// start replays only the tail. encodeSnap/decodeSnap bridge V↔bytes (captured
	// from the typed handle); codecHash invalidates a stale-codec snapshot.
	encodeSnap     func(any) ([]byte, error)
	decodeSnap     func([]byte) (any, error)
	codecHash      string
	snapRev        Rev    // last snapshot cell revision this pod wrote/saw
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
		if json.Unmarshal(data, &cp) == nil {
			// A snapshot folded BEFORE a crypto-shred erasure may hold shredded
			// PII; if the authoritative generation has advanced past it, it is
			// invalid.
			erasureStale := cp.ErasureGen < a.loadErasureGen()
			switch {
			case erasureStale && cp.Compacted:
				// Durable-genesis snapshot invalidated by erasure: the event prefix
				// is gone, so re-folding from 0 would silently TRUNCATE to the
				// surviving tail — dropping SURVIVING subjects' data, not only the
				// erased one. Halt (roll-forward-only); compaction + erasure of one
				// key needs snapshot re-encryption (documented follow-up).
				a.haltErasure(ls, key)
			case erasureStale:
				// Uncompacted + stale → re-fold from genesis (from stays 0); the
				// erased subject's now-undecryptable events drop on the way.
			case cp.CodecHash == ls.codecHash:
				// Codec matches → seed straight from the snapshot.
				if v, err := ls.decodeSnap(cp.V); err == nil {
					a.seedFromSnapshot(ls, v, cp, rev)
					from = cp.CoveredOffset
				}
			case !cp.Compacted:
				// Uncompacted mismatch → the snapshot is a pure disposable cache;
				// re-fold from genesis (from stays 0). Evolving V is free.
			default:
				// Compacted + mismatch → durable genesis: the event prefix is gone,
				// so we MUST NOT discard (that would silently truncate to the
				// surviving tail). Run the registered seeded migration; on a missing
				// or failing migration HALT the projector (roll-forward-only),
				// never truncate (T2-GO-4).
				from = cp.CoveredOffset
				if migrate, found := lookupSnapMigration(cp.CodecHash); found {
					if v, err := migrate.decode(cp.V); err == nil {
						a.seedFromSnapshot(ls, v, cp, rev)
					} else {
						a.haltUnbridgeable(ls, key)
					}
				} else {
					a.haltUnbridgeable(ls, key)
				}
			}
		}
	}
	go func() {
		// Reconnect loop: each Subscribe tails until its channel closes. A close
		// while the app is still running is a TRANSIENT disconnect (the backend
		// dropped the consumer, the stream survives) — re-subscribe from the
		// cursor and rehydrate. A close during Shutdown (backplaneDone) or an
		// ErrClosed is a graceful stop — exit. Otherwise one network blip would
		// strand the key forever.
		for {
			ch, err := a.backplane.Subscribe(context.Background(), key, from)
			if err != nil {
				return // ErrClosed (or unrecoverable) → stop
			}
			// Keep ranging the channel even once halted, so the backplane's
			// Subscribe sender never blocks (no goroutine leak); just stop folding.
			for rec := range ch {
				if a.applyRecord(ls, key, rec) {
					// skip=nil: the projector holds no action ctx. sess=nil: app-wide.
					a.broadcastRender(nil, nil, key)
					a.emitFoldDigest(ls, key)
					a.maybeSnapshot(ls, key)
				}
			}
			if a.shuttingDown() {
				return
			}
			ls.mu.RLock()
			from = ls.cursor // resume strictly after what we have folded
			ls.mu.RUnlock()
			time.Sleep(reconnectBackoff)
		}
	}()
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

// seedFromSnapshot installs a cold-start seed (from a matching snapshot or a
// seeded migration) as the projection baseline, resuming the tail at the
// snapshot's covered offset.
func (a *App) seedFromSnapshot(ls *logState, v any, cp checkpoint, rev Rev) {
	ls.mu.Lock()
	ls.projection = v
	ls.cursor = cp.CoveredOffset
	ls.epoch = cp.Epoch
	ls.epochSeen = true
	ls.snapRev = rev
	ls.mu.Unlock()
}

// haltUnbridgeable freezes a compacted key whose durable-genesis snapshot cannot
// be bridged to the current fold (no / failing migration). Roll-forward-only:
// the projector folds nothing further (projectRecord short-circuits on halted),
// so the value never silently truncates to the surviving tail.
func (a *App) haltUnbridgeable(ls *logState, key string) {
	ls.mu.Lock()
	ls.halted = true
	ls.mu.Unlock()
	a.metricsOrNoop().Counter("via.snapshot.unbridgeable", "key", key)
}

// haltErasure freezes a key whose COMPACTED snapshot was invalidated by a
// crypto-shred erasure: its event prefix is gone, so neither seeding the
// (PII-bearing, stale) snapshot nor re-folding from a truncated log is correct.
// Roll-forward-only until the operator re-encrypts the snapshot (the documented
// compaction+erasure follow-up), so surviving subjects' data is never silently
// truncated.
func (a *App) haltErasure(ls *logState, key string) {
	ls.mu.Lock()
	ls.halted = true
	ls.mu.Unlock()
	a.metricsOrNoop().Counter("via.snapshot.erasure_halt", "key", key)
}

// applyRecord handles one delivered record. It first guards against COMPACTION
// OVERTAKE: if this projector has folded up to cursor C but the next record's
// offset is beyond C+1, the log dropped records C+1..(offset-1) that this pod
// never folded (a faster peer compacted the shared log below this pod's
// position). Folding the post-gap record onto the stale projection would
// silently and permanently diverge this pod from its peers — the cross-pod
// truncation bug. Instead re-seed from the durable snapshot (which covers the
// gap) and let the fold continue; if no snapshot can recover the gap, HALT
// rather than diverge. Then delegate to projectRecord. Runs on the single
// projector goroutine, so the read-then-reseed is not racing another folder.
func (a *App) applyRecord(ls *logState, key string, rec Record) bool {
	ls.mu.RLock()
	cur, halted := ls.cursor, ls.halted
	ls.mu.RUnlock()
	// rec.Offset > cur+1 is a gap: records cur+1..rec.Offset-1 are missing. This
	// fires for a fresh projector (cur=0) whose FIRST delivered record has offset
	// > 1 too — the log's prefix was compacted before its subscriber first read,
	// so it must recover from the snapshot, not fold onto the seed. (A genuinely
	// fresh stream's first record is offset 1 == cur+1, no gap.)
	if !halted && rec.Offset > cur+1 {
		// A gap: compaction dropped records cur+1..rec.Offset-1 we never folded.
		// Recover from the snapshot, but ONLY if it bridges the whole gap
		// (CoveredOffset >= rec.Offset-1) and moves us forward (> cur). Otherwise
		// halt rather than fold onto a stale projection or seed backwards.
		if a.reseedFromSnapshot(ls, key, cur, rec.Offset-1) {
			a.metricsOrNoop().Counter("via.events.compaction_reseed", "key", key)
		} else {
			ls.mu.Lock()
			ls.halted = true
			ls.mu.Unlock()
			a.metricsOrNoop().Counter("via.events.compaction_gap_halt", "key", key)
			return false
		}
	}
	return a.projectRecord(ls, key, rec)
}

// reseedFromSnapshot reloads the durable snapshot for key and installs it as the
// projection baseline (projection=V, cursor=CoveredOffset), so a projector that
// fell behind a compacted prefix recovers the dropped records from the snapshot
// instead of skipping them. Returns false if there is no snapshot or its codec
// hash no longer matches (can't be bridged live — the caller halts). The
// backplane read is OUTSIDE ls.mu; seedFromSnapshot takes the lock.
func (a *App) reseedFromSnapshot(ls *logState, key string, cur, needCovered Offset) bool {
	data, rev, ok, _ := a.backplane.LoadSnapshot(context.Background(), snapKey(key))
	if !ok {
		return false
	}
	var cp checkpoint
	if json.Unmarshal(data, &cp) != nil || cp.CodecHash != ls.codecHash {
		return false
	}
	// Only recover if the snapshot BRIDGES the gap (covers up to the record
	// before rec) and moves us FORWARD — never seed behind our current position.
	if cp.CoveredOffset < needCovered || cp.CoveredOffset <= cur {
		return false
	}
	v, err := ls.decodeSnap(cp.V)
	if err != nil {
		return false
	}
	a.seedFromSnapshot(ls, v, cp, rev)
	return true
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
