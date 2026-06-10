package via

import (
	"encoding/json"
	"errors"

	"github.com/go-via/via/h"
)

// StateSess is a session-scoped reactive value: shared across every tab
// opened from the same browser session, expires per via.WithSessionTTL.
//
//	type Profile struct {
//	    Theme via.StateSess[string]
//	}
//
// The handle holds only the wire key; the value lives in the backplane Store
// cell val:s:<sid>:<key> (the source of truth, so a session spans pods), cached
// per-pod in the session's data. T must be JSON-serializable (the Store moves
// bytes).
type StateSess[T any] struct {
	wireKey string
	app     *App // bound at Mount; nil before
}

func (s *StateSess[T]) bindWireKey(k string) { s.wireKey = k }

// bindApp registers this key's typed (Store bytes → T) decoder so the
// type-erased session changes-tailer / reconcile sweep can recover T, and
// ensures the shared changes-feed tailer is running. Makes StateSess an
// appBinder so bindScopeKeys wires it.
func (s *StateSess[T]) bindApp(app *App) {
	s.app = app
	app.sessDecodersMu.Lock()
	if app.sessDecoders[s.wireKey] == nil {
		app.sessDecoders[s.wireKey] = func(data []byte) (any, error) {
			var t T
			if err := json.Unmarshal(data, &t); err != nil {
				return nil, err
			}
			return t, nil
		}
	}
	app.sessDecodersMu.Unlock()
	app.valTailerOnce.Do(func() { app.startChangesTailer() })
}

// Key returns the wire key (lowercase field name unless overridden by tag).
func (s *StateSess[T]) Key() string { return s.wireKey }

// Read returns the current session value, or the zero value of T if
// unset. A Read that happens during View execution subscribes the ctx
// so a subsequent Update on the same key fans out to it. Accepts
// either *Ctx (action handlers) or *CtxR (View).
func (s *StateSess[T]) Read(rc readCtx) T {
	var zero T
	if rc == nil {
		return zero
	}
	ctx := rc.rctx()
	if ctx == nil {
		return zero
	}
	sess := ctx.session.Load()
	if sess == nil {
		return zero
	}
	ctx.trackRead(s.wireKey)
	v, ok := sess.data.Load(s.wireKey)
	if !ok {
		return zero
	}
	t, _ := v.(T)
	return t
}

// Update atomically applies fn to the current session value. fn
// receives the current T and returns (new T, error). On non-nil error
// the store is unchanged, no broadcast fires, and the error is
// returned. On success the current tab re-renders and every other
// live tab on the same session subscribed to this key fans out a
// re-render. The load → fn → store sequence runs under a per-key
// mutex so concurrent Update calls from different tabs on the same
// session cannot lose updates. Write is intentionally absent on
// session-scoped handles: a blind write across a user's open tabs is
// almost always a read-modify-write race in disguise — model the
// assignment as an Update whose fn ignores the old value if you truly
// mean it.
//
// Panics on nil ctx: without one no broadcast can fan out, so silently
// succeeding would desync server state from every live tab.
func (s *StateSess[T]) Update(ctx *Ctx, fn func(T) (T, error)) error {
	if ctx == nil {
		panic("via: StateSess.Update called with nil *Ctx")
	}
	sess := ctx.session.Load()
	if fn == nil || sess == nil || ctx.app == nil {
		return nil
	}
	app := ctx.app
	bg := app.backplaneCtx
	cellKey := sessValKey(sess.id, s.wireKey)

	for try := 0; try < updateMaxRetries; try++ {
		data, rev, ok, err := app.backplane.LoadSnapshot(bg, cellKey)
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
		newRev, err := app.backplane.CAS(bg, cellKey, rev, enc)
		if errors.Is(err, ErrCASConflict) {
			casSleep(try) // jittered backoff so contenders don't spin in lockstep
			continue
		}
		if err != nil {
			return err
		}
		// Success: set this session's L1 synchronously (sync RYW for every tab
		// on this session, this pod) and record the rev for the monotone gate.
		sess.data.Store(s.wireKey, next)
		sess.advanceRev(s.wireKey, newRev)
		// Liveness hint carrying the FULL sid — suppressed for a silent action.
		if !ctx.silent.Load() {
			if hint, mErr := json.Marshal(change{Sid: sess.id, Key: s.wireKey, Rev: newRev}); mErr == nil {
				_, _ = app.backplane.Append(bg, changesKey, hint)
			}
		}
		ctx.markStateDirty()
		app.broadcastRender(ctx, sess, s.wireKey)
		return nil
	}
	return errCASExhausted
}

// Text returns a static text node carrying the current value. Accepts
// either *Ctx (action handlers) or *CtxR (View).
func (s *StateSess[T]) Text(rc readCtx) h.H { return h.Textf("%v", s.Read(rc)) }

// stateSessMarker tags StateSess[T] (and types that embed it). See
// signalMarker for the rationale.
type stateSessMarker interface{ isStateSess() }

func (*StateSess[T]) isStateSess() {}
