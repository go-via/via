package via

import "time"

// TestGetSession returns a session for testing.
func (v *V) TestGetSession(sessionID string) (*session, error) {
	return v.getSession(sessionID)
}

// createSession creates a new internal session (for testing).
func (v *V) createSession(id string, sessionID string, store *store) *session {
	sess := &session{
		id:         id,
		sessionID:  sessionID,
		store:      store,
		lastAccess: time.Now().Unix(),
	}
	if sess.store == nil {
		sess.store = newStore()
	}
	sess.patchChan = make(chan patch, 10)
	v.sessions.registryMu.Lock()
	v.sessions.registry[sess.id] = sess
	v.sessions.registryMu.Unlock()
	return sess
}

// cleanupStaleSessions removes sessions that haven't been accessed recently.
func (v *V) cleanupStaleSessions() {
	if v.cfg.SessionTTL <= 0 {
		return
	}
	cutoff := time.Now().Unix() - int64(v.cfg.SessionTTL)

	v.sessions.registryMu.Lock()
	defer v.sessions.registryMu.Unlock()

	for id, sess := range v.sessions.registry {
		if sess.lastAccess < cutoff {
			delete(v.sessions.registry, id)
		}
	}

	// Also cleanup session state
	v.sessions.stateMu.Lock()
	defer v.sessions.stateMu.Unlock()
	for id, lastAccess := range v.sessions.lastAccess {
		if lastAccess < cutoff {
			delete(v.sessions.state, id)
			delete(v.sessions.lastAccess, id)
		}
	}

	// Cleanup invalidated sessions - use SessionCookieMaxAge since invalidation is tied to cookie
	cutoffInvalidated := time.Now().Unix() - int64(v.cfg.SessionCookieMaxAge)
	v.sessions.invalidatedMu.Lock()
	defer v.sessions.invalidatedMu.Unlock()
	for id, invalidatedAt := range v.sessions.invalidated {
		if invalidatedAt < cutoffInvalidated {
			delete(v.sessions.invalidated, id)
		}
	}
}

// TestGetPatchChan returns the patch channel for testing.
func (s *session) TestGetPatchChan() <-chan patch {
	return s.patchChan
}

// TestStore returns the store for testing.
func (s *session) TestStore() *store {
	return s.store
}

// TestContent returns the patch content for testing.
func (p patch) TestContent() string {
	return p.content
}

// GenRandIDForTest generates a random ID for testing.
func GenRandIDForTest() string {
	return genRandID()
}

// IsValidHexIDForTest validates a hex ID for testing.
func IsValidHexIDForTest(id string) bool {
	return isValidHexID(id)
}
