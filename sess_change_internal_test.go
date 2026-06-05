package via

import (
	"encoding/json"
	"testing"
)

// applySessionChange is the cross-pod mechanism for session state, and it is
// SECURITY-CRITICAL: a session Change names a sid, and a pod must only ever
// touch a session it actually holds. A Change for a sid this pod has never
// seen must be DROPPED fail-closed — never re-pulled, never broadcast — or a
// session write could leak into / wake an unrelated session.
func TestApplySessionChangeDropsUnknownSidFailClosed(t *testing.T) {
	t.Parallel()
	stub := &loadStub{data: mustJSON(7), rev: 3, ok: true}
	app := &App{
		backplane:    stub,
		sessions:     map[string]*session{}, // no session for "X"
		sessDecoders: map[string]func([]byte) (any, error){"k": intDecode},
	}
	// Must not panic, must not create a record, and — the real fail-closed
	// invariant — must NOT even re-pull the Store for a sid we don't hold.
	app.applySessionChange(change{Sid: "X", Key: "k", Rev: 3})

	if _, ok := app.sessions["X"]; ok {
		t.Fatal("a Change for an unknown sid must not create a session record")
	}
	if len(stub.loads) != 0 {
		t.Fatalf("fail-closed: an unknown sid must not trigger any Store read, got loads=%v", stub.loads)
	}
}

// For a session this pod DOES hold, a Change re-pulls the authoritative Store
// cell to HEAD (keyed by the full sid) and converges that session's value —
// gated monotone so a stale/out-of-order Change cannot regress it.
func TestApplySessionChangeConvergesKnownSidMonotonically(t *testing.T) {
	t.Parallel()
	stub := &loadStub{}
	sess := &session{id: "X"}
	app := &App{
		backplane:    stub,
		sessions:     map[string]*session{"X": sess},
		sessDecoders: map[string]func([]byte) (any, error){"k": intDecode},
	}

	// Store at rev 3 → converge this session's value.
	stub.data, stub.rev, stub.ok = mustJSON(7), 3, true
	app.applySessionChange(change{Sid: "X", Key: "k", Rev: 3})
	if v, ok := sess.data.Load("k"); !ok || v != 7 {
		t.Fatalf("known-sid Change must converge the session value; got %v ok=%v, want 7", v, ok)
	}
	// The Store cell must be keyed by the FULL sid (no truncation that could
	// alias another session's cell).
	if got := stub.loads[len(stub.loads)-1]; got != "val:s:X:k" {
		t.Fatalf("session Store key = %q, want the full-sid key %q", got, "val:s:X:k")
	}

	// Stale replica (T1-SRE-5): a hint promises rev 5 but the Store read lags at
	// rev 3 < 5 — must DROP, never surface a value older than the hint promised.
	stub.data, stub.rev, stub.ok = mustJSON(50), 3, true
	app.applySessionChange(change{Sid: "X", Key: "k", Rev: 5})
	if v, _ := sess.data.Load("k"); v != 7 {
		t.Fatalf("stale replica (storeRev 3 < hint rev 5) must not apply; got %v, want 7", v)
	}

	// Monotone (T3-SRE-1): an older redelivered Change must not regress.
	stub.data, stub.rev, stub.ok = mustJSON(99), 2, true
	app.applySessionChange(change{Sid: "X", Key: "k", Rev: 2})
	if v, _ := sess.data.Load("k"); v != 7 {
		t.Fatalf("a stale/older Change must not regress the session value; got %v, want 7", v)
	}

	// Poison snapshot (undecodable) at a higher rev must keep the last good value.
	stub.data, stub.rev, stub.ok = []byte("not-an-int"), 9, true
	app.applySessionChange(change{Sid: "X", Key: "k", Rev: 9})
	if v, _ := sess.data.Load("k"); v != 7 {
		t.Fatalf("undecodable snapshot must keep the last good session value; got %v, want 7", v)
	}
}

// The reconcile sweep is the session path's safety net (converges a session
// even when a hint was missed — silent write, cold pod). Like reconcileKey it
// must advance only forward and survive a poison snapshot, scoped per session.
func TestReconcileSessionKeyAdvancesOnlyForwardAndSurvivesPoison(t *testing.T) {
	t.Parallel()
	stub := &loadStub{}
	sess := &session{id: "X"}
	app := &App{
		backplane:    stub,
		sessions:     map[string]*session{"X": sess},
		sessDecoders: map[string]func([]byte) (any, error){"k": intDecode},
	}

	// Store ahead → converge.
	stub.data, stub.rev, stub.ok = mustJSON(7), 3, true
	app.reconcileSessionKey(sess, "k")
	if v, ok := sess.data.Load("k"); !ok || v != 7 {
		t.Fatalf("sweep must converge the session value; got %v ok=%v, want 7", v, ok)
	}

	// Not ahead → no-op, no regression.
	stub.data, stub.rev, stub.ok = mustJSON(999), 3, true
	app.reconcileSessionKey(sess, "k")
	if v, _ := sess.data.Load("k"); v != 7 {
		t.Fatalf("sweep at an unchanged rev must be a no-op; got %v, want 7", v)
	}

	// Poison snapshot at a higher rev → keep the last good value.
	stub.data, stub.rev, stub.ok = []byte("nope"), 9, true
	app.reconcileSessionKey(sess, "k")
	if v, _ := sess.data.Load("k"); v != 7 {
		t.Fatalf("sweep must survive a poison snapshot; got %v, want 7", v)
	}
}

func intDecode(b []byte) (any, error) {
	var i int
	if err := json.Unmarshal(b, &i); err != nil {
		return nil, err
	}
	return i, nil
}
