package via

import (
	"github.com/go-via/via/h"
)

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

// Get returns the current session value, or the zero value of T if unset.
// A Get that happens during View execution subscribes the ctx so a
// subsequent Update on the same key fans out to it. Accepts either
// *Ctx (action handlers) or *CtxR (View).
func (s *StateSess[T]) Get(rc readCtx) T {
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

// Update atomically applies fn to the current session value, stores
// the result, re-renders the current tab, and fans out a re-render
// to every other live tab on the same session subscribed to this key.
// The load → fn → store sequence runs under a per-key mutex so
// concurrent Update calls from different tabs on the same session
// cannot lose updates. Set is intentionally absent on session-scoped
// handles: a blind write across a user's open tabs is almost always
// a read-modify-write race in disguise — model the assignment as an
// Update whose fn ignores the old value if you truly mean it.
func (s *StateSess[T]) Update(ctx *Ctx, fn func(T) T) {
	if fn == nil || ctx == nil || ctx.session == nil || ctx.app == nil {
		return
	}
	ctx.session.data.Update(s.wireKey, func(old any) any {
		t, _ := old.(T)
		return fn(t)
	})
	ctx.markStateDirty()
	ctx.app.broadcastRender(ctx, ctx.session, s.wireKey)
}

// Text renders the current value as a static text node. Accepts either
// *Ctx or *CtxR.
func (s *StateSess[T]) Text(rc readCtx) h.H { return h.Textf("%v", s.Get(rc)) }
