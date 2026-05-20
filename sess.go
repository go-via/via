package via

import (
	"net/http"
	"sync/atomic"
	"time"
)

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
// to subscribed tabs on Store; one obtained via [RequestSession] (in a
// middleware, before a Ctx exists) is cookie-only and does not trigger
// re-render.
//
// Typed access lives in the via/sess subpackage — most code reaches
// for sess.Get[T] / sess.Put[T] / sess.Clear[T] rather than this type
// directly.
type Session struct {
	data *session
	ctx  *Ctx
	app  *App
}

// Load reads the value stored under key, or nil/false if absent or if
// the Session is detached (no underlying session record).
func (s *Session) Load(key string) (any, bool) {
	if s == nil || s.data == nil {
		return nil, false
	}
	return s.data.data.Load(key)
}

// Store writes value under key. When the Session is bound to a Ctx,
// also marks the page dirty so the view re-renders and fans the write
// out to every other live tab on the same session subscribed to key.
func (s *Session) Store(key string, value any) {
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

// Delete removes the value stored under key. When the Session is bound
// to a Ctx, also marks the page dirty so the view re-renders.
func (s *Session) Delete(key string) {
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

	s.ctx.session = fresh
	s.data = fresh

	if w := s.ctx.Writer(); w != nil {
		http.SetCookie(w, app.sessionCookie(fresh.id))
	}
	return fresh.id
}

// RequestSession returns the [Session] cookie-resolved off r, or a
// detached Session (Load/Store no-op) if the request carries no via
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
