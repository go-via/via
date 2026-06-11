package via

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-via/via/h"
)

// StateApp is an app-scoped reactive value: shared across every session, every
// tab — and, with a clustered backplane, across every pod. Use sparingly (no
// tenant isolation).
//
//	type Profile struct {
//	    Hits via.StateApp[int]
//	}
//
// The handle holds only the wire key; the value lives in the backplane Store
// cell val:<key> (the single source of truth), cached per-pod in an L1 cell
// populated at Mount time. T must be JSON-serializable (the Store moves bytes).
type StateApp[T any] struct {
	wireKey string
	app     *App // bound at Mount; nil before
}

func (a *StateApp[T]) bindWireKey(k string) { a.wireKey = k }

// bindApp registers this key's L1 cell with a typed decode closure (Store bytes
// → T) and starts the App's changes-feed tailer. Being a method on the typed
// handle is how the type-erased App recovers T from a Store snapshot.
func (a *StateApp[T]) bindApp(app *App) {
	a.app = app
	app.registerValCell(a.wireKey, func(data []byte) (any, error) {
		var t T
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, err
		}
		return t, nil
	})
}

// Key returns the wire key (lowercase field name unless overridden by tag).
func (a *StateApp[T]) Key() string { return a.wireKey }

// Read returns the current app value, or the zero value of T if unset. A Read
// during View execution subscribes the ctx so a subsequent Update on the same
// key (from any pod) fans out to it. O(1): hits the per-pod L1 cache, never the
// backplane. Accepts either *Ctx (action handlers) or *CtxR (View).
func (a *StateApp[T]) Read(rc readCtx) T {
	var zero T
	if rc == nil {
		return zero
	}
	ctx := rc.rctx()
	if ctx == nil || ctx.app == nil {
		return zero
	}
	ctx.trackRead(a.wireKey)
	v, ok := ctx.app.valProjection(a.wireKey)
	if !ok {
		return zero
	}
	t, _ := v.(T)
	return t
}

// errCASExhausted wraps ErrCASConflict when Update gives up after too many
// optimistic retries — pathological contention, not a normal conflict.
var errCASExhausted = fmt.Errorf("via: StateApp.Update exhausted CAS retries: %w", ErrCASConflict)

const updateMaxRetries = 100

// Update atomically applies fn to the current app value. fn receives the
// current T and returns (new T, error). On non-nil error from fn the value is
// unchanged, no broadcast fires, and the error is returned. On success this
// tab re-renders and every other live tab — on this pod and, via the changes
// feed, on every other pod — subscribed to this key fans out a re-render.
//
// The backplane Store cell val:<key> is the source of truth: Update runs a
// compare-and-swap retry loop against it, so concurrent Updates from different
// ctxs (or pods) cannot lose increments — the loser observes ErrCASConflict and
// re-runs fn on the reloaded value. Write is intentionally absent: a blind write
// on shared state is almost always a read-modify-write race in disguise — model
// the assignment as an Update whose fn ignores the old value if you truly mean
// it.
//
// Panics on nil ctx: without one no broadcast can fan out, so silently
// succeeding would desync server state from every live tab.
func (a *StateApp[T]) Update(ctx *Ctx, fn func(T) (T, error)) error {
	if ctx == nil {
		panic("via: StateApp.Update called with nil *Ctx")
	}
	if fn == nil || ctx.app == nil {
		return nil
	}
	app := ctx.app
	bg := app.backplaneCtx

	for try := 0; try < updateMaxRetries; try++ {
		data, rev, ok, err := app.backplane.LoadSnapshot(bg, valKey(a.wireKey))
		if err != nil {
			return err
		}
		var cur T
		if ok {
			_ = json.Unmarshal(data, &cur)
		}
		next, err := fn(cur)
		if err != nil {
			return err // fn rejected: value unchanged
		}
		enc, err := json.Marshal(next)
		if err != nil {
			return err
		}
		newRev, err := app.backplane.CAS(bg, valKey(a.wireKey), rev, enc)
		if errors.Is(err, ErrCASConflict) {
			casSleep(bg, try) // jittered backoff so contenders don't spin in lockstep
			continue          // someone else wrote; reload and re-run fn
		}
		if err != nil {
			return err
		}
		// Success. Set the SHARED L1 cell synchronously so every session/tab on
		// THIS pod sees the new value immediately (single-pod byte-for-byte);
		// peers converge via the changes feed.
		if vc := app.valCellFor(a.wireKey); vc != nil {
			vc.mu.Lock()
			if newRev > vc.l1Rev {
				vc.l1 = next
				vc.l1Rev = newRev
			}
			vc.mu.Unlock()
		}
		// Append a value-less liveness hint so peers (and this pod's tailer)
		// re-pull — UNLESS the action is silent (sync off), which must suppress
		// all fan-out for this write. The value still persists in the Store; a
		// later loud write or the reconcile sweep propagates it. Best-effort:
		// correctness rests on the Store, not on this Append being delivered.
		if !ctx.silent.Load() {
			if hint, mErr := json.Marshal(change{Key: a.wireKey, Rev: newRev}); mErr == nil {
				_, _ = app.backplane.Append(bg, changesKey, hint)
			}
		}
		ctx.markStateDirty()
		app.broadcastRender(ctx, nil, a.wireKey)
		return nil
	}
	return errCASExhausted
}

// Text returns a static text node carrying the current value. Accepts either
// *Ctx (action handlers) or *CtxR (View).
func (a *StateApp[T]) Text(rc readCtx) h.H { return h.Textf("%v", a.Read(rc)) }

// stateAppMarker tags StateApp[T] (and types that embed it). See
// signalMarker for the rationale.
type stateAppMarker interface{ isStateApp() }

func (*StateApp[T]) isStateApp() {}
