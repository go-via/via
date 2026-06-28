package via

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-via/via/v2/internal/sessbridge"
)

const (
	defaultSessionTTL    = 24 * time.Hour
	defaultSessionCookie = "via_session"
)

func init() {
	sessbridge.Load = func(s any, key any) (any, bool) { return s.(*Session).load(key) }
	sessbridge.Store = func(s any, key any, value any) { s.(*Session).set(key, value) }
	sessbridge.Delete = func(s any, key any) { s.(*Session).clear(key) }
}

// sessionData is one browser session's value bag. Values are keyed by an opaque
// key (via/sess uses a per-type sentinel) so distinct typed values coexist.
type sessionData struct {
	mu   sync.Mutex
	vals map[any]any
	seen time.Time // last access, for idle-TTL eviction
}

// sessionStore is the per-Register in-memory session table, keyed by signed id.
type sessionStore struct {
	mu  sync.Mutex
	m   map[string]*sessionData
	ttl time.Duration
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{m: map[string]*sessionData{}, ttl: ttl}
}

func (st *sessionStore) get(id string) (*sessionData, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	d, ok := st.m[id]
	if !ok {
		return nil, false
	}
	if st.ttl > 0 && time.Since(d.seen) > st.ttl {
		delete(st.m, id) // idle past the TTL — evict and treat as gone
		return nil, false
	}
	d.seen = time.Now() // sliding window: each access keeps it warm
	return d, true
}

// reID moves an existing session's data to a fresh id and drops the old one, so
// a captured pre-rotation id no longer resolves.
func (st *sessionStore) reID(oldID string, d *sessionData) string {
	newID := genCSPNonce()
	st.mu.Lock()
	delete(st.m, oldID)
	st.m[newID] = d
	st.mu.Unlock()
	return newID
}

func (st *sessionStore) create() (string, *sessionData) {
	id := genCSPNonce() // 128-bit URL-safe token, same generator as the tab id
	d := &sessionData{vals: map[any]any{}, seen: time.Now()}
	st.mu.Lock()
	st.m[id] = d
	st.mu.Unlock()
	return id, d
}

// sessionManager holds the per-Register session config + store. Nil when the app
// did not opt into sessions.
type sessionManager struct {
	store       *sessionStore
	key         []byte
	cookie      string
	ttl         time.Duration
	forceSecure bool // WithSecureCookies: set Secure even when req.TLS is nil
}

func newSessionManager(cfg *config) *sessionManager {
	key := cfg.sessionKey
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic("via: session key generation failed: " + err.Error())
		}
		log.Print("via: sessions enabled without WithSessionKey — using a random per-process key; " +
			"set a stable key so sessions survive restarts and span processes")
	}
	ttl := cfg.sessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	name := cfg.sessionCookie
	if name == "" {
		name = defaultSessionCookie
	}
	return &sessionManager{store: newSessionStore(ttl), key: key, cookie: name, ttl: ttl, forceSecure: cfg.sessionSecure}
}

// sign returns base64url(HMAC-SHA256(key, id)) — the signature appended to the id
// in the cookie so a tampered id is rejected.
func (m *sessionManager) sign(id string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// resolve returns the session data for the request's cookie, if the cookie is
// present, its signature verifies, and the id is still in the store. It never
// creates a session — reads must not mint one.
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
		return "", nil, false
	}
	d, ok := m.store.get(id)
	if !ok {
		return "", nil, false
	}
	return id, d, true
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

// Session is a browser session's value bag, resolved from the signed cookie. It
// is created lazily on the first write, and only then is the cookie issued — an
// app that never stores anything stays cookieless. Typed access is in via/sess.
type Session struct {
	mgr    *sessionManager
	id     string // current session id; "" until resolved or created
	data   *sessionData
	w      http.ResponseWriter // nil when the cookie can't be set (a live action)
	secure bool
}

// ensure returns the session's data, creating the session (and issuing the
// cookie) on first write. A write where no cookie can be set — a live action,
// which runs after its 204 — still stores into a fresh session but logs a
// warning, since the browser will never carry that id back.
func (s *Session) ensure() *sessionData {
	if s.mgr == nil {
		return nil
	}
	if s.data != nil {
		return s.data
	}
	id, d := s.mgr.store.create()
	s.id, s.data = id, d
	if s.w != nil {
		s.mgr.setCookie(s.w, id, s.secure)
	} else {
		log.Print("via: session created where no cookie can be set (a live action runs after its response); " +
			"establish the session in OnConnect or a stateless action")
	}
	return d
}

func (s *Session) load(key any) (any, bool) {
	if s.data == nil {
		return nil, false
	}
	s.data.mu.Lock()
	defer s.data.mu.Unlock()
	v, ok := s.data.vals[key]
	return v, ok
}

func (s *Session) set(key any, value any) {
	d := s.ensure()
	if d == nil {
		return
	}
	d.mu.Lock()
	d.vals[key] = value
	d.mu.Unlock()
}

// Rotate issues a fresh session id, carrying the existing data to it, and
// re-sets the cookie on the open response — call it after an auth state change
// (login, privilege elevation) so a fixed pre-auth id is invalidated. Returns
// the new id, or "" when no response is open to carry the new cookie (a live
// action): rotate from a stateless action or OnConnect.
func (s *Session) Rotate() string {
	if s.mgr == nil || s.w == nil {
		return ""
	}
	if s.data == nil {
		// Nothing stored yet — rotation of an empty session still mints a fresh id.
		s.id, s.data = s.mgr.store.create()
		s.mgr.setCookie(s.w, s.id, s.secure)
		return s.id
	}
	s.id = s.mgr.store.reID(s.id, s.data)
	s.mgr.setCookie(s.w, s.id, s.secure)
	return s.id
}

func (s *Session) clear(key any) {
	if s.data == nil {
		return
	}
	s.data.mu.Lock()
	delete(s.data.vals, key)
	s.data.mu.Unlock()
}

// Session resolves the browser session for this Ctx. Returns a usable handle
// even when sessions are disabled (reads yield nothing, writes no-op), so the
// via/sess helpers never need a nil check. The cookie is read here but issued
// only on the first write (see Session.ensure).
func (c *Ctx) Session() *Session {
	if c.session != nil {
		return c.session
	}
	s := &Session{}
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
