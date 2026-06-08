package via

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strconv"
)

// checkpoint is the durable fold snapshot for a StateAppEvents key: the
// projected value V at CoveredOffset, tagged with the V codec hash (a mismatch
// invalidates the cache → re-fold from genesis) and the stream Epoch. It rides
// the backplane Store under snapKey. For an UNCOMPACTED key it is a pure
// disposable cache; compaction (P5c) makes it durable genesis.
type checkpoint struct {
	Epoch         Epoch           `json:"e"`
	CoveredOffset Offset          `json:"o"`
	CodecHash     string          `json:"h"`
	V             json.RawMessage `json:"v"`
	// Compacted marks the key as durable genesis: its event prefix has been (or
	// is about to be) discarded, so a codec-hash mismatch must run a seeded
	// migration, never discard + re-fold (T2-GO-4). Set durably BEFORE the prefix
	// is dropped (snapshot-FIRST), so a cold start never sees a dropped prefix
	// behind a Compacted:false checkpoint.
	Compacted bool `json:"c"`
	// ErasureGen is the crypto-shred generation this snapshot was folded under. A
	// GDPR erasure (App.EraseDataSubject) bumps the global generation; a cold
	// start whose authoritative generation is HIGHER than the checkpoint's
	// ignores the snapshot and re-folds from the (now-undecryptable) log, so the
	// erased subject's PII is never seeded from a pre-erasure snapshot.
	ErasureGen uint64 `json:"g,omitempty"`
}

// snapKey namespaces a key's snapshot cell in the Store, distinct from val: and
// log keys.
func snapKey(wireKey string) string { return "snap:" + wireKey }

// maybeSnapshot writes a fold snapshot once snapshotInterval folds have
// accumulated since the last one — keeping the snapshot off the per-event hot
// path so the event log's no-CAS-per-append win is preserved.
func (a *App) maybeSnapshot(ls *logState, key string) {
	interval := a.cfg.snapshotInterval
	if interval <= 0 {
		return
	}
	ls.mu.Lock()
	due := ls.foldsSinceSnap >= interval
	if due {
		ls.foldsSinceSnap = 0
	}
	ls.mu.Unlock()
	if due {
		a.writeSnapshot(ls, key)
	}
}

// writeSnapshot persists the current projection as a checkpoint. The backplane
// CAS runs OUTSIDE ls.mu (no I/O under the projection lock). Best-effort: a
// concurrent peer's snapshot (ErrCASConflict) is fine — refresh our rev and
// skip; the snapshot is a cache, and the next interval will try again.
func (a *App) writeSnapshot(ls *logState, key string) {
	ls.mu.RLock()
	proj := ls.projection
	cp := checkpoint{Epoch: ls.epoch, CoveredOffset: ls.cursor, CodecHash: ls.codecHash}
	// Compacted ⟺ a prefix has been (or, this generation, is about to be)
	// discarded. maybeCompact below compacts at floor=prevSnapOffset, dropping
	// offsets < prevSnapOffset, which removes ≥1 record iff prevSnapOffset >= 2.
	// Setting it here (before that compaction runs) keeps the flag durable BEFORE
	// the drop — snapshot-FIRST.
	cp.Compacted = ls.prevSnapOffset >= 2
	encode := ls.encodeSnap
	ls.mu.RUnlock()
	// Stamp the crypto-shred generation so a post-erasure cold start ignores a
	// snapshot folded before the erasure (its V may hold now-shredded PII).
	cp.ErasureGen = a.loadErasureGen()
	if encode == nil {
		return
	}
	vbytes, err := encode(proj)
	if err != nil {
		return
	}
	cp.V = vbytes
	b, err := json.Marshal(cp)
	if err != nil {
		return
	}
	// MONOTONIC: never regress the shared snapshot's CoveredOffset. The cell is
	// shared across pods; a lagging pod must not overwrite a leader's
	// higher-covered snapshot with a lower one, or compaction (which trusts the
	// durable snapshot to cover the prefix it drops) and a peer's gap-reseed
	// would lose records. Read the current cell, skip if we don't advance it, and
	// CAS against the rev we just read.
	curData, curRev, ok, _ := a.backplane.LoadSnapshot(context.Background(), snapKey(key))
	if ok {
		var curCp checkpoint
		if json.Unmarshal(curData, &curCp) == nil && cp.CoveredOffset <= curCp.CoveredOffset {
			a.setSnapRev(ls, curRev)
			return // a peer's snapshot already covers at least this offset
		}
	}
	newRev, err := a.backplane.CAS(context.Background(), snapKey(key), curRev, b)
	if errors.Is(err, ErrCASConflict) {
		// A peer wrote concurrently; resync our rev and retry next interval.
		_, fresh, _, _ := a.backplane.LoadSnapshot(context.Background(), snapKey(key))
		a.setSnapRev(ls, fresh)
		return
	}
	if err == nil {
		a.setSnapRev(ls, newRev)
		a.maybeCompact(ls, key, cp.CoveredOffset)
	}
}

// maybeCompact reclaims the log prefix a DURABLE snapshot now covers. Called
// only on a successful snapshot CAS (snapshot-FIRST, compact-SECOND). The floor
// LAGS one snapshot generation — Compact(before:prevSnapOffset) discards only
// what the PREVIOUS snapshot already covered, so the current snapshot's offset
// is never truncated (cold start always resumes) and ≥1 generation of tail
// events survives for any in-flight subscriber. A backend that declines
// Compactor runs snapshot-only. Called serially from the single projector
// goroutine, so prevSnapOffset needs no extra synchronization beyond ls.mu.
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
	// processed: clamp the floor to the slowest consumer's committed offset
	// (council line 272 / T-DX-6). A consumer at offset 0 pins it at genesis.
	if cmin, ok := a.minConsumerOffset(key); ok && cmin < floor {
		floor = cmin
	}
	_ = c.Compact(context.Background(), key, floor) // best-effort; a failure just defers reclamation
	ls.mu.Lock()
	ls.prevSnapOffset = covered
	ls.mu.Unlock()
}

func (a *App) setSnapRev(ls *logState, rev Rev) {
	ls.mu.Lock()
	ls.snapRev = rev
	ls.mu.Unlock()
}

// emitFoldDigest publishes the cheap, unconditional fold-divergence canary
// (council T1-SRE-7): after every advancing fold a pod emits its applied offset
// and a digest of the resulting projection, both gauged by key. Two pods that
// have folded a key to the same offset MUST report the same digest; a persistent
// (key, offset)-matched digest MISMATCH across pods is fold non-determinism
// caught before it corrupts a durable snapshot. The digest reuses the snapshot
// codec (encodeSnap), so a key with no codec simply skips the canary — never
// panics. Best-effort: a transient encode error just skips this sample.
//
// Cost: this re-encodes the FULL projection on EVERY advancing fold (O(state)
// per event), unlike maybeSnapshot which amortizes its encode over an interval.
// "Cheap" is relative to the cross-pod corruption it catches, not to the fold
// itself — for a large projection on a hot key the per-event encode is real. It
// reuses the encode the projector already performs at snapshot time, so it adds
// no new serialization machinery, only frequency.
func (a *App) emitFoldDigest(ls *logState, key string) {
	ls.mu.RLock()
	proj := ls.projection
	off := ls.cursor
	encode := ls.encodeSnap
	ls.mu.RUnlock()
	if encode == nil {
		return
	}
	b, err := encode(proj)
	if err != nil {
		return
	}
	h := fnv.New32a()
	_, _ = h.Write(b)
	m := a.metricsOrNoop()
	m.Gauge("via.fold.offset", float64(off), "key", key)
	m.Gauge("via.fold.digest", float64(h.Sum32()), "key", key, "offset", strconv.FormatUint(uint64(off), 10))
}
