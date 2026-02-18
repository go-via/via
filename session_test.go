package via

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func extractTabIDFromBody(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, "&#39;via-ctx&#39;:&#39;")
	if start == -1 {
		t.Fatal("via-ctx not found in HTML")
	}
	start += len("&#39;via-ctx&#39;:&#39;")
	end := strings.Index(html[start:], "&#39;")
	return html[start : start+end]
}

// Test that sessions persist state across requests
func TestSession_PersistsState(t *testing.T) {
	v := New()
	var stateHandle *StateHandle[int]

	v.Page("/", func(c *Composition) {
		stateHandle = State(c, 0)

		increment := Action(c, func(ctx *Context) {
			val := stateHandle.Get(ctx) + 1
			stateHandle.Set(ctx, val)
		})

		c.View(func(ctx *Context) h.H {
			return h.Div(
				h.P(h.Textf("Count: %d", stateHandle.Get(ctx))),
				h.Button(h.Text("+"), increment.OnClick()),
			)
		})
	})

	// Create a session store (per-tab)
	session := v.createSession("tab1", "session1", nil)
	assert.NotNil(t, session)
	assert.NotNil(t, session.store)

	// Initial state should be 0
	val, ok := session.store.state[stateHandle.id]
	assert.False(t, ok) // No value yet, uses initial
	sc := &Context{s: session.store, mode: sessionModeAction, warn: func(string, ...any) {}}
	assert.Equal(t, 0, stateHandle.Get(sc))

	// Modify state
	stateHandle.Set(sc, 42)

	// State should persist
	val = session.store.state[stateHandle.id]
	assert.Equal(t, 42, val)
	assert.Equal(t, 42, stateHandle.Get(sc))
}

// Test that each session has its own state
func TestSession_IsolatedState(t *testing.T) {
	v := New()

	session1 := v.createSession("tab1", "session1", nil)
	session2 := v.createSession("tab2", "session1", nil)

	// Each session has its own store (different pointers)
	assert.NotSame(t, session1.store, session2.store)

	// Modify session1
	session1.store.state["key"] = "value1"

	// Session2 should be unaffected
	_, ok := session2.store.state["key"]
	assert.False(t, ok)
}

// Test Session.Set in view mode warns
func TestSession_SetInViewModeWarns(t *testing.T) {
	var warned bool
	var warnMsg string
	warnFn := func(format string, args ...any) {
		warned = true
		warnMsg = format
	}

	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Context)),
		signals: []signalRegistration{},
		states:  []stateRegistration{},
	}

	ctx := &Context{
		s:    newStore(),
		mode: sessionModeView,
		warn: warnFn,
	}
	state := State(c, 0)

	state.Set(ctx, 42)

	assert.True(t, warned, "Expected warning when Set called in view mode")
	assert.Contains(t, warnMsg, "State.Set()")

	// Value should NOT be set
	got := state.Get(ctx)
	assert.Equal(t, 0, got, "Value should remain at initial (not mutated in view mode)")
}

// Test Session.Sync in view mode warns
func TestSession_SyncInViewModeWarns(t *testing.T) {
	var warned bool
	var warnMsg string
	warnFn := func(format string, args ...any) {
		warned = true
		warnMsg = format
	}

	s := &Context{
		s:    newStore(),
		mode: sessionModeView,
		warn: warnFn,
	}

	s.Sync()

	assert.True(t, warned, "Expected warning when Sync called in view mode")
	assert.Contains(t, warnMsg, "Sync()")
}

// Test Session.SyncFragment in view mode warns
func TestSession_SyncFragmentInViewModeWarns(t *testing.T) {
	var warned bool
	var warnMsg string
	warnFn := func(format string, args ...any) {
		warned = true
		warnMsg = format
	}

	s := &Context{
		s:    newStore(),
		mode: sessionModeView,
		warn: warnFn,
	}

	s.SyncFragment(h.Div(h.Text("test")))

	assert.True(t, warned, "Expected warning when SyncFragment called in view mode")
	assert.Contains(t, warnMsg, "SyncFragment()")
}

func TestSession_SeedsInitialState(t *testing.T) {
	v := New()
	var stateHandle *StateHandle[int]

	v.Page("/", func(c *Composition) {
		stateHandle = State(c, 42)
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Textf("Count: %d", stateHandle.Get(ctx)))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	tabID := extractTabIDFromBody(t, w.Body.String())
	sess, err := v.TestGetSession(tabID)
	if err != nil {
		t.Fatal(err)
	}

	// The session store should have the initial state value pre-seeded
	val, ok := sess.TestStore().state[stateHandle.TestID()]
	assert.True(t, ok, "initial state should be seeded in session store")
	assert.Equal(t, 42, val)
}

func TestSessionClose_RemovesSession(t *testing.T) {
	v := New()

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	// Load page to create session (tabID is in HTML via-ctx signal)
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	tabID := extractTabIDFromBody(t, w1.Body.String())

	// Verify session exists
	_, err := v.TestGetSession(tabID)
	assert.NoError(t, err, "Session should exist after page load")

	// Close session with beacon (sends tabID)
	req2 := httptest.NewRequest("POST", "/_session/close", bytes.NewBufferString(tabID))
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusNoContent, w2.Code)

	// Verify session was removed
	_, err = v.TestGetSession(tabID)
	assert.Error(t, err, "Session should be removed after close")
}

// Test that stale sessions are cleaned up based on TTL
func TestSession_TTLCleanup(t *testing.T) {
	v := New()
	v.cfg.SessionTTL = 2 // 2 seconds for testing

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	// Create a session
	session := v.createSession("test-tab", "session1", nil)
	assert.NotNil(t, session)

	// Verify session exists
	_, err := v.TestGetSession("test-tab")
	assert.NoError(t, err, "Session should exist after creation")

	// Manually set lastAccess to be older than TTL
	session.lastAccess = session.lastAccess - 10 // 10 seconds ago

	// Run cleanup
	v.cleanupStaleSessions()

	// Verify session was removed
	_, err = v.TestGetSession("test-tab")
	assert.Error(t, err, "Session should be removed after TTL cleanup")
}

// Test that active sessions are not cleaned up
func TestSession_ActiveNotCleaned(t *testing.T) {
	v := New()
	v.cfg.SessionTTL = 2 // 2 seconds for testing

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	// Create a session
	session := v.createSession("active-tab", "comp1", nil)
	assert.NotNil(t, session)

	// Run cleanup immediately (session was just created)
	v.cleanupStaleSessions()

	// Verify session still exists
	_, err := v.TestGetSession("active-tab")
	assert.NoError(t, err, "Active session should not be cleaned up")
}

// Test session cookie is set on first request
func TestSession_CookieSetOnFirstRequest(t *testing.T) {
	v := New()

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	// Check for session cookie
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "via_sid" {
			sessionCookie = c
			break
		}
	}

	assert.NotNil(t, sessionCookie, "Session cookie should be set")
	assert.NotEmpty(t, sessionCookie.Value, "Session cookie should have value")
}

// Test session cookie is read on subsequent requests
func TestSession_CookieReadOnSubsequentRequest(t *testing.T) {
	v := New()

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	// First request - get session cookie
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	sessionCookie := w1.Result().Cookies()[0]

	// Second request with cookie
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(sessionCookie)
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	// Should use same session ID
	assert.Equal(t, http.StatusOK, w2.Code)
}

// Test session-scoped state persists across tabs
func TestState_SessionScope_PersistsAcrossRequests(t *testing.T) {
	v := New()
	var sessionState *StateHandle[string]

	v.Page("/", func(c *Composition) {
		sessionState = State(c, "initial", WithScope(ScopeSession))

		setValue := Action(c, func(ctx *Context) {
			sessionState.Set(ctx, "updated")
		})

		c.View(func(ctx *Context) h.H {
			return h.Div(
				h.P(h.Textf("Value: %s", sessionState.Get(ctx))),
				h.Button(h.Text("Set"), setValue.OnClick()),
			)
		})
	})

	// First request - get session cookie
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	cookies := w1.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "via_sid" {
			sessionCookie = c
			break
		}
	}
	assert.NotNil(t, sessionCookie)

	// Create new tab session with session ID from cookie
	s1 := NewContext(v)
	s1.ss = &session{id: "tab1"}
	s1.sessionID = sessionCookie.Value

	// Set session-scoped value
	sessionState.Set(s1, "updated")

	// Verify persisted
	assert.Equal(t, "updated", sessionState.Get(s1))

	// Create another tab with same session ID
	s2 := NewContext(v)
	s2.ss = &session{id: "tab2"}
	s2.sessionID = sessionCookie.Value

	// Should see same value
	assert.Equal(t, "updated", sessionState.Get(s2))
}

// Test that session-scoped state is cleaned up after TTL
func TestSessionState_CleanupAfterTTL(t *testing.T) {
	v := New()
	v.cfg.SessionTTL = 2 // 2 seconds for testing

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	// Simulate session access tracking
	sessionID := "test-session-id"
	v.sessionLastAccessMu.Lock()
	v.sessionLastAccess[sessionID] = time.Now().Unix() - 10 // 10 seconds ago
	v.sessionLastAccessMu.Unlock()

	// Add some session-scoped state
	v.sessionStateMu.Lock()
	v.sessionState[sessionID] = map[string]any{"key": "value"}
	v.sessionStateMu.Unlock()

	// Verify state exists
	v.sessionStateMu.RLock()
	_, exists := v.sessionState[sessionID]
	v.sessionStateMu.RUnlock()
	assert.True(t, exists, "Session state should exist before cleanup")

	// Run cleanup
	v.cleanupStaleSessions()

	// Verify state was cleaned up
	v.sessionStateMu.RLock()
	_, exists = v.sessionState[sessionID]
	v.sessionStateMu.RUnlock()
	assert.False(t, exists, "Session state should be cleaned up after TTL")

	// Verify last access tracking was cleaned up
	v.sessionLastAccessMu.RLock()
	_, exists = v.sessionLastAccess[sessionID]
	v.sessionLastAccessMu.RUnlock()
	assert.False(t, exists, "Session last access should be cleaned up after TTL")
}

// Test that logout invalidates the session
func TestSession_InvalidatedOnLogout(t *testing.T) {
	v := New()
	var userHandle *UserHandle[TestUser]

	v.Page("/", func(c *Composition) {
		userHandle = NewUserHandle[TestUser]()
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	// First request - get session cookie
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	cookies := w1.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "via_sid" {
			sessionCookie = c
			break
		}
	}
	assert.NotNil(t, sessionCookie)

	// Set user data
	ctx := NewContext(v)
	ctx.sessionID = sessionCookie.Value
	userHandle.SetUser(ctx, TestUser{ID: "123", Name: "Alice"})

	// Logout
	userHandle.Logout(ctx)

	// Verify session is in invalidation list
	v.invalidatedSessionsMu.RLock()
	_, invalidated := v.invalidatedSessions[sessionCookie.Value]
	v.invalidatedSessionsMu.RUnlock()
	assert.True(t, invalidated, "Session should be in invalidation list after logout")
}

// Test that invalidated sessions are rejected and new session created
func TestSession_InvalidatedSessionRejected(t *testing.T) {
	v := New()

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	// First request - get session cookie
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	sessionCookie := w1.Result().Cookies()[0]
	originalSessionID := sessionCookie.Value

	// Invalidate the session
	v.invalidatedSessionsMu.Lock()
	v.invalidatedSessions[originalSessionID] = time.Now().Unix()
	v.invalidatedSessionsMu.Unlock()

	// Second request with invalidated cookie
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(sessionCookie)
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	// Should get a new session cookie
	var newCookie *http.Cookie
	for _, c := range w2.Result().Cookies() {
		if c.Name == "via_sid" {
			newCookie = c
			break
		}
	}

	assert.NotNil(t, newCookie, "Should get new session cookie")
	assert.NotEqual(t, originalSessionID, newCookie.Value, "New session ID should be different from invalidated one")
}

// Test that invalidated sessions are cleaned up after cookie MaxAge
func TestSession_InvalidatedSessionsCleanup(t *testing.T) {
	v := New()
	v.cfg.SessionCookieMaxAge = 2 // 2 seconds for testing

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	sessionID := "test-invalidated-session"

	// Add to invalidation list with old timestamp
	v.invalidatedSessionsMu.Lock()
	v.invalidatedSessions[sessionID] = time.Now().Unix() - 10 // 10 seconds ago
	v.invalidatedSessionsMu.Unlock()

	// Verify exists
	v.invalidatedSessionsMu.RLock()
	_, exists := v.invalidatedSessions[sessionID]
	v.invalidatedSessionsMu.RUnlock()
	assert.True(t, exists, "Invalidated session should exist before cleanup")

	// Run cleanup
	v.cleanupStaleSessions()

	// Verify cleaned up
	v.invalidatedSessionsMu.RLock()
	_, exists = v.invalidatedSessions[sessionID]
	v.invalidatedSessionsMu.RUnlock()
	assert.False(t, exists, "Old invalidated session should be cleaned up")
}
