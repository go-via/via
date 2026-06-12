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
	Sid string `json:"s,omitempty"` // session id for a StateSess change; "" = app-scoped
}

// valKey namespaces an app-scoped value cell in the shared Store.
func valKey(wireKey string) string { return "val:" + wireKey }

// sessValKey namespaces a session-scoped value cell by the FULL session id, so
// two sessions (or two pods) never alias each other's cells.
func sessValKey(sid, wireKey string) string { return "val:s:" + sid + ":" + wireKey }

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

// reconcileValues re-pulls every registered value key to the Store HEAD. Run
// periodically (WithReconcileInterval) so the changes feed is a pure latency
// optimization — a pod converges even when no Change hint reached it (joined
// after the write, a crash between CAS and the hint append, or a silent
// Update). Keys are snapshotted under the registry lock so the per-key I/O does
// not hold it.
func (a *App) reconcileValues() {
	a.valStatesMu.Lock()
	keys := make([]string, 0, len(a.valStates))
	for k := range a.valStates {
		keys = append(keys, k)
	}
	a.valStatesMu.Unlock()
	for _, k := range keys {
		a.reconcileKey(k)
	}
	a.reconcileSessions()
}

// reconcileSessions re-pulls every (live session × registered StateSess key) to
// the Store HEAD, so session state converges even when a hint was missed.
// O(sessions × sessKeys) per sweep — acceptable for v1; a future optimization
// could track only dirty (sid,key) pairs. Snapshots both registries first so no
// lock is held during the per-cell I/O.
func (a *App) reconcileSessions() {
	a.sessDecodersMu.Lock()
	if len(a.sessDecoders) == 0 {
		a.sessDecodersMu.Unlock()
		return
	}
	keys := make([]string, 0, len(a.sessDecoders))
	for k := range a.sessDecoders {
		keys = append(keys, k)
	}
	a.sessDecodersMu.Unlock()

	a.sessionsMu.RLock()
	sessions := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.sessionsMu.RUnlock()

	for _, s := range sessions {
		for _, k := range keys {
			a.reconcileSessionKey(s, k)
		}
	}
}

// reconcileSessionKey pulls one session's value cell to the Store HEAD under the
// same gates as applySessionChange (monotone, decode-safe), broadcasting only
// when the value advanced.
func (a *App) reconcileSessionKey(sess *session, key string) {
	a.sessDecodersMu.Lock()
	decode := a.sessDecoders[key]
	a.sessDecodersMu.Unlock()
	if decode == nil {
		return
	}
	data, storeRev, ok, err := a.backplane.LoadSnapshot(a.backplaneCtx, sessValKey(sess.id, key))
	if err != nil {
		a.logWarn(nil, "via: backplane LoadSnapshot failed reconciling session key %q: %v", key, err)
	}
	if !ok || storeRev <= sess.loadRev(key) {
		return
	}
	v, err := decode(data)
	if err != nil {
		return
	}
	if sess.advanceRev(key, storeRev) {
		sess.data.Store(key, v)
		a.broadcastRender(nil, sess, key)
	}
}

// reconcileKey pulls one value cell to the Store HEAD. The monotone gate
// (storeRev > l1Rev) makes it idempotent, and it broadcasts ONLY when L1
// actually advanced — so a steady-state sweep tick is a silent no-op, not a
// render storm.
func (a *App) reconcileKey(key string) {
	vc := a.valCellFor(key)
	if vc == nil {
		return
	}
	data, storeRev, ok, err := a.backplane.LoadSnapshot(a.backplaneCtx, valKey(key))
	if err != nil {
		a.logWarn(nil, "via: backplane LoadSnapshot failed reconciling key %q: %v", key, err)
	}
	vc.mu.Lock()
	changed := false
	if ok && storeRev > vc.l1Rev {
		if v, err := vc.decode(data); err == nil {
			vc.l1 = v
			vc.l1Rev = storeRev
			changed = true
		}
	}
	vc.mu.Unlock()
	if changed {
		a.broadcastRender(nil, nil, key)
	}
}

// startChangesTailer tails the shared changes feed and reconciles each named
// Store cell to HEAD. Runs on tailLoop: a reconnect resumes from the
// last-applied hint offset — the feed is stateful, so a gap could strand a
// peer until the reconcile sweep (or forever, with the sweep disabled).
func (a *App) startChangesTailer() {
	// cursor is read (resumeFrom) and written (onRecord) only on the tailLoop
	// goroutine — see the tailer contract — so it needs no lock.
	var cursor Offset
	a.startTailer(tailer{
		feed:       "changes",
		key:        changesKey,
		resumeFrom: func(context.Context) (Offset, error) { return cursor, nil },
		onRecord: func(rec Record) {
			cursor = rec.Offset
			var c change
			if json.Unmarshal(rec.Data, &c) != nil {
				return
			}
			if c.Sid == "" {
				a.applyChange(c) // app-scoped value
			} else {
				a.applySessionChange(c) // session-scoped value
			}
		},
	})
}

// applySessionChange reconciles a session-scoped value cell after a hint.
// SECURITY: a session Change names a sid; this pod acts ONLY on a session it
// actually holds. If the sid is unknown here it is DROPPED fail-closed — no
// Store read, no broadcast — so a session write can never leak into or wake an
// unrelated session. For a held session it mirrors applyChange's gates
// (stale-replica drop storeRev>=c.Rev + per-session monotone) but scoped to
// that one session via broadcastRender(nil, sess, key).
func (a *App) applySessionChange(c change) {
	a.sessionsMu.RLock()
	sess, ok := a.sessions[c.Sid]
	a.sessionsMu.RUnlock()
	if !ok {
		return // fail-closed: this pod does not hold that session
	}
	a.sessDecodersMu.Lock()
	decode := a.sessDecoders[c.Key]
	a.sessDecodersMu.Unlock()
	if decode == nil {
		return
	}
	data, storeRev, dok, err := a.backplane.LoadSnapshot(a.backplaneCtx, sessValKey(c.Sid, c.Key))
	if err != nil {
		a.logWarn(nil, "via: backplane LoadSnapshot failed applying session change for key %q: %v", c.Key, err)
	}
	if !dok || storeRev < c.Rev {
		return // stale replica: never surface a value older than the hint promised
	}
	v, err := decode(data)
	if err != nil {
		return // poison snapshot: keep the last good value (rev not consumed)
	}
	// advanceRev is the atomic monotone gate (the tailer and the reconcile sweep
	// can both reach the same session): store + broadcast ONLY if it advanced.
	if sess.advanceRev(c.Key, storeRev) {
		sess.data.Store(c.Key, v)
		a.broadcastRender(nil, sess, c.Key)
	}
}

// applyChange re-pulls the Store cell for c.Key to its current HEAD and updates
// L1 — gated so the feed is a pure liveness hint, never the value carrier:
//   - storeRev < c.Rev → a stale replica read; DROP and wait (T1-SRE-5), never
//     apply a value older than the hint promised.
//   - storeRev <= l1Rev → already applied (or newer); monotone gate makes
//     redelivered / out-of-order Changes non-regressing (T3-SRE-1).
func (a *App) applyChange(c change) {
	if a.applyChangeL1(c) {
		a.broadcastRender(nil, nil, c.Key)
	}
}

// applyChangeL1 performs the gated L1 re-pull and reports whether L1 actually
// advanced. The caller broadcasts only when it did — a redelivered or stale
// hint that changes nothing must be a silent no-op, not a render storm, the
// same contract reconcileKey and applySessionChange already honor.
func (a *App) applyChangeL1(c change) bool {
	vc := a.valCellFor(c.Key)
	if vc == nil {
		return false
	}
	vc.mu.Lock()
	defer vc.mu.Unlock()
	if c.Rev <= vc.l1Rev {
		return false
	}
	data, storeRev, ok, err := a.backplane.LoadSnapshot(a.backplaneCtx, valKey(c.Key))
	if err != nil {
		a.logWarn(nil, "via: backplane LoadSnapshot failed applying change for key %q: %v", c.Key, err)
	}
	if !ok || storeRev < c.Rev || storeRev <= vc.l1Rev {
		return false
	}
	v, err := vc.decode(data)
	if err != nil {
		return false
	}
	vc.l1 = v
	vc.l1Rev = storeRev
	return true
}
