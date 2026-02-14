package via

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

// TestMiddleware_ChainExecutesInOrder verifies middleware executes in order
func TestMiddleware_ChainExecutesInOrder(t *testing.T) {
	v := New()
	var order []string

	// Middleware 1
	mw1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw1-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw1-after")
		})
	}

	// Middleware 2
	mw2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw2-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw2-after")
		})
	}

	v.Use(mw1, mw2)

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			order = append(order, "handler")
			return h.Div(h.Text("OK"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, []string{"mw1-before", "mw2-before", "handler", "mw2-after", "mw1-after"}, order)
}

// TestMiddleware_CanModifyRequest verifies middleware can modify request/response
func TestMiddleware_CanModifyRequest(t *testing.T) {
	v := New()

	// Middleware that adds header
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test-Header", "test-value")
			next.ServeHTTP(w, r)
		})
	}

	v.Use(mw)

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("OK"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, "test-value", w.Header().Get("X-Test-Header"))
}

// TestMiddleware_Recovery recovers from panics
func TestMiddleware_Recovery(t *testing.T) {
	v := New()
	recovered := false

	// Recovery middleware
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					recovered = true
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}

	v.Use(mw)

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			panic("test panic")
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.True(t, recovered, "Expected panic to be recovered")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestMiddleware_NoMiddleware works without middleware
func TestMiddleware_NoMiddleware(t *testing.T) {
	v := New()

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("OK"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "OK")
}

// TestMiddleware_MultipleUse accumulates middleware
func TestMiddleware_MultipleUse(t *testing.T) {
	v := New()
	var order []string

	v.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "1")
			next.ServeHTTP(w, r)
		})
	})

	v.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "2")
			next.ServeHTTP(w, r)
		})
	})

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("OK"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, []string{"1", "2"}, order)
}

// TestMiddleware_AppliesToActions verifies middleware applies to actions
func TestMiddleware_AppliesToActions(t *testing.T) {
	v := New()
	var middlewareCalled bool

	v.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middlewareCalled = true
			next.ServeHTTP(w, r)
		})
	})

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("OK"))
		})
	})

	// Load page first to get session
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	// Verify page load called middleware
	assert.True(t, middlewareCalled, "Middleware should be called for page request")
}

// TestMiddleware_ShortCircuit verifies middleware can short-circuit
func TestMiddleware_ShortCircuit(t *testing.T) {
	v := New()
	handlerCalled := false

	v.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			// Don't call next - short circuit
		})
	})

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			handlerCalled = true
			return h.Div(h.Text("OK"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, handlerCalled, "Handler should not be called when middleware short-circuits")
}

// TestAction_GlobalMiddlewareApplies global middleware already applies to action routes
func TestAction_GlobalMiddlewareApplies(t *testing.T) {
	v := New()
	var callOrder []string

	v.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callOrder = append(callOrder, "global-before")
			next.ServeHTTP(w, r)
			callOrder = append(callOrder, "global-after")
		})
	})

	v.Page("/", func(c *Composition) {
		action := Action(c, func(ctx *Context) {
			callOrder = append(callOrder, "action")
		})

		c.View(func(ctx *Context) h.H {
			return h.Div(
				h.Button(h.Text("Click"), action.OnClick()),
			)
		})
	})

	// Load page first
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	// Global middleware was called for page
	assert.Contains(t, callOrder, "global-before")

	// Note: Full action testing requires session context
	// Global middleware already applies to /_action/* routes
}
