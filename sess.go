package via

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type session struct {
	id         string
	data       sync.Map
	lastAccess atomic.Int64
}

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

	sess := &session{id: genSecureID()}
	sess.lastAccess.Store(now)

	a.sessionsMu.Lock()
	a.sessions[sess.id] = sess
	a.sessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "via_session",
		Value:    sess.id,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cfg.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})

	return sess
}



// sessionFromRequest returns the session for the cookie on r, or nil if
// there's no session yet (no cookie or unknown id). The session is
// established by the withSession middleware on the first request, so by
// the time SSE/action handlers run there is always a session present.
func (a *App) sessionFromRequest(r *http.Request) *session {
	c, err := r.Cookie("via_session")
	if err != nil {
		return nil
	}
	a.sessionsMu.RLock()
	defer a.sessionsMu.RUnlock()
	return a.sessions[c.Value]
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
