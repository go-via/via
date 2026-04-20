package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via/internal/viaold"
	"github.com/go-via/via/internal/viaold/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroup_prefixesRoutes(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	g := v.Group("/admin")
	g.Page("/dashboard", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("admin dashboard")) })
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/admin/dashboard")
	assert.Contains(t, body, "admin dashboard")
}

func TestGroup_appliesScopedMiddleware(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.Page("/public", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("public")) })
	})

	g := v.Group("/admin")
	g.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("X-Admin", "true")
		next.ServeHTTP(w, r)
	})
	g.Page("/dashboard", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("admin")) })
	})
	t.Cleanup(server.Close)

	// Admin route should have middleware header
	resp, err := http.Get(server.URL + "/admin/dashboard")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, "true", resp.Header.Get("X-Admin"))

	// Public route should NOT have middleware header
	resp2, err := http.Get(server.URL + "/public")
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Empty(t, resp2.Header.Get("X-Admin"))
}

func TestGroup_middlewareExecutesGlobalThenGroup(t *testing.T) {
	t.Parallel()

	var order string
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		order += "G"
		next.ServeHTTP(w, r)
	})

	g := v.Group("/api")
	g.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		order += "A"
		next.ServeHTTP(w, r)
	})
	g.Page("/data", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/api/data")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "GA", order, "global middleware should run before group middleware")
}

func TestGroup_nestedGroupsStackMiddleware(t *testing.T) {
	t.Parallel()

	var order string
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		order += "G"
		next.ServeHTTP(w, r)
	})

	outer := v.Group("/a")
	outer.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		order += "O"
		next.ServeHTTP(w, r)
	})

	inner := outer.Group("/b")
	inner.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		order += "I"
		next.ServeHTTP(w, r)
	})
	inner.Page("/c", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/a/b/c")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "GOI", order, "middleware: global → outer → inner")
}

func TestGroup_collapsesDoubleSlashes(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	// trailing slash on prefix + leading slash on route
	g := v.Group("/admin/")
	g.Page("/dashboard", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("no double slash")) })
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/admin/dashboard")
	assert.Contains(t, body, "no double slash")
}

func TestGroup_nestedCollapsesDoubleSlashes(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	outer := v.Group("/a/")
	inner := outer.Group("/b/")
	inner.Page("/c", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("nested ok")) })
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/a/b/c")
	assert.Contains(t, body, "nested ok")
}

func TestGroup_middlewareCanShortCircuit(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	g := v.Group("/protected")
	g.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		// not calling next
	})
	g.Page("/secret", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("secret content")) })
	})

	v.Page("/login", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("login page")) })
	})
	t.Cleanup(server.Close)

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(server.URL + "/protected/secret")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.NotContains(t, string(body), "secret content")
}
