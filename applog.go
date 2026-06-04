package via

import (
	"context"
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
}

// registerLog records the typed seed + fold for key (idempotent across the many
// tabs that bind the same key) and starts the per-key projector exactly once.
func (a *App) registerLog(key string, seed any, fold func(any, []byte) (any, error)) {
	a.logsMu.Lock()
	ls := a.logs[key]
	if ls == nil {
		ls = &logState{projection: seed, seed: seed, foldBytes: fold}
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
	ch, err := a.backplane.Subscribe(context.Background(), key, 0)
	if err != nil {
		return
	}
	go func() {
		for rec := range ch {
			ls.mu.Lock()
			if rec.Offset > ls.cursor {
				if next, ferr := ls.foldBytes(ls.projection, rec.Data); ferr == nil {
					ls.projection = next
				}
				ls.cursor = rec.Offset
			}
			ls.mu.Unlock()
			// skip=nil: the projector holds no action ctx. sess=nil: app-wide.
			a.broadcastRender(nil, nil, key)
		}
	}()
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
