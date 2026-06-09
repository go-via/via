package via

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Only the exact genSecureID format (64 lowercase hex chars) may be adopted, so
// a client cannot make a pod adopt an arbitrary attacker-chosen token or a
// malformed value that would land odd keys in the session map / Store.
func TestValidSessionIDAcceptsOnlyTheGenSecureIDFormat(t *testing.T) {
	t.Parallel()
	if !validSessionID(genSecureID()) {
		t.Fatal("a freshly generated id must be valid")
	}
	cases := map[string]string{
		"too short": strings.Repeat("a", 63),
		"too long":  strings.Repeat("a", 65),
		"non-hex":   strings.Repeat("a", 63) + "g",
		"uppercase": strings.Repeat("a", 63) + "A",
		"empty":     "",
	}
	for name, s := range cases {
		if validSessionID(s) {
			t.Errorf("%s (%q) must be rejected", name, s)
		}
	}
}

func cookieReq(sid string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
	return r
}

// A client's session must be servable by a pod that never created it — that is
// the no-sticky-sessions guarantee the cross-pod value path rests on. So a
// presented, well-formed sid this pod has never seen must be ADOPTED (the
// returned session keeps that exact id), not replaced with a fresh one.
func TestUnknownButWellFormedSidIsAdopted(t *testing.T) {
	t.Parallel()
	var s *httptest.Server
	a := New(WithTestServer(&s))
	defer s.Close()

	sid := genSecureID() // valid, but this pod has never issued it
	sess := a.getOrCreateSession(httptest.NewRecorder(), cookieReq(sid))

	if sess.id != sid {
		t.Fatalf("adopted session id = %q, want the presented %q", sess.id, sid)
	}
	a.sessionsMu.RLock()
	_, ok := a.sessions[sid]
	a.sessionsMu.RUnlock()
	if !ok {
		t.Fatal("adopted session must be registered in a.sessions")
	}
}

// Adoption must only touch the UNKNOWN-sid branch: a second request for an
// already-registered sid must return the SAME session object (with its data
// intact), never replace it — otherwise every request would wipe the session.
func TestKnownSidReturnsTheSameSessionUnchanged(t *testing.T) {
	t.Parallel()
	var s *httptest.Server
	a := New(WithTestServer(&s))
	defer s.Close()

	sid := genSecureID()
	first := a.getOrCreateSession(httptest.NewRecorder(), cookieReq(sid))
	first.data.Store("marker", 42) // write into the session

	second := a.getOrCreateSession(httptest.NewRecorder(), cookieReq(sid))
	if second != first {
		t.Fatal("a known sid must return the SAME session object, not a replacement")
	}
	if v, ok := second.data.Load("marker"); !ok || v != 42 {
		t.Fatalf("re-requesting a known sid must not clobber its data; marker=%v ok=%v", v, ok)
	}
}

// adoptSession's re-check guard must be idempotent: a second adoption of an
// already-registered sid returns the SAME object (the LoadOrStore path a racer
// hits), never a fresh replacement that would split the session's state.
func TestAdoptSessionIsIdempotentForTheSameSid(t *testing.T) {
	t.Parallel()
	var s *httptest.Server
	a := New(WithTestServer(&s))
	defer s.Close()

	sid := genSecureID()
	first := a.adoptSession(sid)  // create path
	second := a.adoptSession(sid) // re-check / already-present path
	if first != second {
		t.Fatal("adopting an already-registered sid must return the same *session")
	}
}

// A malformed cookie value must NEVER be adopted — the pod mints a fresh,
// well-formed session instead, so garbage can't become a session key.
func TestMalformedSidIsNotAdopted(t *testing.T) {
	t.Parallel()
	var s *httptest.Server
	a := New(WithTestServer(&s))
	defer s.Close()

	sess := a.getOrCreateSession(httptest.NewRecorder(), cookieReq("not-a-valid-sid"))
	if sess.id == "not-a-valid-sid" {
		t.Fatal("a malformed sid must not be adopted")
	}
	if !validSessionID(sess.id) {
		t.Fatalf("the minted replacement must be a well-formed sid, got %q", sess.id)
	}
}

// Two requests racing to adopt the same sid must converge on ONE session
// record — never double-register or hand back divergent session objects (which
// would split a user's state).
func TestConcurrentAdoptionOfSameSidYieldsOneSession(t *testing.T) {
	t.Parallel()
	var s *httptest.Server
	a := New(WithTestServer(&s))
	defer s.Close()

	sid := genSecureID()
	const n = 16
	var wg sync.WaitGroup
	got := make([]*session, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = a.getOrCreateSession(httptest.NewRecorder(), cookieReq(sid))
		}(i)
	}
	wg.Wait()

	// All racers must converge on the SAME *session object — a double-register
	// (last-write-wins) would split a user's state across divergent objects.
	for i, sess := range got {
		if sess.id != sid {
			t.Fatalf("goroutine %d got id %q, want %q", i, sess.id, sid)
		}
		if sess != got[0] {
			t.Fatalf("goroutine %d got a different *session than goroutine 0 — double-register", i)
		}
	}
	a.sessionsMu.RLock()
	defer a.sessionsMu.RUnlock()
	if a.sessions[sid] != got[0] {
		t.Fatal("a.sessions must hold the one adopted session object")
	}
}

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
