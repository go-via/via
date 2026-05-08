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
	// Plant the cookie on the request too so sessionFromRequest in
	// downstream handlers (renderPage/handleAction/handleSSE) can find
	// the session it just created without waiting for the next round-trip.
	r.AddCookie(&http.Cookie{Name: "via_session", Value: sess.id})

	return sess
}



// SessionLoad reads a value from the per-session store. Used by
// scope.User[T] to back its Get/Set with shared storage that survives
// across tabs of the same browser session.
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

// AppLoad reads a value from the per-app store. Backs scope.App[T].
func AppLoad(ctx *Ctx, key string) (any, bool) {
	if ctx == nil || ctx.app == nil {
		return nil, false
	}
	return ctx.app.appStore.Load(key)
}

// AppStore writes a value to the per-app store and marks the current
// Ctx dirty.
func AppStore(ctx *Ctx, key string, value any) {
	if ctx == nil || ctx.app == nil {
		return
	}
	ctx.app.appStore.Store(key, value)
	ctx.markStateDirty()
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
