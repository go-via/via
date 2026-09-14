package via

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultSessionTTL = 24 * time.Hour
	// defaultSessionStoreTimeout bounds one store operation. Store calls
	// deliberately outlive the request's context (see sessionCtx), so without a
	// deadline of their own a hung backend pins the goroutine indefinitely.
	defaultSessionStoreTimeout = 5 * time.Second
	defaultSessionCookie       = "via_session"
	minSessionKeyLen           = 16 // bytes; below this an HMAC-SHA256 key is guessable
)

// SessionStore is where session state lives between requests. The default is a
// process-local map, which is why a restart logs everyone out and a second pod
// sees none of the first pod's sessions; point [WithSessionStore] at Redis, a
// SQL table, or anything else that outlives the process and both stop being
// true.
//
// A store is a blob map and nothing more. via hands each session over
// already-serialized and never asks a store to understand it, so there is no
// session type to implement against:
//
//   - Rotation is Save under the new id then Delete of the old one; a store
//     implements neither.
//   - Expiry is the ttl handed to Save. Honour it if the backend does it for
//     free (Redis SETEX, a SQL expires_at column); via stamps the blob with the
//     same deadline and refuses an expired Load regardless, so a store that
//     ignores ttl is still correct — it just keeps dead rows around.
//   - The idle window slides: via re-Saves a session once less than half its
//     TTL is left, so a store never has to touch expiry on Load.
//
// Every method may be called concurrently, and from a request goroutine — honour
// ctx. An error is logged and, with one exception, never fails the request: a
// failed Load is treated as "the store could not answer", which is NOT "no
// session" — via refuses to mint a replacement over a cookie the browser
// already holds, and drops the write instead. The exception is
// [Session.Rotate]: if the old id can be neither deleted nor overwritten with
// an expired blob, the pre-rotation id would stay valid, so via panics and the
// request answers 500 rather than reporting a rotation that did not happen.
//
// Implement [VersionedSessionStore] as well if the backend can do a conditional
// write; without it, two requests writing the same session at the same instant
// can still lose one.
type SessionStore interface {
	// Load returns the blob stored under id. ok is false when id is unknown or
	// expired. err is for backend failures only.
	Load(ctx context.Context, id string) (data []byte, ok bool, err error)
	// Save writes data under id and arms its expiry ttl from now, replacing any
	// blob already there.
	Save(ctx context.Context, id string, data []byte, ttl time.Duration) error
	// Delete removes id. Deleting an id that is not there is not an error.
	Delete(ctx context.Context, id string) error
}

// VersionedSessionStore is the optional half of [SessionStore], for a backend that
// can make a write conditional on the revision it read. via writes a session by
// re-reading the stored blob and overlaying the keys this request touched; with
// a plain store that read-modify-write is not atomic, so two requests writing at
// the same instant can still lose one of them. Implement this and via retries
// the merge until its write applies to the revision it merged against, which
// closes the window entirely.
//
// version is opaque and store-defined; 0 means "no blob stored". Redis does this
// with WATCH/MULTI or a Lua script, SQL with an UPDATE ... WHERE version = $n.
// The default memory store implements it.
type VersionedSessionStore interface {
	SessionStore
	// LoadVersion is Load, plus the revision token of the blob returned.
	LoadVersion(ctx context.Context, id string) (data []byte, version uint64, ok bool, err error)
	// SaveIf is Save, applied only while the stored revision is still version.
	// ok is false — with a nil error — when it moved on and the caller must
	// re-read and retry.
	SaveIf(ctx context.Context, id string, data []byte, ttl time.Duration, version uint64) (ok bool, err error)
}

// NewMemorySessionStore returns the default process-local store: a map that is
// lost on restart and invisible to every other pod. Use it explicitly only to
// make that choice visible at the call site.
//
// It returns the concrete type, not the [SessionStore] interface: the store
// also implements [VersionedSessionStore], and a wrapper built around the
// interface would silently drop the CAS path and take the lossy merge instead.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{m: map[string]memoryEntry{}}
}

type memoryEntry struct {
	data []byte
	exp  time.Time
	ver  uint64
}

// MemorySessionStore is the process-local default store — a map guarded by a
// mutex, with CAS support ([VersionedSessionStore]). Build one with
// [NewMemorySessionStore]; the zero value is not usable.
type MemorySessionStore struct {
	mu     sync.Mutex
	m      map[string]memoryEntry
	writes int
	ver    uint64
}

// Load implements [SessionStore]. An entry past its TTL is deleted on sight
// and reported absent.
func (s *MemorySessionStore) Load(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok {
		return nil, false, nil
	}
	if !e.exp.IsZero() && time.Now().After(e.exp) {
		delete(s.m, id)
		return nil, false, nil
	}
	return e.data, true, nil
}

// LoadVersion implements [VersionedSessionStore]. The revision is a
// process-wide counter, so it changes on any write, not only this id's.
func (s *MemorySessionStore) LoadVersion(_ context.Context, id string) ([]byte, uint64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok {
		return nil, 0, false, nil
	}
	if !e.exp.IsZero() && time.Now().After(e.exp) {
		delete(s.m, id)
		return nil, 0, false, nil
	}
	return e.data, e.ver, true, nil
}

// SaveIf implements [VersionedSessionStore]. An absent or expired entry reads
// as version 0, so a first write must pass 0.
func (s *MemorySessionStore) SaveIf(_ context.Context, id string, data []byte, ttl time.Duration, version uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cur uint64
	if e, ok := s.m[id]; ok && (e.exp.IsZero() || !time.Now().After(e.exp)) {
		cur = e.ver
	}
	if cur != version {
		return false, nil
	}
	s.store(id, data, ttl)
	return true, nil
}

// Save implements [SessionStore]. A ttl of 0 or less stores the blob without
// an expiry.
func (s *MemorySessionStore) Save(_ context.Context, id string, data []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store(id, data, ttl)
	return nil
}

// store writes under the held lock and stamps a fresh revision.
func (s *MemorySessionStore) store(id string, data []byte, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	s.ver++
	s.m[id] = memoryEntry{data: data, exp: exp, ver: s.ver}
	// Expiry is otherwise lazy-on-read, so a session nobody ever revisits is
	// never reclaimed. Amortised over writes rather than per write: the sweep
	// is O(n) and a login-heavy burst would otherwise pay it every time.
	s.writes++
	if s.writes%memorySweepEvery == 0 {
		now := time.Now()
		for k, e := range s.m {
			if !e.exp.IsZero() && now.After(e.exp) {
				delete(s.m, k)
			}
		}
	}
}

// Delete implements [SessionStore]. Deleting an absent id is not an error.
func (s *MemorySessionStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

const memorySweepEvery = 256

// sessionData is one browser session as this request sees it: a decoded blob,
// not a shared live object. sid is the session's identity, minted once and
// carried across every Rotate, so a connection bound to a session still
// recognises it after its id changed — and recognises it on another pod, which
// a pointer identity could never do.
type sessionData struct {
	mu   sync.Mutex
	sid  string
	vals map[string]json.RawMessage
	exp  time.Time
	// dirty is THIS request's write set: the keys it Put or Cleared, a nil
	// value marking a Clear. Saving overlays only these onto whatever the
	// store holds now, so a concurrent request writing a DIFFERENT key is not
	// erased by this one re-encoding its own stale copy. It accumulates for
	// the life of the handle and is never trimmed: a re-save must be able to
	// re-apply every write the request made.
	dirty map[string]json.RawMessage
	// retired is set once a save finds the id no longer names this session —
	// rotated away, expired, or recycled. Later writes through the same handle
	// then short-circuit instead of re-round-tripping the store to relearn it.
	retired bool
	// mintFailed records that the store refused the very first write, so a
	// later dropped write is reported as the store outage it is rather than as
	// a rotation that never happened.
	mintFailed bool
}

// sessionBlob is the wire form of a session. Field names are short because
// every request that writes re-encodes the whole thing.
type sessionBlob struct {
	SID  string                     `json:"sid"`
	Exp  int64                      `json:"exp"` // unix nanos; via's own expiry check, independent of the store's
	Vals map[string]json.RawMessage `json:"v"`
}

// sessionManager holds the per-Router session config and store. Sessions are
// always available; the cookie is issued lazily on the first write.
type sessionManager struct {
	store        SessionStore
	key          []byte
	cookie       string
	ttl          time.Duration
	forceSecure  bool // WithSecureCookies: set Secure even when req.TLS is nil
	randomKey    bool // key was minted at boot (no WithSessionKey, no VIA_SESSION_KEY)
	memoryStore  bool // no WithSessionStore: sessions die with the process
	storeTimeout time.Duration
	keyWarnOnce  sync.Once // warn about the random key at the FIRST session mint, not at boot
	storeWarn    sync.Once // warn about the process-local store at the FIRST session mint
	mismatchOnce sync.Once // warn once about signature-mismatch cookies (the two-apps clobber)
}

// newSessionManager resolves the signing key: WithSessionKey → VIA_SESSION_KEY
// → a random per-process key. The random fallback warns on first use; a stable
// key is what makes the COOKIE survive restarts and span pods, and a shared
// SessionStore is what makes the DATA behind it do the same.
func newSessionManager(cfg *config) *sessionManager {
	key := cfg.sessionKey
	if len(key) == 0 {
		if env := os.Getenv("VIA_SESSION_KEY"); env != "" {
			key = []byte(env)
		}
	}
	random := false
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic("via: session key generation failed: " + err.Error())
		}
		random = true
	} else if len(key) < minSessionKeyLen {
		// HMAC-SHA256 accepts any length, so a short key would fail silently
		// into a forgeable signature. Fail at construction, not at request time.
		panic(fmt.Sprintf("via: session key must be at least %d bytes (got %d)", minSessionKeyLen, len(key)))
	}
	ttl := cfg.sessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	name := cfg.sessionCookie
	if name == "" {
		name = defaultSessionCookie
	}
	timeout := cfg.sessionTimeout
	if timeout <= 0 {
		timeout = defaultSessionStoreTimeout
	}
	store, inMemory := cfg.sessionStore, false
	if store == nil {
		store, inMemory = NewMemorySessionStore(), true
	}
	return &sessionManager{store: store, key: key, cookie: name, ttl: ttl,
		forceSecure: cfg.sessionSecure, randomKey: random, memoryStore: inMemory,
		storeTimeout: timeout}
}

// sign returns the signature appended to the id in the cookie, so a tampered
// id is rejected.
func (m *sessionManager) sign(id string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// resolve returns the session data for the request's cookie when it verifies
// and is still in the store. It never creates one — reads must not mint. A nil
// sessionData with a nil error is "no session"; a non-nil error is "the store
// could not answer", which callers must not treat as "no session".
func (m *sessionManager) resolve(req *http.Request) (string, *sessionData, error) {
	if req == nil {
		return "", nil, nil
	}
	ck, err := req.Cookie(m.cookie)
	if err != nil {
		return "", nil, nil
	}
	id, ok := m.verify(ck.Value)
	if !ok {
		// Almost always another app on the same host signing the same cookie
		// name with a different key (two dev servers on localhost ports).
		// Silence here reads as "my session randomly resets".
		m.mismatchOnce.Do(func() {
			log.Print("via: session cookie failed its signature check — likely another app on this host " +
				"uses the same cookie name with a different key; issuing a fresh session " +
				"(set WithSessionCookieName or share VIA_SESSION_KEY to stop the clobber)")
		})
		return "", nil, nil
	}
	d, err := m.get(sessionCtx(req), id)
	if err != nil {
		return "", nil, err
	}
	return id, d, nil
}

// bounded caps one store operation. Applied at the sessionManager entry points
// rather than at each m.store call, so a save's CAS retry loop is bounded as a
// whole instead of restarting its clock on every attempt.
func (m *sessionManager) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if m.storeTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, m.storeTimeout)
}

// sessionCtx detaches the request's context for store calls: a session write
// must land even when the client hung up mid-request, and a live unit's Ctx
// holds a request whose context is already done.
func sessionCtx(req *http.Request) context.Context {
	if req == nil {
		return context.Background()
	}
	return context.WithoutCancel(req.Context())
}

// get returns the session stored under id. A nil sessionData with a nil error
// means "no such session"; a non-nil error means the STORE is unreachable,
// which is a third state and not the same thing — minting a replacement on a
// backend blip would overwrite the user's cookie and orphan their real session
// the moment the backend came back.
func (m *sessionManager) get(ctx context.Context, id string) (*sessionData, error) {
	ctx, cancel := m.bounded(ctx)
	defer cancel()
	raw, ok, err := m.store.Load(ctx, id)
	if err != nil {
		log.Printf("via: session store Load failed: %v", err)
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var b sessionBlob
	if json.Unmarshal(raw, &b) != nil || b.SID == "" {
		return nil, nil
	}
	exp := time.Unix(0, b.Exp)
	if b.Exp > 0 && time.Now().After(exp) {
		// via's own expiry, so a store that ignores the ttl it was handed
		// cannot resurrect an idle session.
		_ = m.store.Delete(ctx, id)
		return nil, nil
	}
	if b.Vals == nil {
		b.Vals = map[string]json.RawMessage{}
	}
	d := &sessionData{sid: b.SID, vals: b.Vals, exp: exp, dirty: map[string]json.RawMessage{}}
	if m.ttl > 0 && time.Until(exp) < m.ttl/2 {
		m.save(ctx, id, d, false) // sliding idle window, at one write per half-TTL rather than per request
	}
	return d, nil
}

// save writes d back under id, merging rather than replacing: the blob the
// store holds RIGHT NOW is re-read and only this request's write set is
// overlaid onto it. Two requests on one session therefore each keep their own
// keys, where re-encoding a whole decoded copy silently dropped whichever
// finished first. What is left is a read-modify-write window of one store
// round-trip, and last-writer-wins on the SAME key — see [Session].
//
// mint says id is a brand-new home for d — a freshly created session, or the
// new id of a Rotate — and is the ONLY case allowed to write where the store
// holds no live blob for it. Without that gate a handle still holding a
// pre-rotation id would re-create a valid session under it on its next write:
// the rotated-away session never sees the write, and the id Rotate exists to
// invalidate resolves again.
//
// sessionSaveRetries caps the CAS loop. Contention on ONE session id is a
// handful of tabs, not a thundering herd, so exhausting it means the store is
// pathological; the write is dropped rather than applied unconditionally over
// a blob up to that many revisions stale, which is the very lost update the
// CAS loop exists to prevent.
const sessionSaveRetries = 8

func (m *sessionManager) save(ctx context.Context, id string, d *sessionData, mint bool) bool {
	d.mu.Lock()
	if d.retired {
		d.mu.Unlock()
		return false
	}
	mintFailed := d.mintFailed
	sid, dirty := d.sid, make(map[string]json.RawMessage, len(d.dirty))
	for k, v := range d.dirty {
		dirty[k] = v
	}
	own := make(map[string]json.RawMessage, len(d.vals))
	for k, v := range d.vals {
		own[k] = v
	}
	d.mu.Unlock()

	ctx, cancel := m.bounded(ctx)
	defer cancel()
	cas, _ := m.store.(VersionedSessionStore)
	for attempt := 0; ; attempt++ {
		if cas != nil && attempt >= sessionSaveRetries {
			log.Printf("via: session write gave up after %d CAS attempts — the store is under "+
				"pathological contention on one session; the write is dropped rather than "+
				"clobbering the writers that got through", sessionSaveRetries)
			return false
		}
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
			log.Printf("via: session store Load failed before save: %v", err)
			return false
		}
		var b sessionBlob
		// Deliberately no b.Vals != nil clause: get accepts a nil value map as
		// an empty session, and a store that normalises {} to null would
		// otherwise yield a session that reads fine and retires on first write.
		live := ok && json.Unmarshal(raw, &b) == nil && b.SID == sid &&
			(b.Exp <= 0 || time.Now().Before(time.Unix(0, b.Exp)))
		if !live && !mint {
			if mintFailed {
				log.Print("via: session write dropped — the store rejected this session's first write, " +
					"so there is nothing under its id to merge into")
			} else {
				log.Print("via: session id retired (rotated away or expired); write dropped — " +
					"writing under it would revive an id that no longer names this session")
			}
			d.mu.Lock()
			d.retired = true
			d.mu.Unlock()
			return false
		}
		vals := make(map[string]json.RawMessage, len(own))
		for k, v := range own {
			vals[k] = v
		}
		if live && b.Vals != nil {
			vals = b.Vals
		}
		for k, v := range dirty {
			if v == nil {
				delete(vals, k)
				continue
			}
			vals[k] = v
		}

		exp := time.Now().Add(m.ttl)
		blob, err := json.Marshal(sessionBlob{SID: sid, Exp: exp.UnixNano(), Vals: vals})
		if err != nil {
			log.Printf("via: session encode failed: %v", err)
			return false
		}
		if cas != nil {
			applied, err := cas.SaveIf(ctx, id, blob, m.ttl, ver)
			if err != nil {
				log.Printf("via: session store SaveIf failed: %v", err)
				return false
			}
			if !applied {
				continue // another request wrote first; re-merge onto its blob
			}
		} else if err := m.store.Save(ctx, id, blob, m.ttl); err != nil {
			log.Printf("via: session store Save failed: %v", err)
			return false
		}
		d.mu.Lock()
		// The merged view is what this request should read back too: a Tick
		// handler holding a connect-time snapshot otherwise keeps serving
		// values another request has since replaced.
		d.vals, d.exp = vals, exp
		d.mu.Unlock()
		return true
	}
}

func (m *sessionManager) create(ctx context.Context) (string, *sessionData) {
	id := randomToken() // 128-bit URL-safe token, same generator as the tab id
	d := &sessionData{sid: randomToken(), vals: map[string]json.RawMessage{}, dirty: map[string]json.RawMessage{}}
	if !m.save(ctx, id, d, true) {
		d.mu.Lock()
		d.mintFailed = true
		d.mu.Unlock()
	}
	return id, d
}

// reID moves a session's data to a fresh id and drops the old one, so a
// captured pre-rotation id no longer resolves. sid is untouched: it is the
// session's identity, and a live connection bound to it stays bound.
func (m *sessionManager) reID(ctx context.Context, oldID string, d *sessionData) string {
	ctx, cancel := m.bounded(ctx)
	defer cancel()
	newID := randomToken()
	m.save(ctx, newID, d, true)
	if oldID != "" {
		if err := m.store.Delete(ctx, oldID); err != nil {
			// Returning the new id while the old one still resolves would void
			// the fixation defence Rotate exists to provide, and void it
			// exactly when the store is flaky. Overwrite the old id with a
			// blob that is already expired instead: get deletes it on sight.
			log.Printf("via: session store Delete failed on rotate, writing an expired tombstone: %v", err)
			dead, _ := json.Marshal(sessionBlob{SID: d.sid, Exp: time.Now().Add(-time.Minute).UnixNano()})
			if err := m.store.Save(ctx, oldID, dead, time.Second); err != nil {
				panic("via: Session.Rotate could neither delete nor invalidate the old session id, " +
					"so the pre-rotation id is still valid: " + err.Error())
			}
		}
	}
	return newID
}

// verify splits "id.sig" and constant-time-compares the recomputed signature.
func (m *sessionManager) verify(value string) (string, bool) {
	i := strings.LastIndexByte(value, '.')
	if i < 0 {
		return "", false
	}
	id, sig := value[:i], value[i+1:]
	if !hmac.Equal([]byte(sig), []byte(m.sign(id))) {
		return "", false
	}
	return id, true
}

func (m *sessionManager) setCookie(w http.ResponseWriter, id string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cookie,
		Value:    id + "." + m.sign(id),
		Path:     "/",
		MaxAge:   int(m.ttl.Seconds()), // persist up to the idle TTL, not just the browser session
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Session is a browser session's value bag, resolved from the signed cookie,
// created lazily on the first write — an app that never stores anything stays
// cookieless. A value is keyed by the Go type used to store it, and stored as
// JSON: T must round-trip through encoding/json, because the bytes may be read
// back by a different process.
//
// SECURITY: sessions do NOT rotate their id on their own. Call [Session.Rotate]
// at every auth-state change (login, logout, privilege elevation) to invalidate
// an id an attacker may have planted before it — fixation defense.
//
// Where the data lives is [SessionStore]'s business: the default is
// process-local, so a restart logs everyone out and a second pod sees nothing —
// [WithSessionStore] is the fix. Expiry is an idle window (default 24h) that
// slides as the session is used.
//
// Each request decodes its own copy, so a write is visible to the next request,
// not to a request already in flight. A live unit's Tick or Listen handler sees
// the snapshot taken when its stream connected — and refreshed by its own next
// write.
//
// Writes from two in-flight requests on one session are merged per key: each
// write re-reads the stored blob and overlays only the keys that request
// touched, so a Put in one tab does not erase a Put of a DIFFERENT type in
// another. Two requests writing the SAME type resolve last-writer-wins.
//
// The merge is a read-modify-write. Against a store that implements
// [VersionedSessionStore] — the default one does — it re-merges and retries until
// its write applies to the revision it merged against, so a concurrent write is
// not clobbered; under contention that will not settle it gives up after a
// bounded number of attempts and drops ITS OWN write, with a log. Against a
// store that does not, the window between the read and the write is real: a
// write landing inside another's round-trip is dropped. Either way a session is
// a value bag, not a counter and not a lock.
//
// A write through a handle whose id has been rotated away or has expired is
// also dropped, with a log: reviving that id would undo [Session.Rotate].
//
// The handle itself is NOT safe for concurrent use — only the stored data is
// merged across requests. Call it from the via callback that handed it to you;
// see the package doc for the goroutine model.
type Session struct {
	mgr    *sessionManager
	id     string // "" until resolved or created
	data   *sessionData
	w      http.ResponseWriter // nil when no response is open to carry a cookie (a Tick/Listen Ctx); live in a plain action, OnInit, AND a live action
	ctx    context.Context
	secure bool
	// errPage marks the session of a WithErrorPage render, which has no response
	// of its own to carry a Set-Cookie.
	errPage bool
	// down is set when the store could not be read for this request. The
	// session is then neither present nor absent, and writes are dropped
	// rather than minting a replacement over the user's real cookie.
	down bool
}

func (s *Session) storeCtx() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

// ensure creates the session and issues the cookie on first write. A write with
// no open response at all — a Tick or Listen handler's Ctx — still stores into a
// fresh session but warns, since the browser will never carry that id back.
func (s *Session) ensure() *sessionData {
	if s.mgr == nil {
		return nil
	}
	if s.data != nil {
		return s.data
	}
	if s.down {
		// The user almost certainly HAS a session; the store just could not say
		// so. Minting one here would Set-Cookie over their real id and log them
		// out permanently once the backend recovered — a far worse outcome than
		// a dropped write, and the store contract already says a backend error
		// never fails the request.
		log.Print("via: session write dropped — the session store could not be read for this request, " +
			"so via will not mint a replacement session over the one the browser already holds")
		return nil
	}
	if s.mgr.randomKey {
		s.mgr.keyWarnOnce.Do(func() {
			log.Print("via: session minted under a random per-process key — sessions will not survive a " +
				"restart or span pods; set WithSessionKey or the VIA_SESSION_KEY env for a stable key")
		})
	}
	if s.mgr.memoryStore {
		s.mgr.storeWarn.Do(func() {
			log.Print("via: sessions are held in this process's memory — every restart or deploy logs " +
				"every user out, and a second pod sees none of them; pass WithSessionStore for a " +
				"shared store")
		})
	}
	id, d := s.mgr.create(s.storeCtx())
	s.id, s.data = id, d
	if s.w != nil {
		s.mgr.setCookie(s.w, id, s.secure)
	} else if s.errPage {
		log.Print("via: session written from a WithErrorPage handler, where no cookie can be set — the " +
			"failing response is already committed; treat the session as read-only in an error page")
	} else {
		log.Print("via: session created where no cookie can be set (a Tick or Listen handler, which has no " +
			"request in flight); establish the session in OnInit or an action instead")
	}
	return d
}

func (s *Session) load(key string) (json.RawMessage, bool) {
	if s.data == nil {
		return nil, false
	}
	s.data.mu.Lock()
	defer s.data.mu.Unlock()
	v, ok := s.data.vals[key]
	return v, ok
}

func (s *Session) set(key string, value json.RawMessage) {
	d := s.ensure()
	if d == nil {
		return
	}
	d.mu.Lock()
	d.vals[key] = value
	d.dirty[key] = value
	d.mu.Unlock()
	s.mgr.save(s.storeCtx(), s.id, d, false)
}

// Rotate issues a fresh session id, carries the existing data to it, and
// re-sets the cookie — call it after every auth-state change (login, privilege
// elevation) so a fixed pre-auth id is invalidated. Returns the new id, or ""
// when no response is open to carry the cookie; rotate from a plain action or
// OnInit.
func (s *Session) Rotate() string {
	if s.mgr == nil || s.w == nil {
		return ""
	}
	if s.down {
		log.Print("via: Session.Rotate skipped — the session store could not be read for this request")
		return ""
	}
	if s.data == nil {
		s.id, s.data = s.mgr.create(s.storeCtx())
		s.mgr.setCookie(s.w, s.id, s.secure)
		return s.id
	}
	s.data.mu.Lock()
	retired := s.data.retired
	s.data.mu.Unlock()
	if retired {
		// Another request already rotated this id away, so the browser's cookie
		// names THAT request's new id. reID would short-circuit on the retired
		// flag and write nothing, then Set-Cookie an id with no blob behind it —
		// overwriting a good cookie and logging the user out. Leave it alone.
		log.Print("via: Session.Rotate skipped — this handle's session id was already rotated away by " +
			"another request; the cookie the browser now holds is left untouched")
		return ""
	}
	s.id = s.mgr.reID(s.storeCtx(), s.id, s.data)
	s.mgr.setCookie(s.w, s.id, s.secure)
	return s.id
}

func (s *Session) clear(key string) {
	if s.data == nil {
		return
	}
	s.data.mu.Lock()
	delete(s.data.vals, key)
	s.data.dirty[key] = nil // tombstone: a Clear must survive the merge, not just be absent from it
	s.data.mu.Unlock()
	s.mgr.save(s.storeCtx(), s.id, s.data, false)
}

// Session resolves the browser session for this Ctx, always returning a usable
// handle so callers never need a nil check. The cookie is read here but issued
// only on the first write (see Session.ensure).
func (c *Ctx) Session() *Session {
	if c.session != nil {
		return c.session
	}
	s := &Session{ctx: sessionCtx(c.req)}
	if c.sessions != nil {
		s.mgr = c.sessions
		s.w = c.sessW
		s.errPage = c.errPage
		s.secure = c.sessions.forceSecure || (c.req != nil && c.req.TLS != nil)
		switch id, d, err := c.sessions.resolve(c.req); {
		case err != nil:
			s.down = true
		case d != nil:
			s.id, s.data = id, d
		}
	}
	c.session = s
	return s
}

// sessionKey names T on the wire. A printed type name, so the key a session was
// written under still decodes on another pod — which the per-process sentinel
// pointer this replaced could never do. %T of a typed nil pointer, not reflect:
// via's core stays import-free of reflect outside the three type-setup files.
// Renaming or moving T retires the values already stored under it, and two
// same-named types in same-named packages would share a key.
func sessionKey[T any]() string { return fmt.Sprintf("%T", (*T)(nil)) }

// Put stores a typed value in the session, keyed by its type — the
// one-per-session value like the logged-in user. The first Put issues the
// cookie, and only where a response is open: a plain action, OnInit, or a live
// action. It panics if T does not marshal to JSON: a session may be read back
// by another process, so an unencodable value has nowhere to go.
//
// SECURITY: Put does NOT rotate the session id. Call [Session.Rotate] right
// after a Put that changes auth state, so a pre-auth id an attacker planted
// doesn't survive the login.
func (s *Session) Put[T any](v T) {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("via: Session.Put: " + sessionKey[T]() + " does not marshal to JSON: " + err.Error())
	}
	s.set(sessionKey[T](), raw)
}

// Get reads the value stored with [Session.Put] for type T, returning the zero
// value and false when nothing is stored or the stored bytes no longer decode
// into T.
func (s *Session) Get[T any]() (T, bool) {
	var zero T
	raw, ok := s.load(sessionKey[T]())
	if !ok {
		return zero, false
	}
	var v T
	if json.Unmarshal(raw, &v) != nil {
		return zero, false
	}
	return v, true
}

// Delete removes the value stored under T's key — a logout dropping the
// session-held user.
func (s *Session) Delete[T any]() {
	s.clear(sessionKey[T]())
}
