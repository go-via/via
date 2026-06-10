package via

import (
	"encoding/json"
)

// coldStartFrom seeds ls from a durable snapshot (so the projector replays only
// the tail) and returns the offset to subscribe from. A missing / stale-codec /
// undecodable snapshot returns 0 (re-fold from genesis) — the snapshot is a
// disposable cache, never required for correctness. Returns the snapshot's
// covered offset only when a bridging seed (matching codec or seeded migration)
// was installed; otherwise a halt is latched on ls and folding stops.
func (a *App) coldStartFrom(ls *logState, key string) Offset {
	data, _, ok, _ := a.backplane.LoadSnapshot(a.backplaneCtx, snapKey(key))
	if !ok {
		return 0
	}
	var cp checkpoint
	if json.Unmarshal(data, &cp) != nil {
		return 0
	}
	// A snapshot folded BEFORE a crypto-shred erasure may hold shredded PII; if
	// the authoritative generation has advanced past it, it is invalid.
	erasureStale := cp.ErasureGen < a.loadErasureGen()
	switch {
	case erasureStale && cp.Compacted:
		// Durable-genesis snapshot invalidated by erasure: the event prefix is
		// gone, so re-folding from 0 would silently TRUNCATE to the surviving
		// tail — dropping SURVIVING subjects' data, not only the erased one. Halt
		// (roll-forward-only); compaction + erasure of one key needs snapshot
		// re-encryption (documented follow-up).
		a.haltErasure(ls, key)
		return 0
	case erasureStale:
		// Uncompacted + stale → re-fold from genesis; the erased subject's
		// now-undecryptable events drop on the way.
		return 0
	case cp.CodecHash == ls.codecHash:
		// Codec matches → seed straight from the snapshot.
		if v, err := ls.decodeSnap(cp.V); err == nil {
			a.seedFromSnapshot(ls, v, cp)
			return cp.CoveredOffset
		}
		return 0
	case !cp.Compacted:
		// Uncompacted mismatch → the snapshot is a pure disposable cache; re-fold
		// from genesis. Evolving V is free.
		return 0
	default:
		// Compacted + mismatch → durable genesis: the event prefix is gone, so we
		// MUST NOT discard (that would silently truncate to the surviving tail).
		// Run the registered seeded migration; on a missing or failing migration
		// HALT the projector (roll-forward-only), never truncate (T2-GO-4).
		if migrate, found := lookupSnapMigration(cp.CodecHash); found {
			if v, err := migrate.decode(cp.V); err == nil {
				a.seedFromSnapshot(ls, v, cp)
				return cp.CoveredOffset
			}
		}
		a.haltUnbridgeable(ls, key)
		return cp.CoveredOffset
	}
}

// seedFromSnapshot installs a cold-start seed (from a matching snapshot or a
// seeded migration) as the projection baseline, resuming the tail at the
// snapshot's covered offset.
func (a *App) seedFromSnapshot(ls *logState, v any, cp checkpoint) {
	ls.mu.Lock()
	ls.projection = v
	ls.cursor = cp.CoveredOffset
	ls.epoch = cp.Epoch
	ls.epochSeen = true
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
