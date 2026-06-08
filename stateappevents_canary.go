package via

import (
	"hash/fnv"
	"strconv"
)

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
