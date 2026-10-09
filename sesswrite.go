package via

import (
	"context"
	"encoding/json"
	"maps"
	"time"
)

// save writes d back under id, merging rather than replacing: the blob the
// store holds right now is re-read and only this request's write set is
// overlaid onto it. Two requests on one session therefore each keep their own
// keys, where re-encoding a whole decoded copy silently dropped whichever
// finished first. What is left is a read-modify-write window of one store
// round-trip, and last-writer-wins on the same key — see [Session].
//
// mint says id is a brand-new home for d — a freshly created session, or the
// new id of a Rotate — and is the only case allowed to write where the store
// holds no live blob for it. Without that gate a handle still holding a
// pre-rotation id would re-create a valid session under it on its next write:
// the rotated-away session never sees the write, and the id Rotate exists to
// invalidate resolves again.
//
// sessionSaveRetries caps the CAS loop. Contention on one session id is a
// handful of tabs, not a thundering herd, so exhausting it means the store is
// pathological; the write is dropped rather than applied unconditionally over
// a blob up to that many revisions stale, which is the very lost update the
// CAS loop exists to prevent.
const sessionSaveRetries = 8

func (m *sessionManager) save(ctx context.Context, id string, d *sessionData, mint bool) bool {
	w, ok := m.pending(d)
	if !ok {
		return false
	}
	ctx, cancel := m.bounded(ctx)
	defer cancel()
	cas, _ := m.store.(VersionedSessionStore)
	// Without CAS saveOnce never asks for a retry, so the cap binds only CAS.
	for attempt := 0; cas == nil || attempt < sessionSaveRetries; attempt++ {
		saved, retry := m.saveOnce(ctx, id, d, w, mint, cas)
		if !retry {
			return saved
		}
	}
	m.logger().Error("via: session write gave up after CAS attempts — the store is under pathological "+
		"contention on one session; the write is dropped rather than clobbering the writers "+
		"that got through", "attempts", sessionSaveRetries)
	return false
}

// pendingWrite is a handle's state copied out from under its lock, so the
// store round-trips of a save run without holding it.
type pendingWrite struct {
	sid        string
	dirty      map[string]json.RawMessage
	own        map[string]json.RawMessage
	mintFailed bool
}

func (m *sessionManager) pending(d *sessionData) (pendingWrite, bool) {
	d.mu.Lock()
	if d.retired {
		first := !d.dropLogged
		d.dropLogged = true
		d.mu.Unlock()
		if first {
			// Every other drop in save() logs its own cause; this branch is the
			// short-circuit for a handle already known to be retired, and was
			// the one path that lost a user's Put in silence. Session.Put
			// returns nothing, so the log is the only place the drop surfaces.
			m.logger().Warn("via: session write dropped — this handle's session id was already retired " +
				"(rotated away, expired, or refused by the store), so the value is NOT persisted")
		}
		return pendingWrite{}, false
	}
	w := pendingWrite{sid: d.sid, dirty: maps.Clone(d.dirty), own: maps.Clone(d.vals), mintFailed: d.mintFailed}
	d.mu.Unlock()
	return w, true
}

// saveOnce runs one read-merge-write round. retry reports that another request
// wrote first under CAS, so the caller re-merges onto its blob.
func (m *sessionManager) saveOnce(ctx context.Context, id string, d *sessionData, w pendingWrite, mint bool, cas VersionedSessionStore) (saved, retry bool) {
	stored, ver, live, err := m.loadCurrent(ctx, id, w.sid, cas)
	if err != nil {
		return false, false
	}
	if !live && !mint {
		m.retire(d, w.mintFailed)
		return false, false
	}
	vals := w.mergeOnto(stored, live)
	exp := time.Now().Add(m.ttl)
	blob, err := json.Marshal(sessionBlob{SID: w.sid, Exp: exp.UnixNano(), Vals: vals})
	if err != nil {
		m.logger().Error("via: session encode failed", "err", err)
		return false, false
	}
	applied, err := m.write(ctx, id, blob, ver, cas)
	if err != nil {
		return false, false
	}
	if !applied {
		return false, true
	}
	d.mu.Lock()
	// The merged view is what this request should read back too: a Tick
	// handler holding a connect-time snapshot otherwise keeps serving
	// values another request has since replaced.
	d.vals, d.exp = vals, exp
	d.mu.Unlock()
	m.fanout(d, vals, exp)
	return true, false
}

// loadCurrent reads the blob under id and reports whether it is live: the
// session sid, not yet expired. ver is zero without CAS.
func (m *sessionManager) loadCurrent(ctx context.Context, id, sid string, cas VersionedSessionStore) (map[string]json.RawMessage, uint64, bool, error) {
	var (
		raw []byte
		ver uint64
		ok  bool
		err error
	)
	if cas != nil {
		raw, ver, ok, err = cas.LoadVersion(ctx, id)
	} else {
		raw, ok, err = m.store.Load(ctx, id)
	}
	if err != nil {
		// Writing d's copy over a store that could not be read is exactly
		// the clobber this merge exists to avoid.
		m.logger().Error("via: session store Load failed before save", "err", err)
		return nil, 0, false, err
	}
	var b sessionBlob
	// Deliberately no b.Vals != nil clause: get accepts a nil value map as
	// an empty session, and a store that normalises {} to null would
	// otherwise yield a session that reads fine and retires on first write.
	live := ok && json.Unmarshal(raw, &b) == nil && b.SID == sid &&
		(b.Exp <= 0 || time.Now().Before(time.Unix(0, b.Exp)))
	return b.Vals, ver, live, nil
}

func (m *sessionManager) retire(d *sessionData, mintFailed bool) {
	if mintFailed {
		m.logger().Error("via: session write dropped — the store rejected this session's first write, " +
			"so there is nothing under its id to merge into")
	} else {
		m.logger().Warn("via: session id retired (rotated away or expired); write dropped — " +
			"writing under it would revive an id that no longer names this session")
	}
	d.mu.Lock()
	d.retired = true
	d.mu.Unlock()
}

// mergeOnto overlays the write set onto the live stored values, or onto the
// handle's own copy when there is no live blob to merge into.
func (w pendingWrite) mergeOnto(stored map[string]json.RawMessage, live bool) map[string]json.RawMessage {
	vals := stored
	if !live || stored == nil {
		vals = make(map[string]json.RawMessage, len(w.own))
		maps.Copy(vals, w.own)
	}
	for k, v := range w.dirty {
		if v == nil {
			delete(vals, k)
			continue
		}
		vals[k] = v
	}
	return vals
}

// write stores blob under id; without CAS it always applies or fails.
func (m *sessionManager) write(ctx context.Context, id string, blob []byte, ver uint64, cas VersionedSessionStore) (bool, error) {
	if cas == nil {
		if err := m.store.Save(ctx, id, blob, m.ttl); err != nil {
			m.logger().Error("via: session store Save failed", "err", err)
			return false, err
		}
		return true, nil
	}
	applied, err := cas.SaveIf(ctx, id, blob, m.ttl, ver)
	if err != nil {
		m.logger().Error("via: session store SaveIf failed", "err", err)
		return false, err
	}
	return applied, nil
}
