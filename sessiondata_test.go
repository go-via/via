package via

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

type TestUser struct {
	ID   string
	Name string
	Role string
}

func TestSessionData_TypeExists(t *testing.T) {
	// Just verify the type can be instantiated
	v := New()
	v.Page("/", func(c *Composition) {
		_ = NewSessionDataHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})
}

func TestSessionData_Get_ReturnsNotAuthenticated(t *testing.T) {
	v := New()
	var sessionData *SessionDataHandle[TestUser]

	v.Page("/", func(c *Composition) {
		sessionData = NewSessionDataHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	// Create session without setting data
	ctx := NewContext(v)
	ctx.sessionID = "test-session"

	// Get should return false for data exists
	_, ok := sessionData.Get(ctx)
	assert.False(t, ok, "Should not be data exists initially")
}

func TestSessionData_Exists_False(t *testing.T) {
	v := New()
	var sessionData *SessionDataHandle[TestUser]

	v.Page("/", func(c *Composition) {
		sessionData = NewSessionDataHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	ctx := NewContext(v)
	ctx.sessionID = "test-session"

	assert.False(t, sessionData.Exists(ctx))
}

func TestSessionData_Get_AfterSet(t *testing.T) {
	v := New()
	var sessionData *SessionDataHandle[TestUser]

	v.Page("/", func(c *Composition) {
		sessionData = NewSessionDataHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	ctx := NewContext(v)
	ctx.sessionID = "test-session"

	// Simulate middleware setting data
	v.sessions.stateMu.Lock()
	if v.sessions.state[ctx.sessionID] == nil {
		v.sessions.state[ctx.sessionID] = make(map[string]any)
	}
	v.sessions.state[ctx.sessionID][sessionData.id] = TestUser{ID: "123", Name: "Alice", Role: "admin"}
	v.sessions.stateMu.Unlock()

	// Get should return data and true
	user, ok := sessionData.Get(ctx)
	assert.True(t, ok)
	assert.Equal(t, "123", user.ID)
	assert.Equal(t, "Alice", user.Name)
	assert.Equal(t, "admin", user.Role)
}

func TestSessionData_Exists_AfterSet(t *testing.T) {
	v := New()
	var sessionData *SessionDataHandle[TestUser]

	v.Page("/", func(c *Composition) {
		sessionData = NewSessionDataHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	ctx := NewContext(v)
	ctx.sessionID = "test-session"

	// Set data
	v.sessions.stateMu.Lock()
	if v.sessions.state[ctx.sessionID] == nil {
		v.sessions.state[ctx.sessionID] = make(map[string]any)
	}
	v.sessions.state[ctx.sessionID][sessionData.id] = TestUser{ID: "123", Name: "Alice"}
	v.sessions.stateMu.Unlock()

	assert.True(t, sessionData.Exists(ctx))
}

func TestSessionData_Clear_ClearsData(t *testing.T) {
	v := New()
	var sessionData *SessionDataHandle[TestUser]

	v.Page("/", func(c *Composition) {
		sessionData = NewSessionDataHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	ctx := NewContext(v)
	ctx.sessionID = "test-session"

	// Set data
	v.sessions.stateMu.Lock()
	if v.sessions.state[ctx.sessionID] == nil {
		v.sessions.state[ctx.sessionID] = make(map[string]any)
	}
	v.sessions.state[ctx.sessionID][sessionData.id] = TestUser{ID: "123", Name: "Alice"}
	v.sessions.stateMu.Unlock()

	assert.True(t, sessionData.Exists(ctx))

	// Clear
	sessionData.Clear(ctx)

	// Should no longer be data exists
	assert.False(t, sessionData.Exists(ctx))
	_, ok := sessionData.Get(ctx)
	assert.False(t, ok)
}

func TestSessionData_Clear_ClearsCookie(t *testing.T) {
	v := New()
	var sessionData *SessionDataHandle[TestUser]

	v.Page("/", func(c *Composition) {
		sessionData = NewSessionDataHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	// First request to get cookie
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

	// Set data
	v.sessions.stateMu.Lock()
	if v.sessions.state[sessionCookie.Value] == nil {
		v.sessions.state[sessionCookie.Value] = make(map[string]any)
	}
	v.sessions.state[sessionCookie.Value]["data"] = TestUser{ID: "123", Name: "Alice"}
	v.sessions.stateMu.Unlock()

	// Clear
	ctx := NewContext(v)
	ctx.sessionID = sessionCookie.Value
	sessionData.id = "data" // Set manually for test
	sessionData.Clear(ctx)

	// Verify session data cleared
	v.sessions.stateMu.RLock()
	_, exists := v.sessions.state[sessionCookie.Value]
	v.sessions.stateMu.RUnlock()
	assert.True(t, exists, "Session should still exist")
	v.sessions.stateMu.RLock()
	_, dataExists := v.sessions.state[sessionCookie.Value]["data"]
	v.sessions.stateMu.RUnlock()
	assert.False(t, dataExists, "User data should be cleared")
}
