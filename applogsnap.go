package via

import (
	"context"
	"encoding/json"
	"errors"
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
	encode := ls.encodeSnap
	rev := ls.snapRev
	ls.mu.RUnlock()
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
	newRev, err := a.backplane.CAS(context.Background(), snapKey(key), rev, b)
	if errors.Is(err, ErrCASConflict) {
		// A peer wrote a newer snapshot; resync our rev so the next write CASes
		// against it, and skip this one.
		_, fresh, _, _ := a.backplane.LoadSnapshot(context.Background(), snapKey(key))
		a.setSnapRev(ls, fresh)
		return
	}
	if err == nil {
		a.setSnapRev(ls, newRev)
	}
}

func (a *App) setSnapRev(ls *logState, rev Rev) {
	ls.mu.Lock()
	ls.snapRev = rev
	ls.mu.Unlock()
}
