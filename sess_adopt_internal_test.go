package via

import (
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
	if !validSessionID(genSecureID()) {
		t.Fatal("a freshly generated id must be valid")
	}
	cases := map[string]string{
		"too short":   strings.Repeat("a", 63),
		"too long":    strings.Repeat("a", 65),
		"non-hex":     strings.Repeat("a", 63) + "g",
		"uppercase":   strings.Repeat("a", 63) + "A",
		"empty":       "",
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
