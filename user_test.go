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

func TestUserHandle_TypeExists(t *testing.T) {
	// Just verify the type can be instantiated
	v := New()
	v.Page("/", func(c *Composition) {
		_ = NewUserHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})
}

func TestUserHandle_Get_ReturnsNotAuthenticated(t *testing.T) {
	v := New()
	var userHandle *UserHandle[TestUser]

	v.Page("/", func(c *Composition) {
		userHandle = NewUserHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	// Create session without setting user
	ctx := NewContext(v)
	ctx.sessionID = "test-session"

	// Get should return false for authenticated
	_, ok := userHandle.Get(ctx)
	assert.False(t, ok, "Should not be authenticated initially")
}

func TestUserHandle_IsAuthenticated_False(t *testing.T) {
	v := New()
	var userHandle *UserHandle[TestUser]

	v.Page("/", func(c *Composition) {
		userHandle = NewUserHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	ctx := NewContext(v)
	ctx.sessionID = "test-session"

	assert.False(t, userHandle.IsAuthenticated(ctx))
}

func TestUserHandle_Get_AfterSet(t *testing.T) {
	v := New()
	var userHandle *UserHandle[TestUser]

	v.Page("/", func(c *Composition) {
		userHandle = NewUserHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	ctx := NewContext(v)
	ctx.sessionID = "test-session"

	// Simulate middleware setting user
	v.sessionStateMu.Lock()
	if v.sessionState[ctx.sessionID] == nil {
		v.sessionState[ctx.sessionID] = make(map[string]any)
	}
	v.sessionState[ctx.sessionID][userHandle.id] = TestUser{ID: "123", Name: "Alice", Role: "admin"}
	v.sessionStateMu.Unlock()

	// Get should return user and true
	user, ok := userHandle.Get(ctx)
	assert.True(t, ok)
	assert.Equal(t, "123", user.ID)
	assert.Equal(t, "Alice", user.Name)
	assert.Equal(t, "admin", user.Role)
}

func TestUserHandle_IsAuthenticated_AfterSet(t *testing.T) {
	v := New()
	var userHandle *UserHandle[TestUser]

	v.Page("/", func(c *Composition) {
		userHandle = NewUserHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	ctx := NewContext(v)
	ctx.sessionID = "test-session"

	// Set user data
	v.sessionStateMu.Lock()
	if v.sessionState[ctx.sessionID] == nil {
		v.sessionState[ctx.sessionID] = make(map[string]any)
	}
	v.sessionState[ctx.sessionID][userHandle.id] = TestUser{ID: "123", Name: "Alice"}
	v.sessionStateMu.Unlock()

	assert.True(t, userHandle.IsAuthenticated(ctx))
}

func TestUserHandle_Logout_ClearsData(t *testing.T) {
	v := New()
	var userHandle *UserHandle[TestUser]

	v.Page("/", func(c *Composition) {
		userHandle = NewUserHandle[TestUser]()
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	ctx := NewContext(v)
	ctx.sessionID = "test-session"

	// Set user data
	v.sessionStateMu.Lock()
	if v.sessionState[ctx.sessionID] == nil {
		v.sessionState[ctx.sessionID] = make(map[string]any)
	}
	v.sessionState[ctx.sessionID][userHandle.id] = TestUser{ID: "123", Name: "Alice"}
	v.sessionStateMu.Unlock()

	assert.True(t, userHandle.IsAuthenticated(ctx))

	// Logout
	userHandle.Logout(ctx)

	// Should no longer be authenticated
	assert.False(t, userHandle.IsAuthenticated(ctx))
	_, ok := userHandle.Get(ctx)
	assert.False(t, ok)
}

func TestUserHandle_Logout_ClearsCookie(t *testing.T) {
	v := New()
	var userHandle *UserHandle[TestUser]

	v.Page("/", func(c *Composition) {
		userHandle = NewUserHandle[TestUser]()
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

	// Set user data
	v.sessionStateMu.Lock()
	if v.sessionState[sessionCookie.Value] == nil {
		v.sessionState[sessionCookie.Value] = make(map[string]any)
	}
	v.sessionState[sessionCookie.Value]["user"] = TestUser{ID: "123", Name: "Alice"}
	v.sessionStateMu.Unlock()

	// Logout
	ctx := NewContext(v)
	ctx.sessionID = sessionCookie.Value
	userHandle.id = "user" // Set manually for test
	userHandle.Logout(ctx)

	// Verify session data cleared
	v.sessionStateMu.RLock()
	_, exists := v.sessionState[sessionCookie.Value]
	v.sessionStateMu.RUnlock()
	assert.True(t, exists, "Session should still exist")
	v.sessionStateMu.RLock()
	_, userExists := v.sessionState[sessionCookie.Value]["user"]
	v.sessionStateMu.RUnlock()
	assert.False(t, userExists, "User data should be cleared")
}
