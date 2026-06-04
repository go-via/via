package via

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-via/via/internal/sessbridge"
)

// Wire the sess package's access path to the unexported KV methods.
// See internal/sessbridge for why this is a function-var bridge.
func init() {
	sessbridge.Load = func(s any, key string) (any, bool) { return s.(*Session).load(key) }
	sessbridge.Store = func(s any, key string, value any) { s.(*Session).store(key, value) }
	sessbridge.Delete = func(s any, key string) { s.(*Session).delete(key) }
}

// sessionCookieName is the name of the HTTP cookie via uses to identify
// a browser session across requests. Centralized here so set/read/delete
// paths can never drift.
const sessionCookieName = "via_session"

type session struct {
	id         string
	data       kvStore
	lastAccess atomic.Int64
}

// Session is the per-browser session value bag. Survives tab close;
// expires per [WithSessionTTL].
//
// A Session obtained via [Ctx.Session] marks the page dirty + fans out
// to subscribed tabs on writes; one obtained via [RequestSession] (in a
// middleware, before a Ctx exists) is cookie-only and does not trigger
// re-render.
//
// All value access is typed and lives in the via/sess subpackage —
// sess.Get[T] / sess.Put[T] / sess.Clear[T]. Session itself only
// exposes [Session.Rotate].
type Session struct {
	data *session
	ctx  *Ctx
	app  *App
}

// load reads the value stored under key, or nil/false if absent or if
// the Session is detached (no underlying session record). Unexported:
// the only sanctioned access path is the typed via/sess package, which
// reaches these through internal/sessbridge.
func (s *Session) load(key string) (any, bool) {
	if s == nil || s.data == nil {
		return nil, false
	}
	return s.data.data.Load(key)
}

// store writes value under key. When the Session is bound to a Ctx,
// also marks the page dirty so the view re-renders and fans the write
// out to every other live tab on the same session subscribed to key.
func (s *Session) store(key string, value any) {
	if s == nil || s.data == nil {
		return
	}
	s.data.data.Store(key, value)
	if s.ctx != nil {
		s.ctx.markStateDirty()
	}
	if s.app != nil {
		s.app.broadcastRender(s.ctx, s.data, key)
	}
}

// delete removes the value stored under key. When the Session is bound
// to a Ctx, also marks the page dirty so the view re-renders.
func (s *Session) delete(key string) {
	if s == nil || s.data == nil {
		return
	}
	s.data.data.Delete(key)
	if s.ctx != nil {
		s.ctx.markStateDirty()
	}
}

// Rotate issues a fresh session id, copies the existing session's data
// into it, and points the bound Ctx + the cookie on the in-flight
// response at the new session. Returns the new session id, or "" if
// rotation could not be performed (no bound Ctx, no Writer, no App).
//
// Use after authentication state changes (login, privilege elevation,
// password reset) so any captured pre-auth session id can no longer
// impersonate the user.
func (s *Session) Rotate() string {
	if s == nil || s.app == nil || s.ctx == nil {
		return ""
	}
	app := s.app
	old := s.data

	fresh := &session{id: genSecureID()}
	fresh.lastAccess.Store(time.Now().UnixNano())

	if old != nil {
		old.data.Range(func(k, v any) bool {
			fresh.data.Store(k.(string), v)
			return true
		})
	}

	app.sessionsMu.Lock()
	app.sessions[fresh.id] = fresh
	if old != nil {
		delete(app.sessions, old.id)
	}
	app.sessionsMu.Unlock()

	s.ctx.session.Store(fresh)
	s.data = fresh

	if w := s.ctx.Writer(); w != nil {
		http.SetCookie(w, app.sessionCookie(fresh.id))
	}
	return fresh.id
}

// RequestSession returns the [Session] cookie-resolved off r, or a
// detached Session (reads/writes no-op) if the request carries no via
// session yet. Use this from middleware that needs to read or write
// session state before any composition is rendered.
//
// Writes performed via the returned Session do not trigger a tab
// re-render — there is no Ctx attached. Use [Ctx.Session] from inside
// actions / handlers when re-render fan-out is required.
func RequestSession(r *http.Request) *Session {
	a, _ := r.Context().Value(appKey{}).(*App)
	if a == nil {
		return &Session{}
	}
	return &Session{data: a.sessionFromRequest(r), app: a}
}

// adoptSession returns the session for a cross-pod-presented sid, creating and
// registering it under the SAME id if this pod has never seen it. The re-check
// under the write lock is the LoadOrStore guard: concurrent adopters of the same
// sid converge on one *session — never a double-register that would split state.
func (a *App) adoptSession(sid string) *session {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	if sess, ok := a.sessions[sid]; ok {
		return sess
	}
	sess := &session{id: sid}
	a.sessions[sid] = sess
	return sess
}

func (a *App) getOrCreateSession(w http.ResponseWriter, r *http.Request) *session {
	now := time.Now().UnixNano()
	if c, err := r.Cookie(sessionCookieName); err == nil {
		a.sessionsMu.RLock()
		sess, ok := a.sessions[c.Value]
		a.sessionsMu.RUnlock()
		if ok {
			sess.lastAccess.Store(now)
			return sess
		}
		// Cross-pod adoption: a well-formed sid this pod never issued is a
		// session some other pod created and the client legitimately holds (the
		// 256-bit sid is the bearer credential). Adopt it under the SAME id so
		// state keyed by that sid converges here — no sticky sessions needed.
		// A malformed value is never adopted; it falls through to a fresh mint.
		if validSessionID(c.Value) {
			sess := a.adoptSession(c.Value)
			sess.lastAccess.Store(now)
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.id})
			return sess
		}
	}

	sess := &session{id: genSecureID()}
	sess.lastAccess.Store(now)

	a.sessionsMu.Lock()
	a.sessions[sess.id] = sess
	a.sessionsMu.Unlock()

	http.SetCookie(w, a.sessionCookie(sess.id))
	// Plant the cookie on the request too so sessionFromRequest in
	// downstream handlers (renderPage/handleAction/handleSSE) can find
	// the session it just created without waiting for the next round-trip.
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.id})

	return sess
}

type appKey struct{}

// sessionCookie returns the canonical via_session cookie for id with
// the app's configured Secure flag applied. Single source of truth
// shared by getOrCreateSession and Session.Rotate so the two paths
// can never drift.
//
// SameSite=Lax is chosen (over Strict) so users following an inbound
// link from another origin still see their session on the first page
// load — a Strict cookie would force them to re-auth after every
// external referral, which is hostile to e-mailed deep links. The CSRF
// surface that Lax leaves open is closed separately by the via_tab
// signal binding (see feedback_csrf_threat_model.md): every action
// POST and SSE handshake validates via_tab against the session, so a
// cross-site form submission can't reach an action even if the cookie
// rides along.
func (a *App) sessionCookie(id string) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cfg.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

// sessionFromRequest returns the session for the cookie on r, or nil
// if there's no session yet (no cookie or unknown id). The session is
// established by the withSession middleware on the first request, so
// by the time SSE/action handlers run there is always a session present.
func (a *App) sessionFromRequest(r *http.Request) *session {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	a.sessionsMu.RLock()
	defer a.sessionsMu.RUnlock()
	return a.sessions[c.Value]
}

func (a *App) removeExpiredSessions() {
	cutoff := time.Now().Add(-a.cfg.sessionTTL).UnixNano()
	a.sessionsMu.Lock()
	for id, sess := range a.sessions {
		if sess.lastAccess.Load() < cutoff {
			delete(a.sessions, id)
		}
	}
	a.sessionsMu.Unlock()
}
