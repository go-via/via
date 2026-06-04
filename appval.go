package via

import (
	"context"
	"encoding/json"
	"sync"
)

// valCell is the per-pod L1 cache for one value-shaped StateApp key. The
// backplane Store cell valKey(key) is the source of truth; l1 holds the live
// decoded value so reads stay zero-serialization. Exactly two writers touch
// l1/l1Rev under mu: the local Update (sync, on its own pod) and the
// changes-feed tailer (for writes from peer pods). decode turns a Store
// snapshot's bytes back into the typed value (captured from the typed handle at
// bindApp, since the App itself is type-erased).
type valCell struct {
	mu     sync.RWMutex
	l1     any
	l1Rev  Rev
	decode func([]byte) (any, error)
}

// changesKey is the shared EventLog feed carrying value-less Change hints; every
// pod tails it and re-pulls the named Store cell to HEAD.
const changesKey = "via.changes"

// change is the value-LESS liveness hint appended after a value CAS. It carries
// only the key and the new revision — never the value — so a stale replica read
// can be detected (storeRev >= rev) and peers always re-pull the authoritative
// Store cell rather than trust the hint's payload.
type change struct {
	Key string `json:"k"`
	Rev Rev    `json:"r"`
}

// valKey namespaces a value cell in the shared Store, distinct from log keys.
func valKey(wireKey string) string { return "val:" + wireKey }

// registerValCell records the typed decode closure for key (idempotent across
// the many tabs that bind it — never resets a live l1) and starts the single
// per-App changes-feed tailer.
func (a *App) registerValCell(key string, decode func([]byte) (any, error)) {
	a.valStatesMu.Lock()
	if a.valStates[key] == nil {
		a.valStates[key] = &valCell{decode: decode}
	}
	a.valStatesMu.Unlock()

	a.valTailerOnce.Do(func() { a.startChangesTailer() })
}

func (a *App) valCellFor(key string) *valCell {
	a.valStatesMu.Lock()
	defer a.valStatesMu.Unlock()
	return a.valStates[key]
}

// valProjection returns the cached value for key, or ok=false if no cell is
// registered. Read hits this — never the backplane — so it stays O(1) and
// allocation-free.
func (a *App) valProjection(key string) (any, bool) {
	vc := a.valCellFor(key)
	if vc == nil {
		return nil, false
	}
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	if vc.l1 == nil {
		return nil, false
	}
	return vc.l1, true
}

// startChangesTailer tails the shared changes feed and reconciles each named
// Store cell to HEAD. The goroutine exits when the Subscribe channel closes
// (backplane Close on Shutdown).
func (a *App) startChangesTailer() {
	ch, err := a.backplane.Subscribe(context.Background(), changesKey, 0)
	if err != nil {
		return
	}
	go func() {
		for rec := range ch {
			var c change
			if json.Unmarshal(rec.Data, &c) != nil {
				continue
			}
			a.applyChange(c)
		}
	}()
}

// applyChange re-pulls the Store cell for c.Key to its current HEAD and updates
// L1 — gated so the feed is a pure liveness hint, never the value carrier:
//   - storeRev < c.Rev → a stale replica read; DROP and wait (T1-SRE-5), never
//     apply a value older than the hint promised.
//   - storeRev <= l1Rev → already applied (or newer); monotone gate makes
//     redelivered / out-of-order Changes non-regressing (T3-SRE-1).
func (a *App) applyChange(c change) {
	vc := a.valCellFor(c.Key)
	if vc == nil {
		return
	}
	vc.mu.Lock()
	if c.Rev > vc.l1Rev {
		data, storeRev, ok, _ := a.backplane.LoadSnapshot(context.Background(), valKey(c.Key))
		if ok && storeRev >= c.Rev && storeRev > vc.l1Rev {
			if v, err := vc.decode(data); err == nil {
				vc.l1 = v
				vc.l1Rev = storeRev
			}
		}
	}
	vc.mu.Unlock()
	a.broadcastRender(nil, nil, c.Key)
}
