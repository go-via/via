package via

import (
	"context"
	"log"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

type session struct {
	id         string
	data       sync.Map
	lastAccess atomic.Int64
}

type sessContextKey struct{}
type sessAppContextKey struct{}

// getOrCreateSession looks up the session by cookie or creates a new one.
func (a *App) getOrCreateSession(w http.ResponseWriter, r *http.Request) *session {
	now := time.Now().UnixNano()
	if c, err := r.Cookie("via_session"); err == nil {
		a.sessionsMu.RLock()
		sess, ok := a.sessions[c.Value]
		a.sessionsMu.RUnlock()
		if ok {
			sess.lastAccess.Store(now)
			return sess
		}
	}

	sess := &session{id: genRandID()}
	sess.lastAccess.Store(now)

	a.sessionsMu.Lock()
	a.sessions[sess.id] = sess
	a.sessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "via_session",
		Value:    sess.id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	return sess
}

func (a *App) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := a.getOrCreateSession(w, r)
		ctx := context.WithValue(r.Context(), sessContextKey{}, sess)
		ctx = context.WithValue(ctx, sessAppContextKey{}, a)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func sessionFromRequest(r *http.Request) *session {
	if s, ok := r.Context().Value(sessContextKey{}).(*session); ok {
		return s
	}
	return nil
}

// GetSess reads a typed value from the session. Accepts *Ctx or *http.Request.
// Returns the zero value of T if not set or if the argument type is unrecognized.
func GetSess[T any](from any) T {
	var zero T
	var sess *session
	switch v := from.(type) {
	case *Ctx:
		if v != nil {
			sess = v.session
		}
	case *http.Request:
		sess = sessionFromRequest(v)
	default:
		log.Printf("[error] via: GetSess called with unsupported type %T", from)
		return zero
	}
	if sess == nil {
		return zero
	}
	key := reflect.TypeFor[T]()
	val, ok := sess.data.Load(key)
	if !ok {
		return zero
	}
	return val.(T)
}

// SetSess stores a typed value in the session. No-op with warning if w or r is nil.
func SetSess[T any](w http.ResponseWriter, r *http.Request, val T) {
	if w == nil || r == nil {
		log.Printf("[warn] via: SetSess called with nil writer or request")
		return
	}
	sess := sessionFromRequest(r)
	if sess == nil {
		log.Printf("[warn] via: SetSess called without session in request context")
		return
	}
	key := reflect.TypeFor[T]()
	sess.data.Store(key, val)
}

func (a *App) sweepExpiredSessions() {
	interval := a.cfg.sessionTTL / 2
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopSweep:
			return
		case <-ticker.C:
			a.removeExpiredSessions()
		}
	}
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

// ClearSess destroys the session and expires the cookie. No-op with warning if w or r is nil.
func ClearSess(w http.ResponseWriter, r *http.Request) {
	if w == nil || r == nil {
		log.Printf("[warn] via: ClearSess called with nil writer or request")
		return
	}
	sess := sessionFromRequest(r)
	if sess == nil {
		return
	}
	if a, ok := r.Context().Value(sessAppContextKey{}).(*App); ok {
		a.sessionsMu.Lock()
		delete(a.sessions, sess.id)
		a.sessionsMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "via_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
