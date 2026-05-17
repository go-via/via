package via

import (
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

// sessionCookieName is the name of the HTTP cookie via uses to identify
// a browser session across requests. Centralized here so set/read/delete
// paths can never drift.
const sessionCookieName = "via_session"

type session struct {
	id         string
	data       sync.Map
	lastAccess atomic.Int64
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

// PutSess stores a typed value in the session, keyed by the type name.
// Use it to attach "the logged-in user" or any struct that is one-per-
// session. Marks the current Ctx dirty so the page re-renders.
//
//	type User struct { Email, Name string }
//	via.PutSess(ctx, User{Email: "alice@example.com"})
func PutSess[T any](ctx *Ctx, v T) {
	if ctx == nil {
		return
	}
	SessionStore(ctx, sessionTypeKey[T](), v)
}

// GetSess reads the typed value stored with PutSess, returning the zero
// value of T and false if nothing matches. The src argument may be a
// *Ctx or an *http.Request — the latter form lets middleware check the
// session before any composition is rendered.
//
//	func requireAuth(w http.ResponseWriter, r *http.Request, next http.Handler) {
//	    if u, ok := via.GetSess[User](r); !ok || u.Email == "" {
//	        http.Redirect(w, r, "/login", 303)
//	        return
//	    }
//	    next.ServeHTTP(w, r)
//	}
func GetSess[T any](src any) (T, bool) {
	var zero T
	switch s := src.(type) {
	case *Ctx:
		v, ok := SessionLoad(s, sessionTypeKey[T]())
		if !ok {
			return zero, false
		}
		t, ok := v.(T)
		return t, ok
	case *http.Request:
		sess := sessionFromRequestCtx(s)
		if sess == nil {
			return zero, false
		}
		v, ok := sess.data.Load(sessionTypeKey[T]())
		if !ok {
			return zero, false
		}
		t, ok := v.(T)
		return t, ok
	}
	return zero, false
}

// ClearSess removes the value stored under T's key from the session.
func ClearSess[T any](src any) {
	switch s := src.(type) {
	case *Ctx:
		if s != nil && s.session != nil {
			s.session.data.Delete(sessionTypeKey[T]())
			s.markStateDirty()
		}
	case *http.Request:
		sess := sessionFromRequestCtx(s)
		if sess != nil {
			sess.data.Delete(sessionTypeKey[T]())
		}
	}
}

// sessionTypeKeyCache memoises sessionTypeKey results so PutSess/GetSess/
// ClearSess hot paths avoid repeated string concatenation. Keyed by
// reflect.Type which is canonical and comparable.
var sessionTypeKeyCache sync.Map // map[reflect.Type]string

// sessionTypeKey returns a stable string key for a Go type used as a
// typed-session value. We use the reflect type's full string ("pkg.Name")
// so distinct types in different packages don't collide.
func sessionTypeKey[T any]() string {
	var zero T
	rt := reflect.TypeOf(&zero).Elem()
	if v, ok := sessionTypeKeyCache.Load(rt); ok {
		return v.(string)
	}
	key := "type:" + rt.String()
	sessionTypeKeyCache.Store(rt, key)
	return key
}

// sessionFromRequestCtx looks up the session associated with the request's
// cookie, but only if the request originated from this app's session
// middleware. Resolved via the App pointer stamped into each request's
// context (see withSession).
func sessionFromRequestCtx(r *http.Request) *session {
	if r == nil {
		return nil
	}
	a, _ := r.Context().Value(appKey{}).(*App)
	if a == nil {
		return nil
	}
	return a.sessionFromRequest(r)
}

type appKey struct{}

// SessionLoad reads a value from the per-session store. Used by
// scope.User[T] to back its Get/Set with shared storage that survives
// across tabs of the same browser session.
//
// Deprecated: scope-package integration hook. End users should access
// session state through scope.User[T] rather than calling this directly.
func SessionLoad(ctx *Ctx, key string) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	if ctx.session != nil {
		return ctx.session.data.Load(key)
	}
	return ctx.localScope.Load(key)
}

// SessionStore writes a value to the per-session store and marks the
// current Ctx dirty so the page re-renders with the new value. If ctx
// has no session (test path that bypassed the session middleware), the
// value is held on the ctx itself so within-request reads still work.
//
// Deprecated: scope-package integration hook. End users should access
// session state through scope.User[T] rather than calling this directly.
func SessionStore(ctx *Ctx, key string, value any) {
	if ctx == nil {
		return
	}
	if ctx.session != nil {
		ctx.session.data.Store(key, value)
	} else {
		// ephemeral fallback so Get(ctx) within the same request returns v
		ctx.localScope.Store(key, value)
	}
	ctx.markStateDirty()
}

// RotateSession issues a fresh session id, copies the existing session's
// data into it, and points the current Ctx + the cookie on the in-flight
// response at the new session. Use after authentication state changes
// (login, privilege elevation, password reset) so any captured pre-auth
// session id can no longer impersonate the user.
//
// Must be called from inside an action handler — Writer() must be non-nil.
// Returns the new session id, or "" if the rotation could not be performed.
func RotateSession(ctx *Ctx) string {
	if ctx == nil || ctx.app == nil {
		return ""
	}
	old := ctx.session
	app := ctx.app

	fresh := &session{id: genSecureID()}
	fresh.lastAccess.Store(time.Now().UnixNano())

	if old != nil {
		old.data.Range(func(k, v any) bool {
			fresh.data.Store(k, v)
			return true
		})
	}

	app.sessionsMu.Lock()
	app.sessions[fresh.id] = fresh
	if old != nil {
		delete(app.sessions, old.id)
	}
	app.sessionsMu.Unlock()

	ctx.session = fresh

	if w := ctx.Writer(); w != nil {
		http.SetCookie(w, app.sessionCookie(fresh.id))
	}
	return fresh.id
}

// sessionCookie returns the canonical via_session cookie for id with the
// app's configured Secure flag applied. Single source of truth shared by
// getOrCreateSession and RotateSession so the two paths can never drift.
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

// AppLoad reads a value from the per-app store. Backs scope.App[T].
// When ctx has no App attached (test path that bypassed New), falls
// back to the ctx's local scope so a paired AppStore/AppLoad on the
// same Ctx still round-trips — mirrors SessionLoad's contract.
//
// Deprecated: scope-package integration hook. End users should access
// app-wide state through scope.App[T] rather than calling this directly.
func AppLoad(ctx *Ctx, key string) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	if ctx.app != nil {
		return ctx.app.appStore.Load(key)
	}
	return ctx.localScope.Load(key)
}

// AppStore writes a value to the per-app store and marks the current
// Ctx dirty. When ctx has no App attached (test path that bypassed
// New), the value is held on the ctx's local scope so within-request
// reads still work — mirrors SessionStore's contract.
//
// Deprecated: scope-package integration hook. End users should access
// app-wide state through scope.App[T] rather than calling this directly.
func AppStore(ctx *Ctx, key string, value any) {
	if ctx == nil {
		return
	}
	if ctx.app != nil {
		ctx.app.appStore.Store(key, value)
	} else {
		// ephemeral fallback so AppLoad on the same Ctx returns v
		ctx.localScope.Store(key, value)
	}
	ctx.markStateDirty()
}

// sessionFromRequest returns the session for the cookie on r, or nil if
// there's no session yet (no cookie or unknown id). The session is
// established by the withSession middleware on the first request, so by
// the time SSE/action handlers run there is always a session present.
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
