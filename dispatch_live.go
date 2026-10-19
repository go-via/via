package via

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"
)

// dispatchOverStream runs act against a connected live unit on its
// connection's serialized goroutine and waits for the result — synchronous,
// unlike the old fire-and-forget child dispatch, so a Redirect, the session
// cookie, and a panic all resolve on this response like a plain action. The
// wait for the goroutine to pick the action up is bounded by req.Context(), the
// connection closing and the pinned deadline; an action already running is
// waited for, since it writes this response.
func (m *mount) dispatchOverStream(ar *actionReq, lc *tabStream, deadline time.Time, t *parkTicket) {
	w, req := ar.w, ar.req
	res, outcome := lc.run(req.Context(), deadline, t, &liveAction{ar: ar, m: m, lc: lc})
	switch outcome {
	case runPinned:
		// 503, not 410: the tab is fine and the action is legitimate — this
		// connection's goroutine is not answering. See warnPinned.
		http.Error(w, "stream busy", http.StatusServiceUnavailable)
		return
	case runClosed:
		noteErr(w, ErrStaleTab)
		http.Error(w, "stream closed", http.StatusGone)
		return
	case runAbandoned:
		http.Error(w, "request abandoned", http.StatusGone)
		return
	}
	if res.fail != nil {
		res.fail.answer(m.cfg.log, w)
		return
	}
	if m.answerRedirect(w, req, ar.mode, res.redirect) {
		return
	}
	if ar.mode == modeNative {
		// A native submit replaces the whole document, so this must be the
		// page a brand-new connection will hold — a fresh instance with OnInit
		// run, not a snapshot of the dying connection's live tree (which the
		// reconnect is about to reseed anyway). The request's cookie predates
		// the action's Put or Rotate, so OnInit reads the action's session.
		ctx, body := m.renderFreshPage(w, req, ar.base, res.session)
		m.writePage(w, req, ctx, body)
		return
	}
	writePatch(w, nil) // the push already framed it
}

// liveAction is one action POST handed to its tab's stream goroutine.
type liveAction struct {
	ar     *actionReq
	m      *mount
	lc     *tabStream
	result chan actionResult
}

// run applies the action, answers the waiting POST, then pushes.
func (a *liveAction) run() {
	res := a.applyRecovered()
	a.result <- res
	if res.pushWork != nil {
		res.pushWork.push()
	}
}

func (a *liveAction) applyRecovered() (res actionResult) {
	defer a.recoverPanic(&res)
	return a.apply()
}

func (a *liveAction) recoverPanic(res *actionResult) {
	if rec := recover(); rec != nil {
		a.lc.mount.cfg.log.Error("via: live action panic",
			"tab", a.lc.id, "unit", a.lc.unitType(), "err", rec, "stack", string(debug.Stack()))
		*res = actionResult{fail: failPanic(rec)}
	}
}

func (a *liveAction) apply() actionResult {
	// The client hung up between the handoff and now: applying the action
	// would double-apply on its retry, and nobody reads this answer.
	if a.ar.req.Context().Err() != nil {
		return actionResult{fail: failGone("request abandoned")}
	}
	// Checked here, not before the action was posted: a cookieless
	// dispatch that passed a pre-queue check while the connection was
	// unbound could be applied after a concurrent live login bound it. Here
	// the compare and the run are atomic on one serialized goroutine.
	// Resolved once, here, for both the binding check and the action. The
	// store error is kept, not discarded: a store that could not answer
	// yields a nil session, which used to read as "wrong session" (or "no
	// prior session") rather than an outage.
	sessID, sess, err := a.m.sessions.resolve(a.ar.req)
	if err != nil {
		// 503, not 403: the request is well-formed and the tab is live, so a
		// store blip must not read as a security rejection.
		return actionResult{fail: &actionFail{status: http.StatusServiceUnavailable, body: "session store unavailable", note: ErrStoreDown}}
	}
	if bound := a.lc.boundSession(); bound != "" {
		// A dispatch must carry the same session, compared by sid (a Rotate
		// moves the cookie id, never the sid). Otherwise a leaked tab id is a
		// bearer credential good from any request, session or none, once
		// the origin floor is open.
		if sess == nil || sess.sid != bound {
			return actionResult{fail: &actionFail{status: http.StatusForbidden, body: "session mismatch"}}
		}
	}
	// Also looked up here, not before the action was posted: a concurrent
	// push replaces lc.units, so a unit read earlier could be stale. That
	// is more than an address mismatch — Signal.bind stamps the dirty sink
	// at render time, so acting against an older unit attributes the write
	// to a Ctx nothing downstream reads, silently dropping the patch.
	u := a.lc.unit(a.ar.child)
	if u == nil || u.unitV.ident != a.ar.req.URL.Query().Get(unitIdentParam) {
		return actionResult{fail: failGone("no such child")}
	}
	act, ok := u.actions[a.ar.act]
	if !ok {
		return actionResult{fail: failGone(a.m.unknownAction(u, a.ar.act))}
	}
	return a.runOn(u, act, sessID, sess)
}

// runOn hydrates unit's signals from the posted body and runs act. unit is the bind
// Ctx the last push produced, whose actions/hydrators table is current because
// every push is a render — so nothing re-renders here first. It runs on the
// connection's serialized goroutine while the POST blocks in tabStream.run, so
// w is safe to write to exactly as on a plain action.
//
// The re-render + SSE push rides back in res.pushWork instead of running here:
// run sends the result to the waiting POST first, so a stalled peer's write
// delays only the next push item, never this response. A detached goroutine
// doing the enqueue used to race other actions' goroutines and reorder their
// pushes; returning it as data keeps everything on the one goroutine, in order.
func (a *liveAction) runOn(unit *Ctx, act action, sessID string, beforeSession *sessionData) (res actionResult) {
	lc, w, req := a.lc, a.ar.w, a.ar.req
	defer lc.recoverAction(unit, act, &res)
	// Registered before the hydrate loop so every exit unwinds it, not just the
	// one that reaches a push: a malformed or unrendered arg returns without
	// pushWork, and livePush's restore is the only other one — so without this
	// the client's posted values stayed on the instance for the next
	// Tick/Listen handler to read. Deferred rather than inline so the handler and reloadUnit still see
	// the hydrated values; a Set inside the handler drops its own slot, so a
	// server write still survives.
	defer lc.rev.restore()
	// beforeSession is resolved before the action runs, not inferred from the
	// handle afterwards: a request already carrying a cookie and one whose
	// action just minted look the same once the action is done.
	//
	// An unbound connection binds to the session the request carries: the user
	// may have logged in from another tab after this one connected, and the tab
	// id must stop being a bearer credential once a session reaches it. A
	// leaked id presented with someone else's cookie binds to that cookie
	// instead, which locks the rightful tab out (403, recovered by a reload) but
	// grants nothing: every action still runs under its own request's session.
	if beforeSession != nil {
		lc.bindSession(beforeSession.sid)
	}
	lc.hydrateUnit(unit, a.ar.in)
	// A fresh Ctx per dispatch, not unit itself: a Tick/Listen handler holds
	// unit for the life of the connection, so writing req/sessW/redirect onto
	// it would rewrite what that handler sees. Signal.Set and State.Set still
	// land on unit — those handles were bound to it at render time.
	rc := &Ctx{reqScope: reqScope{req: req, sessions: a.m.sessions, policy: a.m.policy, sessW: w}, streamCtx: unit.streamCtx}
	rc.adoptSession(sessID, beforeSession, nil)
	rc.session.stream = lc
	// unit.dirty is not reset here: clearDirty (via flushDirty, in the actual
	// push) is the only place that owns clearing it. Resetting unconditionally
	// on every dispatch dropped an earlier action's Set the moment it panicked
	// or reloadUnit errored — no push ran to flush it, and this line wiped it
	// before the next action's push ever saw it.
	act.run(rc)

	// Re-load the unit the same way the plain path does: the handler mutated
	// state this unit's OnInit had already read, and the push render below
	// would otherwise frame the pre-action data. Skipped behind a Redirect —
	// the tab is navigating away from this render. See reloader.
	if rc.redirect.url == "" {
		rl := &Ctx{reqScope: rc.reqScope, session: rc.Session(), base: unit.base, streamCtx: unit.streamCtx}
		if err := reloadUnit(unit.unitV.v, rl); err != nil {
			return actionResult{fail: &actionFail{hookErr: err}, pushWork: unit.pusher}
		}
		rc.redirect = rl.redirect
	}

	// A session this action minted: bind it now so the tab id stops being a
	// bearer credential the instant this action logs it in (bindSession is a
	// no-op once bound).
	if beforeSession == nil && rc.session.data != nil {
		lc.bindSession(rc.session.sid())
	}
	lc.shareSession(rc.session)

	// Behind a Redirect the tab is navigating away, so nothing is pushed. A
	// refused one navigates nowhere, so the push still runs.
	if rc.redirect.url != "" && rc.redirect.refusal() == "" {
		return actionResult{redirect: rc.redirect}
	}
	// The dirty set this action wrote is shipped by the push itself
	// (tabStream.flushDirty), on the one path every server-driven signal change
	// takes — a Tick/Listen handler's Set included.
	return actionResult{
		redirect: rc.redirect,
		pushWork: unit.pusher, // patch-signals, then re-render this unit and frame the element-patch
		session:  rc.session,
	}
}

// hydrateUnit applies the posted body to unit's client-writable slots.
func (c *tabStream) hydrateUnit(unit *Ctx, in map[string]json.RawMessage) {
	for slot, raw := range in {
		hydrate, ok := unit.hydrators[slot]
		if !ok {
			continue
		}
		if !hydrate.Hydrate(raw) {
			delete(c.client, slot)
			continue
		}
		// Remembered so the next push's display render still shows what the
		// client is holding: livePush reverts the instance to its
		// server-authored values before the authority render, and without
		// this the client's value would vanish from the frame that follows
		// its own action. It never reaches the authority render (I1).
		c.client[slot] = raw
	}
}

// recoverAction turns a panic in liveAction.runOn into its actionResult: via's
// sentinels get the answer the plain path gives them, anything else is logged
// as a handler fault.
func (c *tabStream) recoverAction(unit *Ctx, act action, res *actionResult) {
	if rec := recover(); rec != nil {
		if f := sentinelFail(c.mount.cfg.log, rec); f != nil {
			*res = actionResult{fail: f}
			// Param panics from inside the handler, which may already have
			// Set state; as after a handler panic, the push flushes it.
			if _, miss := rec.(paramMiss); miss {
				res.pushWork = unit.pusher
			}
			return
		}
		c.mount.cfg.log.Error("via: live action panic", "tab", c.id,
			"unit", fmt.Sprintf("%T", unit.unitV.v), "act", act.name, "err", rec, "stack", string(debug.Stack()))
		*res = actionResult{fail: failPanic(rec), pushWork: unit.pusher}
	}
}
