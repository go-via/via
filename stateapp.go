package via

import "github.com/go-via/via/h"

// StateApp is an app-scoped reactive value: shared across every session,
// every tab. Use sparingly (no tenant isolation).
//
//	type Profile struct {
//	    Hits via.StateApp[int]
//	}
//
// The handle holds only the wire key; storage lives in the app store
// owned by the via runtime, populated at Mount time.
type StateApp[T any] struct {
	wireKey string
}

func (a *StateApp[T]) bindWireKey(k string) { a.wireKey = k }

// Key returns the wire key (lowercase field name unless overridden by tag).
func (a *StateApp[T]) Key() string { return a.wireKey }

// Get returns the current app value, or the zero value of T if unset.
// A Get that happens during View execution subscribes the ctx so a
// subsequent Update on the same key fans out to it. Accepts either
// *Ctx (action handlers) or *CtxR (View).
func (a *StateApp[T]) Get(rc readCtx) T {
	var zero T
	if rc == nil {
		return zero
	}
	ctx := rc.rctx()
	if ctx == nil || ctx.app == nil {
		return zero
	}
	ctx.trackRead(a.wireKey)
	v, ok := ctx.app.appStore.Load(a.wireKey)
	if !ok {
		return zero
	}
	t, _ := v.(T)
	return t
}

// Update atomically applies fn to the current app value, stores the
// result, re-renders the current tab, and fans out a re-render to
// every other live tab subscribed to this key. The load → fn → store
// sequence runs under a per-key mutex so concurrent Update calls from
// different ctxs cannot lose increments. Set is intentionally absent
// on app-scoped handles: a blind write on shared state is almost
// always a read-modify-write race in disguise — model the assignment
// as an Update whose fn ignores the old value if you truly mean it.
func (a *StateApp[T]) Update(ctx *Ctx, fn func(T) T) {
	if fn == nil || ctx == nil || ctx.app == nil {
		return
	}
	ctx.app.appStore.Update(a.wireKey, func(old any) any {
		t, _ := old.(T)
		return fn(t)
	})
	ctx.markStateDirty()
	ctx.app.broadcastRender(ctx, nil, a.wireKey)
}

// Text returns a static text node carrying the current value. Accepts
// either *Ctx (action handlers) or *CtxR (View).
func (a *StateApp[T]) Text(rc readCtx) h.H { return h.Textf("%v", a.Get(rc)) }
