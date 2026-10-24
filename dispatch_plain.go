package via

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"sync/atomic"
)

// dispatchPlain binds a fresh instance, runs OnInit, runs the acted-on unit's
// action, then answers per mode.
func (m *mount) dispatchPlain(ar *actionReq) {
	w, req := ar.w, ar.req
	d, ok := m.discoverPlain(ar)
	if !ok {
		return
	}
	ua, u, authAct, a, ok := m.actedUnit(ar, d)
	if !ok {
		return
	}
	// The intersection is per (handler, arg), not per handler: a posted signal
	// that widens an Each would otherwise widen the accepted arg set too, and
	// an arg only the client's own body produced would dispatch. a.args is the
	// very map action.run checks against, so pruning it in place is
	// what fails the unauthorized arg closed.
	pruneArgs(a, authAct)
	if ua.live {
		// Liveness is read off the auth render, never off bind (I1/I5): only
		// auth ran the root's OnInit. Reaching here means the tab was missing
		// or stale, so fail closed rather than mutating a throwaway instance.
		noteErr(w, ErrStaleTab)
		http.Error(w, noStream(ar.mode, ar.tab), http.StatusGone)
		return
	}
	u.reqScope = m.scope(w, req)
	a.run(u) // no long-lived handler holds this render's Ctx, so u is its own dispatch Ctx

	// The handler mutated state the acted unit's OnInit had already read, so
	// the response render below would frame the pre-action data — a 204 and a
	// silently unchanged UI. Skipped behind a Redirect: nothing from this
	// instance gets rendered. See reloader.
	if u.redirect.url == "" {
		rl := &Ctx{reqScope: u.reqScope, session: d.auth.session, base: ar.base}
		if err := reloadUnit(actedViewer(d.inst, u), rl); err != nil {
			answerHookError(m.cfg.log, w, err, "via: OnReload after an action failed")
			return
		}
		u.redirect = rl.redirect
	}

	if m.answerRedirect(w, req, ar.mode, u.redirect) {
		return
	}
	if ar.mode == modeNative {
		// u already ran OnInit for this request; from carries that wiring down
		// (inheritRequestScope) instead of running OnInit twice.
		ctx, body := renderRootBase(d.inst, true, rerender{base: ar.base, from: u, badDecodeLogged: u.badDecodeLogged})
		// auth ran the root's OnInit, so its verdict holds a Tick or Listen the
		// re-render cannot re-derive.
		ctx.live = ctx.live || d.auth.live
		m.writePage(w, req, ctx, body)
		return
	}
	m.answerPlain(w, ar, d, u, a.name)
}

// answerPlain writes a Datastar action's answer from the acted unit's re-render.
func (m *mount) answerPlain(w http.ResponseWriter, ar *actionReq, d *discovered, u *Ctx, name string) {
	markup, signals := m.rerenderPlain(ar, d, u)
	if markup == nil && len(signals) == 0 {
		m.warnNoChange(ar.act, name, actedViewer(d.inst, u))
	}
	writeActionAnswer(m.cfg.log, w, markup, signals)
}

// discovered is a plain action's discovery: the fresh instance, its auth and
// bind renders, and the root markup the response render is diffed against.
type discovered struct {
	inst       instance
	auth, bind *Ctx
	rootBefore []byte
}

// actedUnit finds the unit a plain action addresses in both discovery renders
// and its handler in each, answering 410 when either render disagrees about
// what lives at the address. ua and authAct are the auth render's; u and a are
// the bind render's, the pair the action runs against.
func (m *mount) actedUnit(ar *actionReq, d *discovered) (ua, u *Ctx, authAct, a action, ok bool) {
	w, child, act := ar.w, ar.child, ar.act
	ua = d.auth.unit(child)
	if ua == nil {
		http.Error(w, "no such child", http.StatusGone)
		return
	}
	authAct, authOK := ua.actions[act]
	if !authOK {
		http.Error(w, m.unknownAction(ua, act), http.StatusGone)
		return
	}
	u = d.bind.unit(child)
	if u == nil {
		http.Error(w, "no such child", http.StatusGone)
		return
	}
	// Same guard, same reason, as the acted-instance substitution in
	// childViewer: a When around a Child that depends on a hydrated signal can
	// shift ordinals, so one key may denote different types in the auth and
	// bind renders. Child's docs forbid such a When; fail closed rather than
	// run the handler against a unit the authorization never looked at.
	if ua.unitV.typ != u.unitV.typ {
		http.Error(w, "no such child", http.StatusGone)
		return
	}
	// A matching type is not yet the same unit: a sibling of the same type
	// shifted onto this key carries a different identity (see childViewer).
	if ident := ar.req.URL.Query().Get(unitIdentParam); ua.unitV.ident != ident || u.unitV.ident != ident {
		http.Error(w, "no such child", http.StatusGone)
		return
	}
	a, ok = u.actions[act]
	if !ok {
		http.Error(w, m.unknownAction(u, act), http.StatusGone)
	}
	return
}

// discoverPlain runs OnInit and the discovery renders for a plain action on a
// fresh instance.
func (m *mount) discoverPlain(ar *actionReq) (*discovered, bool) {
	inst := m.newInst()
	// Discovery is two-phase. auth is the render the client did not influence:
	// it alone decides what is dispatchable (see on.Bind). Then the body is
	// applied to the slots that render made client-writable and the tree is
	// re-rendered, so a Bind()ed signal opening a lazy branch gets the slots
	// inside it hydrated too — without this their posted values were silently
	// dropped. The action must be present in both, so the executed render is an
	// intersection with auth, never a superset.
	auth := newRootCtx(false, ar.base)
	// One flag for the whole discovery+hydrate+rebind tree, so a POST carrying
	// several malformed slots at once still logs one Warn, not one per slot per
	// pass (rebindFrom copies the pointer; childViewer carries it to children).
	auth.badDecodeLogged = new(atomic.Bool)
	auth.unitV = inst // so auth.unit(rootAddr)'s liveness reads the same way a child's does
	auth.passUnits = map[string]*Ctx{}
	prebindSignals(auth, inst)
	if runOnInit(inst.v, auth, m.scope(ar.w, ar.req), false) != nil {
		return nil, false
	}
	rootBefore := renderRootWith(auth, inst.v)
	bind := auth
	done := map[string]bool{}
	n := 0
	for ; n < maxHydratePasses && hydrateTree(bind, ar.in, done); n++ {
		bind = rebindFrom(auth)
		rootBefore = renderRootWith(bind, inst.v)
	}
	if n == maxHydratePasses {
		// Never expected: done grows monotonically, so a View would have to
		// bind a fresh slot on every pass. Loud, because the alternative is a
		// silently half-hydrated render.
		m.cfg.log.Warn("via: plain discovery hit its hydration-pass cap; some posted signals may be unapplied",
			"passes", maxHydratePasses, "act", ar.act)
	}
	if ar.child == rootAddr && n > 0 && len(auth.passUnits) > 0 {
		// The response render re-copies every child from its field, as it must
		// to show what the root action did to them, so the render it is
		// compared against has to as well: the last pass shows the children's
		// hydrated copies, and every root action would differ from it. bind is
		// rendered again afterwards so the handler's Sets land on the Ctx the
		// response reads.
		fresh := rebindFrom(auth)
		fresh.passUnits = nil
		rootBefore = renderRootWith(fresh, inst.v)
		bind = rebindFrom(auth)
		renderRootWith(bind, inst.v)
	}
	return &discovered{inst: inst, auth: auth, bind: bind, rootBefore: rootBefore}, true
}

// actedViewer is the composition the action just mutated: the root, or the
// child instance the discovery render bound. Re-loading the root after a child
// action would reload the wrong unit and leave the acted one stale.
func actedViewer(inst instance, u *Ctx) any {
	if u.isChild {
		return u.unitV.v
	}
	return inst.v
}

// rebindFrom builds the Ctx for hydration pass >= 2: a clone of the auth
// render with only the per-render tables reset. Three separate defect rounds
// were one field set on auth and forgotten here (req/sessions/sessW/doInit,
// then session, then live), each patched by hand-copying one more
// field — so the default is inverted: a new Ctx field rides along unless it
// belongs to unitPass, and forgetting one can no longer silently drop a request
// scope.
//
// unitPass holds exactly what a render produces and the next render must
// produce again, plus the root's OnInit registrations — OnInit does not re-run
// here and nothing on the plain path consumes them. `live` stays on Ctx: it is
// auth's verdict, and a later pass may only widen it (I5).
//
// The invariants this loop must preserve:
//
//	I1 the authority for actions, args and liveness is the un-hydrated auth render.
//	I2 passes >= 2 widen only hydratable slots; the executed action is auth ∩ bind
//	   per (handler, arg).
//	I3 one request = one *Session, resolved by runOnInit and propagated by copy;
//	   only Session.ensure mints.
//	I4 a Session.w is non-nil iff its response is still writable.
//	I5 liveness is a GET/auth verdict — a bind pass cannot lower it and a
//	   re-render cannot raise it.
func rebindFrom(auth *Ctx) *Ctx {
	c := *auth
	c.unitPass = newUnitPass()
	return &c
}

// hydrateTree applies the POST body's signals to every slot the discovery
// render bound, page-wide, after that render finished. It records each slot it
// reached in done and reports whether it reached a new one, which is
// dispatchPlain's signal that another render may uncover more.
//
// Deliberately not done during the render, which is what decides what is
// dispatchable (see on.Bind): hydrating from the body let a client flip a Signal
// OnInit set from the session — via.When(p.Admin.Get(), …) opened by posting
// {"admin":true} — and mint its own authorization. Later passes do see hydrated
// values, but only to widen the set of hydratable slots; the action table they
// may dispatch from stays auth's.
func hydrateTree(c *Ctx, in map[string]json.RawMessage, done map[string]bool) bool {
	fresh := false
	for slot, raw := range in {
		hydrate, ok := c.hydrators[slot]
		if !ok {
			continue
		}
		if !hydrate.Hydrate(raw) {
			// Gone, so a live connection does not re-decode it on every push.
			delete(in, slot)
			continue
		}
		if !done[slot] {
			done[slot] = true
			fresh = true
		}
	}
	for _, child := range c.children {
		if hydrateTree(child, in, done) {
			fresh = true
		}
	}
	return fresh
}

// maxHydratePasses bounds dispatchPlain's discovery loop. The loop cannot
// oscillate — done only grows, and is bounded by |in ∩ hydrators| — so the cap
// is a backstop against a pathological View that binds a brand-new slot name on
// every render, not against divergence. A page whose body carries any Bind()ed
// slot takes exactly 2 passes (2 renders), which is the floor: the handler has
// to run on a render that already saw the posted values.
//
// Each pass re-renders the whole tree, but a child keeps the instance
// its first pass made (see Ctx.passUnits), so its OnInit runs once, as a root's
// does.
const maxHydratePasses = 8

// rerenderPlain re-renders the acted-on unit for a Datastar action's response
// and collects the signals the answer patches (see actionSignals). markup is
// nil when the render is unchanged; a plain child's comes in its own container.
func (m *mount) rerenderPlain(ar *actionReq, d *discovered, u *Ctx) (markup []byte, signals map[string]any) {
	seen := d.bind.slotSet()
	if ar.child == rootAddr {
		afterCtx, after := renderRootBase(d.inst, false, rerender{base: ar.base, from: u, badDecodeLogged: u.badDecodeLogged})
		if len(liveUnits(d.bind)) == 0 {
			assertRenderInvariantLiveness(len(liveUnits(afterCtx)) > 0)
		}
		signals = actionSignals(afterCtx, seen, d.bind.dirtyAll())
		if bytes.Equal(d.rootBefore, after) {
			return nil, signals
		}
		return after, signals
	}
	// u is always plain here (assertRenderInvariantLiveness below would already
	// have fired otherwise), but the real page can still hold it beneath an
	// already-live root — liveAncestor reads that off the discovery tree, since
	// this re-render's own synthetic root has no ancestor Ctx to walk from.
	// auth, not bind (I1): liveness is the un-hydrated render's verdict.
	afterCtx, afterInner := renderChildBind(u.childKey, u.unitV, rerender{base: ar.base, from: u, badDecodeLogged: u.badDecodeLogged}, liveAncestor(d.auth, ar.child))
	assertRenderInvariantLiveness(afterCtx.live)
	signals = actionSignals(afterCtx, seen, u.dirtyAll())
	if bytes.Equal(u.rendered, afterInner) {
		return nil, signals
	}
	return []byte(`<div id="via-i` + u.childKey + `">` + string(afterInner) + `</div>`), signals
}

// actionSignals is what a plain action's answer patches into the client store:
// the slots the action wrote, and the slots its render opened that the
// pre-action render did not carry. The client store has no value for an opened
// slot, or a stale one from whatever occupied it before.
func actionSignals(after *Ctx, seen map[string]bool, dirty map[string]any) map[string]any {
	out := map[string]any{}
	openedSlots(after, seen, out)
	maps.Copy(out, dirty)
	return out
}

// pruneArgs drops the args a bound handler holds that the authority render's
// handler does not.
func pruneArgs(a, authAct action) {
	for k := range a.args {
		if _, ok := authAct.args[k]; !ok {
			delete(a.args, k)
		}
	}
}

// writeActionAnswer answers a plain Datastar action. Signals ride their own
// patch-signals frame ahead of the markup, as on a live stream: a morph skips a
// data-signals text the element already carries, so a Set back to the value
// the page was served with would never reach the client store. The SSE answer
// carries no CSP header: a frame is no document, so it would be inert.
func writeActionAnswer(log *slog.Logger, w http.ResponseWriter, markup []byte, signals map[string]any) {
	raw, n := encodeSlotMap(log, signals)
	if n == 0 {
		writePatch(w, markup)
		return
	}
	writeSSEHeaders(w)
	writeSignalsFrame(w, string(raw))
	if markup != nil {
		writePatchFrame(w, markup)
	}
}
