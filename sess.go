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
	defaultSessionTTL    = 24 * time.Hour
	defaultSessionCookie = "via_session"
	minSessionKeyLen     = 16 // bytes; below this an HMAC-SHA256 key is guessable
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
// ctx. Returning an error is reported as "no session" and logged; it never fails
// the request.
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

// MemorySessionStore returns the default process-local store: a map that is
// lost on restart and invisible to every other pod. Use it explicitly only to
// make that choice visible at the call site.
func MemorySessionStore() SessionStore {
	return &memoryStore{m: map[string]memoryEntry{}}
}

type memoryEntry struct {
	data []byte
	exp  time.Time
}

type memoryStore struct {
	mu     sync.Mutex
	m      map[string]memoryEntry
	writes int
}

func (s *memoryStore) Load(_ context.Context, id string) ([]byte, bool, error) {
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

func (s *memoryStore) Save(_ context.Context, id string, data []byte, ttl time.Duration) error {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = memoryEntry{data: data, exp: exp}
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
	return nil
}

func (s *memoryStore) Delete(_ context.Context, id string) error {
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
	forceSecure  bool      // WithSecureCookies: set Secure even when req.TLS is nil
	randomKey    bool      // key was minted at boot (no WithSessionKey, no VIA_SESSION_KEY)
	memoryStore  bool      // no WithSessionStore: sessions die with the process
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
	store, inMemory := cfg.sessionStore, false
	if store == nil {
		store, inMemory = MemorySessionStore(), true
	}
	return &sessionManager{store: store, key: key, cookie: name, ttl: ttl,
		forceSecure: cfg.sessionSecure, randomKey: random, memoryStore: inMemory}
}

// sign returns the signature appended to the id in the cookie, so a tampered
// id is rejected.
func (m *sessionManager) sign(id string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// resolve returns the session data for the request's cookie when it verifies
// and is still in the store. It never creates one — reads must not mint.
func (m *sessionManager) resolve(req *http.Request) (string, *sessionData, bool) {
	if req == nil {
		return "", nil, false
	}
	ck, err := req.Cookie(m.cookie)
	if err != nil {
		return "", nil, false
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
		return "", nil, false
	}
	d, ok := m.get(sessionCtx(req), id)
	if !ok {
		return "", nil, false
	}
	return id, d, true
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

func (m *sessionManager) get(ctx context.Context, id string) (*sessionData, bool) {
	raw, ok, err := m.store.Load(ctx, id)
	if err != nil {
		log.Printf("via: session store Load failed: %v", err)
		return nil, false
	}
	if !ok {
		return nil, false
	}
	var b sessionBlob
	if json.Unmarshal(raw, &b) != nil || b.SID == "" {
		return nil, false
	}
	exp := time.Unix(0, b.Exp)
	if b.Exp > 0 && time.Now().After(exp) {
		// via's own expiry, so a store that ignores the ttl it was handed
		// cannot resurrect an idle session.
		_ = m.store.Delete(ctx, id)
		return nil, false
	}
	if b.Vals == nil {
		b.Vals = map[string]json.RawMessage{}
	}
	d := &sessionData{sid: b.SID, vals: b.Vals, exp: exp}
	if m.ttl > 0 && time.Until(exp) < m.ttl/2 {
		m.save(ctx, id, d) // sliding idle window, at one write per half-TTL rather than per request
	}
	return d, true
}

func (m *sessionManager) save(ctx context.Context, id string, d *sessionData) {
	d.mu.Lock()
	exp := time.Now().Add(m.ttl)
	raw, err := json.Marshal(sessionBlob{SID: d.sid, Exp: exp.UnixNano(), Vals: d.vals})
	if err == nil {
		d.exp = exp
	}
	d.mu.Unlock()
	if err != nil {
		log.Printf("via: session encode failed: %v", err)
		return
	}
	if err := m.store.Save(ctx, id, raw, m.ttl); err != nil {
		log.Printf("via: session store Save failed: %v", err)
	}
}

func (m *sessionManager) create(ctx context.Context) (string, *sessionData) {
	id := randomToken() // 128-bit URL-safe token, same generator as the tab id
	d := &sessionData{sid: randomToken(), vals: map[string]json.RawMessage{}}
	m.save(ctx, id, d)
	return id, d
}

// reID moves a session's data to a fresh id and drops the old one, so a
// captured pre-rotation id no longer resolves. sid is untouched: it is the
// session's identity, and a live connection bound to it stays bound.
func (m *sessionManager) reID(ctx context.Context, oldID string, d *sessionData) string {
	newID := randomToken()
	m.save(ctx, newID, d)
	if oldID != "" {
		if err := m.store.Delete(ctx, oldID); err != nil {
			log.Printf("via: session store Delete failed: %v", err)
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
// the snapshot taken when its stream connected.
type Session struct {
	mgr    *sessionManager
	id     string // "" until resolved or created
	data   *sessionData
	w      http.ResponseWriter // nil when no response is open to carry a cookie (a Tick/Listen Ctx); live in a plain action, OnInit, AND a live action
	ctx    context.Context
	secure bool
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
	d.mu.Unlock()
	s.mgr.save(s.storeCtx(), s.id, d)
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
	if s.data == nil {
		// Rotating an empty session still mints a fresh id.
		s.id, s.data = s.mgr.create(s.storeCtx())
		s.mgr.setCookie(s.w, s.id, s.secure)
		return s.id
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
	s.data.mu.Unlock()
	s.mgr.save(s.storeCtx(), s.id, s.data)
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
		s.secure = c.sessions.forceSecure || (c.req != nil && c.req.TLS != nil)
		if id, d, ok := c.sessions.resolve(c.req); ok {
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

// Clear removes the value stored under T's key — a logout dropping the
// session-held user.
func (s *Session) Clear[T any]() {
	s.clear(sessionKey[T]())
}
