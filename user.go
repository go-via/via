package via

import "time"

// UserHandle provides access to session-scoped user data.
// The user type T is defined by the application.
type UserHandle[T any] struct {
	id string
}

// NewUserHandle creates a new user data handle.
// User data is always session-scoped (persists across tabs for same user).
func NewUserHandle[T any]() *UserHandle[T] {
	return &UserHandle[T]{
		id: genRandID(),
	}
}

// Get returns the user data and whether the user is authenticated.
// Returns (zero value, false) if no user data has been set.
func (h *UserHandle[T]) Get(s *Session) (T, bool) {
	var zero T

	if s == nil || s.v == nil || s.sessionID == "" {
		return zero, false
	}

	s.v.sessionStateMu.RLock()
	defer s.v.sessionStateMu.RUnlock()

	if sessionData, ok := s.v.sessionState[s.sessionID]; ok {
		if val, ok := sessionData[h.id]; ok {
			return val.(T), true
		}
	}

	return zero, false
}

// IsAuthenticated returns true if user data has been set for this session.
func (h *UserHandle[T]) IsAuthenticated(s *Session) bool {
	if s == nil || s.v == nil || s.sessionID == "" {
		return false
	}

	s.v.sessionStateMu.RLock()
	defer s.v.sessionStateMu.RUnlock()

	if sessionData, ok := s.v.sessionState[s.sessionID]; ok {
		_, exists := sessionData[h.id]
		return exists
	}

	return false
}

// Logout clears the user data from the session.
func (h *UserHandle[T]) Logout(s *Session) {
	if s == nil || s.v == nil || s.sessionID == "" {
		return
	}

	s.v.sessionStateMu.Lock()
	if sessionData, ok := s.v.sessionState[s.sessionID]; ok {
		delete(sessionData, h.id)
	}
	s.v.sessionStateMu.Unlock()

	// Mark session as invalidated to prevent reuse
	s.v.invalidatedSessionsMu.Lock()
	s.v.invalidatedSessions[s.sessionID] = time.Now().Unix()
	s.v.invalidatedSessionsMu.Unlock()
}

// SetUser sets the user data for the session.
// This is intended to be called from auth middleware.
func (h *UserHandle[T]) SetUser(s *Session, user T) {
	if s == nil || s.v == nil || s.sessionID == "" {
		return
	}

	s.v.sessionStateMu.Lock()
	if s.v.sessionState[s.sessionID] == nil {
		s.v.sessionState[s.sessionID] = make(map[string]any)
	}
	s.v.sessionState[s.sessionID][h.id] = user
	s.v.sessionStateMu.Unlock()
}

