# Implementation Plan: Address Code Review Issues

## Phase 1: Fix User Logout Scope
**File:** `user.go:56-69`

**Problem:** `Logout()` deletes entire session instead of just user data

**Change:**
```go
// BEFORE:
s.v.sessionStateMu.Lock()
delete(s.v.sessionState, s.sessionID)  // Deletes ALL session data
s.v.sessionStateMu.Unlock()

// AFTER:
s.v.sessionStateMu.Lock()
if sessionData, ok := s.v.sessionState[s.sessionID]; ok {
    delete(sessionData, h.id)  // Only delete user data
}
s.v.sessionStateMu.Unlock()
```

## Phase 2: Session State Cleanup
**Files:** `via.go`, `cfg.go`

**Problem:** `sessionState` map grows indefinitely - memory leak

**Changes:**
1. Add `sessionLastAccess map[string]int64` to V struct
2. Update access time in `newPageHTTPHandler` when cookie is used
3. In `cleanupStaleSessions()`, also clean `sessionState` entries older than TTL

**New fields in V struct:**
```go
sessionLastAccess    map[string]int64
sessionLastAccessMu  sync.RWMutex
```

**Cleanup logic:**
```go
func (v *V) cleanupStaleSessions() {
    // Existing tab session cleanup...
    
    // NEW: Cleanup session-scoped data
    cutoff := time.Now().Unix() - int64(v.cfg.SessionTTL)
    v.sessionLastAccessMu.Lock()
    for sessionID, lastAccess := range v.sessionLastAccess {
        if lastAccess < cutoff {
            v.sessionStateMu.Lock()
            delete(v.sessionState, sessionID)
            v.sessionStateMu.Unlock()
            delete(v.sessionLastAccess, sessionID)
        }
    }
    v.sessionLastAccessMu.Unlock()
}
```

## Phase 3: Session Invalidation List
**Files:** `via.go`, `user.go`

**Problem:** Logged-out sessions remain technically valid

**Changes:**
1. Add `invalidatedSessions map[string]int64` to V struct (sessionID → timestamp)
2. In `user.go Logout()`, add session to invalidation list
3. In `newPageHTTPHandler()`, check if session is invalidated before accepting
4. Cleanup invalidated entries after cookie MaxAge expires

**New fields in V struct:**
```go
invalidatedSessions    map[string]int64
invalidatedSessionsMu  sync.RWMutex
```

**In Logout():**
```go
s.v.invalidatedSessionsMu.Lock()
if s.v.invalidatedSessions == nil {
    s.v.invalidatedSessions = make(map[string]int64)
}
s.v.invalidatedSessions[s.sessionID] = time.Now().Unix()
s.v.invalidatedSessionsMu.Unlock()
```

**In newPageHTTPHandler():**
```go
// Check for invalidated session
v.invalidatedSessionsMu.RLock()
if _, invalidated := v.invalidatedSessions[sessionID]; invalidated {
    v.invalidatedSessionsMu.RUnlock()
    // Generate new session
    sessionID = genRandID()
    // ... set new cookie
} else {
    v.invalidatedSessionsMu.RUnlock()
}
```

**Cleanup in cleanupStaleSessions():**
```go
// Cleanup old invalidated sessions (older than cookie MaxAge)
cookieExpiryCutoff := time.Now().Unix() - int64(v.cfg.SessionCookieMaxAge)
v.invalidatedSessionsMu.Lock()
for sessionID, invalidatedAt := range v.invalidatedSessions {
    if invalidatedAt < cookieExpiryCutoff {
        delete(v.invalidatedSessions, sessionID)
    }
}
v.invalidatedSessionsMu.Unlock()
```

## Test Updates Needed

1. **user_test.go:** Update logout test to verify only user data deleted
2. **via_test.go or session_test.go:** Add tests for:
   - Session state cleanup after TTL
   - Invalidated session rejection
   - Cleanup of invalidated entries

## Files Modified

- `user.go` - Phase 1, Phase 3 additions
- `via.go` - Phase 2, Phase 3 infrastructure
- `cfg.go` - (already has SessionTTL, SessionCookieMaxAge)
- `user_test.go` - Update existing tests
- `session_test.go` - New tests for cleanup

