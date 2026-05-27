package via

import "github.com/go-via/via/h"

// StateSess is a session-scoped reactive value: shared across every tab
// opened from the same browser session, expires per via.WithSessionTTL.
//
//	type Profile struct {
//	    Theme via.StateSess[string]
//	}
//
// The handle holds only the wire key; storage lives in the session store
// owned by the via runtime, populated at Mount time.
type StateSess[T any] struct {
	wireKey string
}

func (s *StateSess[T]) bindWireKey(k string) { s.wireKey = k }

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
	if ctx == nil || ctx.session == nil {
		return zero
	}
	ctx.trackRead(s.wireKey)
	v, ok := ctx.session.data.Load(s.wireKey)
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
	if fn == nil || ctx.session == nil || ctx.app == nil {
		return nil
	}
	_, err := ctx.session.data.Update(s.wireKey, func(old any) (any, error) {
		t, _ := old.(T)
		return fn(t)
	})
	if err != nil {
		return err
	}
	ctx.markStateDirty()
	ctx.app.broadcastRender(ctx, ctx.session, s.wireKey)
	return nil
}

// Text returns a static text node carrying the current value. Accepts
// either *Ctx (action handlers) or *CtxR (View).
func (s *StateSess[T]) Text(rc readCtx) h.H { return h.Textf("%v", s.Read(rc)) }

// stateSessMarker tags StateSess[T] (and types that embed it). See
// signalMarker for the rationale.
type stateSessMarker interface{ isStateSess() }

func (*StateSess[T]) isStateSess() {}
