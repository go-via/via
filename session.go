package via

import "time"

// SessionDataHandle provides access to session-scoped data.
// The data type T is defined by the application.
type SessionDataHandle[T any] struct {
	id string
}

// NewSessionDataHandle creates a new session data handle.
// Session data persists across tabs for the same session.
func NewSessionDataHandle[T any]() *SessionDataHandle[T] {
	return &SessionDataHandle[T]{
		id: genRandID(),
	}
}

// Get returns the session data and whether data exists.
// Returns (zero value, false) if no data has been set.
func (sd *SessionDataHandle[T]) Get(ctx *Context) (T, bool) {
	var zero T

	if ctx == nil || ctx.v == nil || ctx.sessionID == "" {
		return zero, false
	}

	ctx.v.sessions.stateMu.RLock()
	defer ctx.v.sessions.stateMu.RUnlock()

	if sessionData, ok := ctx.v.sessions.state[ctx.sessionID]; ok {
		if val, ok := sessionData[sd.id]; ok {
			return val.(T), true
		}
	}

	return zero, false
}

// Exists returns true if data has been set for this session.
func (sd *SessionDataHandle[T]) Exists(ctx *Context) bool {
	if ctx == nil || ctx.v == nil || ctx.sessionID == "" {
		return false
	}

	ctx.v.sessions.stateMu.RLock()
	defer ctx.v.sessions.stateMu.RUnlock()

	if sessionData, ok := ctx.v.sessions.state[ctx.sessionID]; ok {
		_, exists := sessionData[sd.id]
		return exists
	}

	return false
}

// Clear removes the data from the session.
func (sd *SessionDataHandle[T]) Clear(ctx *Context) {
	if ctx == nil || ctx.v == nil || ctx.sessionID == "" {
		return
	}

	ctx.v.sessions.stateMu.Lock()
	if sessionData, ok := ctx.v.sessions.state[ctx.sessionID]; ok {
		delete(sessionData, sd.id)
	}
	ctx.v.sessions.stateMu.Unlock()

	// Mark session as invalidated to prevent reuse
	ctx.v.sessions.invalidatedMu.Lock()
	ctx.v.sessions.invalidated[ctx.sessionID] = time.Now().Unix()
	ctx.v.sessions.invalidatedMu.Unlock()
}

// Set stores data for the session.
// This is intended to be called from middleware.
func (sd *SessionDataHandle[T]) Set(ctx *Context, data T) {
	if ctx == nil || ctx.v == nil || ctx.sessionID == "" {
		return
	}

	ctx.v.sessions.stateMu.Lock()
	if ctx.v.sessions.state[ctx.sessionID] == nil {
		ctx.v.sessions.state[ctx.sessionID] = make(map[string]any)
	}
	ctx.v.sessions.state[ctx.sessionID][sd.id] = data
	ctx.v.sessions.stateMu.Unlock()
}
