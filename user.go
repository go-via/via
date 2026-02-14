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
func (h *UserHandle[T]) Get(ctx *Context) (T, bool) {
	var zero T

	if ctx == nil || ctx.v == nil || ctx.sessionID == "" {
		return zero, false
	}

	ctx.v.sessionStateMu.RLock()
	defer ctx.v.sessionStateMu.RUnlock()

	if sessionData, ok := ctx.v.sessionState[ctx.sessionID]; ok {
		if val, ok := sessionData[h.id]; ok {
			return val.(T), true
		}
	}

	return zero, false
}

// IsAuthenticated returns true if user data has been set for this session.
func (h *UserHandle[T]) IsAuthenticated(ctx *Context) bool {
	if ctx == nil || ctx.v == nil || ctx.sessionID == "" {
		return false
	}

	ctx.v.sessionStateMu.RLock()
	defer ctx.v.sessionStateMu.RUnlock()

	if sessionData, ok := ctx.v.sessionState[ctx.sessionID]; ok {
		_, exists := sessionData[h.id]
		return exists
	}

	return false
}

// Logout clears the user data from the session.
func (h *UserHandle[T]) Logout(ctx *Context) {
	if ctx == nil || ctx.v == nil || ctx.sessionID == "" {
		return
	}

	ctx.v.sessionStateMu.Lock()
	if sessionData, ok := ctx.v.sessionState[ctx.sessionID]; ok {
		delete(sessionData, h.id)
	}
	ctx.v.sessionStateMu.Unlock()

	// Mark session as invalidated to prevent reuse
	ctx.v.invalidatedSessionsMu.Lock()
	ctx.v.invalidatedSessions[ctx.sessionID] = time.Now().Unix()
	ctx.v.invalidatedSessionsMu.Unlock()
}

// SetUser sets the user data for the session.
// This is intended to be called from auth middleware.
func (h *UserHandle[T]) SetUser(ctx *Context, user T) {
	if ctx == nil || ctx.v == nil || ctx.sessionID == "" {
		return
	}

	ctx.v.sessionStateMu.Lock()
	if ctx.v.sessionState[ctx.sessionID] == nil {
		ctx.v.sessionState[ctx.sessionID] = make(map[string]any)
	}
	ctx.v.sessionState[ctx.sessionID][h.id] = user
	ctx.v.sessionStateMu.Unlock()
}
