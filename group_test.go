package via

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func TestGroup_BasicGroup(t *testing.T) {
	v := New()

	v.Group("/api", func(g *Group) {
		g.Page("/hello", func(c *Composition) {
			c.View(func(ctx *Context) h.H {
				return h.Div(h.Text("Hello from API"))
			})
		})
	})

	req := httptest.NewRequest("GET", "/api/hello", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Hello from API")
}

func TestGroup_AppliesMiddleware(t *testing.T) {
	v := New()
	var order []string

	v.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "global-before")
			next.ServeHTTP(w, r)
			order = append(order, "global-after")
		})
	})

	v.Group("/api", func(g *Group) {
		g.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "group-before")
				next.ServeHTTP(w, r)
				order = append(order, "group-after")
			})
		})

		g.Page("/hello", func(c *Composition) {
			c.View(func(ctx *Context) h.H {
				return h.Div(h.Text("OK"))
			})
		})
	})

	req := httptest.NewRequest("GET", "/api/hello", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, []string{"global-before", "group-before", "group-after", "global-after"}, order)
}

func TestGroup_NestedGroups(t *testing.T) {
	v := New()
	var order []string

	v.Group("/admin", func(admin *Group) {
		admin.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "admin")
				next.ServeHTTP(w, r)
			})
		})

		admin.Group("/users", func(users *Group) {
			users.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					order = append(order, "users")
					next.ServeHTTP(w, r)
				})
			})

			users.Page("/list", func(c *Composition) {
				c.View(func(ctx *Context) h.H {
					return h.Div(h.Text("User List"))
				})
			})
		})
	})

	req := httptest.NewRequest("GET", "/admin/users/list", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, []string{"admin", "users"}, order)
	assert.Contains(t, w.Body.String(), "User List")
}

func TestGroup_PageInheritsMiddleware(t *testing.T) {
	v := New()
	var middlewareCalled bool

	v.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middlewareCalled = true
			next.ServeHTTP(w, r)
		})
	})

	v.Group("/api", func(g *Group) {
		g.Page("/test", func(c *Composition) {
			c.View(func(ctx *Context) h.H {
				return h.Div(h.Text("OK"))
			})
		})
	})

	req := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.True(t, middlewareCalled)
}

func TestGroup_NonGroupRouteNoGroupMiddleware(t *testing.T) {
	v := New()
	var groupMiddlewareCalled bool

	v.Group("/admin", func(g *Group) {
		g.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				groupMiddlewareCalled = true
				next.ServeHTTP(w, r)
			})
		})

		g.Page("/dashboard", func(c *Composition) {
			c.View(func(ctx *Context) h.H {
				return h.Div(h.Text("Admin"))
			})
		})
	})

	v.Page("/", func(c *Composition) {
		c.View(func(ctx *Context) h.H {
			return h.Div(h.Text("Home"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.False(t, groupMiddlewareCalled, "Group middleware should not apply to non-group routes")
}

func TestGroup_NonExistentRoute404(t *testing.T) {
	v := New()

	v.Group("/api", func(g *Group) {
		g.Page("/hello", func(c *Composition) {
			c.View(func(ctx *Context) h.H {
				return h.Div(h.Text("Hello"))
			})
		})
	})

	req := httptest.NewRequest("GET", "/api/notfound", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
