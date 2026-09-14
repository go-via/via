package via

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type auditA struct{ N int }
type auditB struct{ N int }

func auditReq(m *sessionManager, id string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: m.cookie, Value: id + "." + m.sign(id)})
	return r
}

func auditResolve(t *testing.T, m *sessionManager, id string) *Session {
	t.Helper()
	gotID, d, err := m.resolve(auditReq(m, id))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if d == nil {
		t.Fatalf("session %s did not resolve", id)
	}
	return &Session{mgr: m, id: gotID, data: d}
}

// A write from one in-flight request must not erase a write of a different type
// made by another request on the same session.
func TestSession_concurrentRequestsDoNotLoseWrites(t *testing.T) {
	m := newSessionManager(&config{})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	id := s0.id

	s1, s2 := auditResolve(t, m, id), auditResolve(t, m, id)
	s1.Put(auditA{2})
	s2.Put(auditB{9})

	s3 := auditResolve(t, m, id)
	if a, ok := s3.Get[auditA](); !ok || a.N != 2 {
		t.Errorf("lost update: Put[auditA]{2} was overwritten, got %+v ok=%v", a, ok)
	}
	if b, ok := s3.Get[auditB](); !ok || b.N != 9 {
		t.Errorf("lost update: Put[auditB]{9} was overwritten, got %+v ok=%v", b, ok)
	}
}

// A Clear has to survive the merge as a tombstone, not merely be absent from
// the writing request's own copy.
func TestSession_clearSurvivesMerge(t *testing.T) {
	m := newSessionManager(&config{})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	s0.Put(auditB{2})
	id := s0.id

	s1, s2 := auditResolve(t, m, id), auditResolve(t, m, id)
	s1.Delete[auditA]()
	s2.Put(auditB{3})

	s3 := auditResolve(t, m, id)
	if _, ok := s3.Get[auditA](); ok {
		t.Error("Delete[auditA] was undone by the other request's save")
	}
	if b, ok := s3.Get[auditB](); !ok || b.N != 3 {
		t.Errorf("Put[auditB]{3} lost, got %+v ok=%v", b, ok)
	}
}

type concKey1 struct{ N int }
type concKey2 struct{ N int }
type concKey3 struct{ N int }
type concKey4 struct{ N int }
type concKey5 struct{ N int }
type concKey6 struct{ N int }
type concKey7 struct{ N int }
type concKey8 struct{ N int }

// N goroutines, each a separate "request" writing its own distinct type to one
// session id: every key must be readable afterwards.
func TestSession_concurrentDistinctKeysAllSurvive(t *testing.T) {
	m := newSessionManager(&config{})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	id := s0.id

	writers := []func(*Session){
		func(s *Session) { s.Put(concKey1{1}) },
		func(s *Session) { s.Put(concKey2{2}) },
		func(s *Session) { s.Put(concKey3{3}) },
		func(s *Session) { s.Put(concKey4{4}) },
		func(s *Session) { s.Put(concKey5{5}) },
		func(s *Session) { s.Put(concKey6{6}) },
		func(s *Session) { s.Put(concKey7{7}) },
		func(s *Session) { s.Put(concKey8{8}) },
	}
	// Each writer resolves its own copy first, exactly as a request would.
	handles := make([]*Session, len(writers))
	for i := range writers {
		handles[i] = auditResolve(t, m, id)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, fn := range writers {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; fn(handles[i]) }()
	}
	close(start)
	wg.Wait()

	final := auditResolve(t, m, id)
	if _, ok := final.Get[concKey1](); !ok {
		t.Error("concKey1 lost")
	}
	if _, ok := final.Get[concKey2](); !ok {
		t.Error("concKey2 lost")
	}
	if _, ok := final.Get[concKey3](); !ok {
		t.Error("concKey3 lost")
	}
	if _, ok := final.Get[concKey4](); !ok {
		t.Error("concKey4 lost")
	}
	if _, ok := final.Get[concKey5](); !ok {
		t.Error("concKey5 lost")
	}
	if _, ok := final.Get[concKey6](); !ok {
		t.Error("concKey6 lost")
	}
	if _, ok := final.Get[concKey7](); !ok {
		t.Error("concKey7 lost")
	}
	if _, ok := final.Get[concKey8](); !ok {
		t.Error("concKey8 lost")
	}
	if a, ok := final.Get[auditA](); !ok || a.N != 1 {
		t.Errorf("pre-existing value lost: %+v ok=%v", a, ok)
	}
}

// A Tick/Listen Ctx keeps its connect-time snapshot for the connection's life.
// Writing through it must not re-encode that snapshot over everything written
// since.
func TestSession_tickWriteDoesNotRevertLaterWrites(t *testing.T) {
	m := newSessionManager(&config{})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	id := s0.id

	connect := &Ctx{req: auditReq(m, id), sessions: m}
	connect.Session() // snapshot taken at connect

	auditResolve(t, m, id).Put(auditB{7}) // a plain action later in the connection

	connect.Session().Put(auditA{2}) // the tick fires

	final := auditResolve(t, m, id)
	if b, ok := final.Get[auditB](); !ok || b.N != 7 {
		t.Errorf("tick Put re-encoded the connect-time snapshot: Put[auditB] erased (%+v ok=%v)", b, ok)
	}
	if a, ok := final.Get[auditA](); !ok || a.N != 2 {
		t.Errorf("tick's own Put did not land: %+v ok=%v", a, ok)
	}
	// The merge also refreshes the snapshot the tick reads from.
	if b, ok := connect.Session().Get[auditB](); !ok || b.N != 7 {
		t.Errorf("tick handle still serving the stale snapshot: %+v ok=%v", b, ok)
	}
}

type failStore struct {
	SessionStore
	mu              sync.Mutex
	loadErr, delErr error
	saveErr         error
}

func (f *failStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	f.mu.Lock()
	e := f.loadErr
	f.mu.Unlock()
	if e != nil {
		return nil, false, e
	}
	return f.SessionStore.Load(ctx, id)
}

func (f *failStore) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	f.mu.Lock()
	e := f.saveErr
	f.mu.Unlock()
	if e != nil {
		return e
	}
	return f.SessionStore.Save(ctx, id, data, ttl)
}

func (f *failStore) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	e := f.delErr
	f.mu.Unlock()
	if e != nil {
		return e
	}
	return f.SessionStore.Delete(ctx, id)
}

// Rotate exists to invalidate a pre-auth id. A store that cannot Delete must not
// leave that id resolving.
func TestSession_rotateInvalidatesOldIDWhenDeleteFails(t *testing.T) {
	fs := &failStore{SessionStore: NewMemorySessionStore()}
	m := newSessionManager(&config{sessionStore: fs})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	old := s0.id

	fs.mu.Lock()
	fs.delErr = errors.New("redis down")
	fs.mu.Unlock()
	newID := s0.Rotate()
	if newID == "" || newID == old {
		t.Fatalf("Rotate returned %q", newID)
	}

	if _, d, err := m.resolve(auditReq(m, old)); err != nil || d != nil {
		t.Errorf("pre-rotation id still resolves after Rotate (d=%v err=%v)", d, err)
	}
	if _, d, err := m.resolve(auditReq(m, newID)); err != nil || d == nil {
		t.Errorf("rotated-to id does not resolve (d=%v err=%v)", d, err)
	}
}

// If the tombstone cannot be written either, failing loudly beats returning a
// rotation that did not happen.
func TestSession_rotatePanicsWhenOldIDCannotBeInvalidated(t *testing.T) {
	fs := &failStore{SessionStore: NewMemorySessionStore()}
	m := newSessionManager(&config{sessionStore: fs})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})

	fs.mu.Lock()
	fs.delErr = errors.New("redis down")
	fs.saveErr = errors.New("redis down")
	fs.mu.Unlock()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Rotate returned success with the old id still valid")
		}
		if !strings.Contains(fmt.Sprint(r), "pre-rotation id is still valid") {
			t.Errorf("unexpected panic: %v", r)
		}
	}()
	s0.Rotate()
}

// A store outage must not be read as "no session": minting one would Set-Cookie
// over the user's real id and orphan their session once the store recovered.
func TestSession_storeOutageDoesNotMintOverExistingCookie(t *testing.T) {
	fs := &failStore{SessionStore: NewMemorySessionStore()}
	m := newSessionManager(&config{sessionStore: fs})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	id := s0.id

	fs.mu.Lock()
	fs.loadErr = errors.New("redis down")
	fs.mu.Unlock()

	w := httptest.NewRecorder()
	c := &Ctx{req: auditReq(m, id), sessions: m, sessW: w}
	c.Session().Put(auditB{1}) // a flash written during the outage
	if got := c.Session().id; got != "" {
		t.Errorf("minted a replacement session %s during the outage", got)
	}
	if cks := w.Result().Cookies(); len(cks) > 0 {
		t.Errorf("Set-Cookie issued during the outage: %v", cks)
	}
	if got := c.Session().Rotate(); got != "" {
		t.Errorf("Rotate minted %s during the outage", got)
	}

	fs.mu.Lock()
	fs.loadErr = nil
	fs.mu.Unlock()
	after := auditResolve(t, m, id)
	if a, ok := after.Get[auditA](); !ok || a.N != 1 {
		t.Errorf("the real session did not survive the outage: %+v ok=%v", a, ok)
	}
}

// interleaveStore fires hook once, between the load and the write of the save
// it is asked to serve — the exact window a non-atomic read-modify-write leaves
// open. It makes the lost update deterministic rather than a matter of timing.
type interleaveStore struct {
	VersionedSessionStore
	armed atomic.Bool
	hook  func()
}

func (s *interleaveStore) LoadVersion(ctx context.Context, id string) ([]byte, uint64, bool, error) {
	data, ver, ok, err := s.VersionedSessionStore.LoadVersion(ctx, id)
	if s.armed.CompareAndSwap(true, false) { // disarmed first: the hook saves too
		s.hook()
	}
	return data, ver, ok, err
}

// A write that another request overtook mid-merge must be re-merged onto the
// blob that landed, not written over it.
func TestSession_saveRetriesWhenOvertakenMidMerge(t *testing.T) {
	is := &interleaveStore{VersionedSessionStore: NewMemorySessionStore()}
	m := newSessionManager(&config{sessionStore: is})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	id := s0.id

	slow, fast := auditResolve(t, m, id), auditResolve(t, m, id)
	is.hook = func() { fast.Put(auditB{9}) } // lands between slow's load and its write
	is.armed.Store(true)
	slow.Put(auditA{2})

	final := auditResolve(t, m, id)
	if b, ok := final.Get[auditB](); !ok || b.N != 9 {
		t.Errorf("the overtaking write was clobbered: %+v ok=%v", b, ok)
	}
	if a, ok := final.Get[auditA](); !ok || a.N != 2 {
		t.Errorf("the retried write did not land: %+v ok=%v", a, ok)
	}
}

// Rotate's whole point is that the pre-rotation id stops resolving. A handle
// still pinned to that id — a Tick/Listen snapshot, or a plain request resolved
// just before the Rotate — must NOT re-create a session under it on its next
// write.
func TestSession_writeThroughARotatedAwayIDDoesNotReviveIt(t *testing.T) {
	m := newSessionManager(&config{})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	old := s0.id

	pinned := auditResolve(t, m, old) // resolved before the rotation

	newID := s0.Rotate()
	if newID == "" || newID == old {
		t.Fatalf("Rotate returned %q", newID)
	}
	if _, d, _ := m.resolve(auditReq(m, old)); d != nil {
		t.Fatal("precondition: the pre-rotation id still resolves immediately after Rotate")
	}

	pinned.Put(auditA{2})

	if _, d, err := m.resolve(auditReq(m, old)); err != nil || d != nil {
		t.Error("a write through the pinned handle re-created a session under the pre-rotation id")
	}
	after := auditResolve(t, m, newID)
	if a, ok := after.Get[auditA](); !ok || a.N != 1 {
		t.Errorf("the rotated-to session was disturbed by the dropped write: %+v ok=%v", a, ok)
	}
}

// When Delete fails, Rotate leaves an expired tombstone under the old id. A
// pinned handle writing there must not overwrite the tombstone with live values
// (which would also refresh its expiry) — that would defeat the tombstone.
func TestSession_writeThroughATombstonedIDDoesNotReviveIt(t *testing.T) {
	fs := &failStore{SessionStore: NewMemorySessionStore()}
	m := newSessionManager(&config{sessionStore: fs})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	old := s0.id

	pinned := auditResolve(t, m, old)

	fs.mu.Lock()
	fs.delErr = errors.New("redis down")
	fs.mu.Unlock()
	newID := s0.Rotate()
	if newID == "" || newID == old {
		t.Fatalf("Rotate returned %q", newID)
	}

	pinned.Put(auditA{2})

	if _, d, err := m.resolve(auditReq(m, old)); err != nil || d != nil {
		t.Error("a write through the pinned handle overwrote the rotation tombstone, reviving the old id")
	}
	after := auditResolve(t, m, newID)
	if a, ok := after.Get[auditA](); !ok || a.N != 1 {
		t.Errorf("the rotated-to session was disturbed by the dropped write: %+v ok=%v", a, ok)
	}
}

// casStuckStore never lets a conditional write apply once armed: the CAS loop
// can retry forever and never settle.
type casStuckStore struct {
	VersionedSessionStore
	armed atomic.Bool
}

func (s *casStuckStore) SaveIf(ctx context.Context, id string, data []byte, ttl time.Duration, version uint64) (bool, error) {
	if s.armed.Load() {
		return false, nil
	}
	return s.VersionedSessionStore.SaveIf(ctx, id, data, ttl, version)
}

// Contention the CAS loop cannot settle means every merge this request made was
// against a revision that moved on. Falling back to an unconditional write
// there applies a stale merge over whichever writers did get through — exactly
// the lost update the loop exists to prevent. The write must be dropped.
func TestSession_saveDropsItsWriteWhenCASNeverSettles(t *testing.T) {
	cs := &casStuckStore{VersionedSessionStore: NewMemorySessionStore()}
	m := newSessionManager(&config{sessionStore: cs})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	id := s0.id

	cs.armed.Store(true)
	auditResolve(t, m, id).Put(auditA{2})
	cs.armed.Store(false)

	final := auditResolve(t, m, id)
	if a, ok := final.Get[auditA](); !ok || a.N != 1 {
		t.Errorf("an unsettled CAS loop wrote unconditionally instead of dropping: %+v ok=%v", a, ok)
	}
}

// A handle whose id another request already rotated away must not rotate
// again: it has nothing to carry to a new id, so re-issuing the cookie would
// overwrite the good post-rotation cookie the browser holds and log the user
// out.
func TestSession_rotateThroughARetiredHandleLeavesTheGoodCookieAlone(t *testing.T) {
	m := newSessionManager(&config{})
	s0 := &Session{mgr: m, w: httptest.NewRecorder()}
	s0.Put(auditA{1})
	old := s0.id

	pinnedW := httptest.NewRecorder()
	pinned := auditResolve(t, m, old)
	pinned.w = pinnedW

	newID := s0.Rotate() // another request rotates the id away
	if newID == "" || newID == old {
		t.Fatalf("Rotate returned %q", newID)
	}
	pinned.Put(auditA{2}) // dropped; marks the handle retired

	if got := pinned.Rotate(); got != "" {
		t.Errorf("Rotate through a retired handle returned %q", got)
	}
	if cks := pinnedW.Result().Cookies(); len(cks) > 0 {
		t.Errorf("Rotate through a retired handle issued Set-Cookie %v, clobbering the browser's good cookie", cks)
	}
	if _, d, err := m.resolve(auditReq(m, newID)); err != nil || d == nil {
		t.Fatalf("the post-rotation id stopped resolving (d=%v err=%v)", d, err)
	}
	after := auditResolve(t, m, newID)
	if a, ok := after.Get[auditA](); !ok || a.N != 1 {
		t.Errorf("the live session was disturbed: %+v ok=%v", a, ok)
	}
}

// nullValsStore is a third-party store whose backend normalises an empty
// object to null — legal JSON for the same session.
type nullValsStore struct{ SessionStore }

func (s *nullValsStore) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	return s.SessionStore.Save(ctx, id, nullVals(data), ttl)
}

func nullVals(data []byte) []byte {
	var b sessionBlob
	if json.Unmarshal(data, &b) != nil || len(b.Vals) != 0 {
		return data
	}
	b.Vals = nil
	out, err := json.Marshal(b)
	if err != nil {
		return data
	}
	return out
}

// Read and write must agree on what a session is: get accepts a nil value map
// as an empty session, so save must not retire the id over the same bytes.
func TestSession_nilValsSessionReadsAndWritesAlike(t *testing.T) {
	m := newSessionManager(&config{sessionStore: &nullValsStore{SessionStore: NewMemorySessionStore()}})
	id, _ := m.create(context.Background())

	raw, ok, err := m.store.Load(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("precondition: store lost the session (ok=%v err=%v)", ok, err)
	}
	if !strings.Contains(string(raw), `"v":null`) {
		t.Fatalf("precondition: store did not normalise the empty value map: %s", raw)
	}

	s := auditResolve(t, m, id) // reads fine
	s.Put(auditA{7})

	after := auditResolve(t, m, id)
	if a, ok := after.Get[auditA](); !ok || a.N != 7 {
		t.Errorf("a session that reads fine was retired on its first write: %+v ok=%v", a, ok)
	}
}
