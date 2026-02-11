package via

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

// Test that sessions persist state across requests
func TestSession_PersistsState(t *testing.T) {
	v := New()
	var stateHandle *StateHandle[int]
	var sessionID string

	v.Page("/", func(c *Composition) {
		stateHandle = State(0)
		sessionID = c.id // Capture session ID

		increment := Action(c, func(s *Session) {
			val := stateHandle.Get(s) + 1
			stateHandle.Set(s, val)
		})

		c.View(func(s *Session) h.H {
			return h.Div(
				h.P(h.Textf("Count: %d", stateHandle.Get(s))),
				h.Button(h.Text("+"), increment.OnClick()),
			)
		})
	})

	// Create a session store
	session := v.getOrCreateSession(sessionID)
	assert.NotNil(t, session)
	assert.NotNil(t, session.store)

	// Initial state should be 0
	val, ok := session.store.state[stateHandle.id]
	assert.False(t, ok) // No value yet, uses initial
	sc := &Session{s: session.store, mode: sessionModeAction, warn: func(string, ...any) {}}
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

	session1 := v.getOrCreateSession("session1")
	session2 := v.getOrCreateSession("session2")

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

	s := &Session{
		s:    newStore(),
		mode: sessionModeView,
		warn: warnFn,
	}
	state := State(0)

	state.Set(s, 42)

	assert.True(t, warned, "Expected warning when Set called in view mode")
	assert.Contains(t, warnMsg, "State.Set()")

	// Value should NOT be set
	got := state.Get(s)
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

	s := &Session{
		s:    newStore(),
		mode: sessionModeView,
		warn: warnFn,
	}

	s.Sync()

	assert.True(t, warned, "Expected warning when Sync called in view mode")
	assert.Contains(t, warnMsg, "Sync()")
}

func TestSessionClose_RemovesSession(t *testing.T) {
	v := New()
	var sessionID string

	v.Page("/", func(c *Composition) {
		sessionID = c.id
		c.View(func(s *Session) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	// Load page to create session
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	// Verify session exists
	_, err := v.TestGetSession(sessionID)
	assert.NoError(t, err, "Session should exist after page load")

	// Close session with beacon
	req2 := httptest.NewRequest("POST", "/_session/close", bytes.NewBufferString(sessionID))
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusNoContent, w2.Code)

	// Verify session was removed
	_, err = v.TestGetSession(sessionID)
	assert.Error(t, err, "Session should be removed after close")
}
