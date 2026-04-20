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

func TestMiddleware_runsBeforePageHandler(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("X-Via-Test", "middleware-ran")
		next.ServeHTTP(w, r)
	})
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("hello")) })
	})
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "middleware-ran", resp.Header.Get("X-Via-Test"))
}

func TestMiddleware_canShortCircuit(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.WriteHeader(http.StatusForbidden)
		// not calling next — page should never render
	})
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("secret")) })
	})
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.NotContains(t, string(body), "secret")
}

func TestMiddleware_executesInOrder(t *testing.T) {
	t.Parallel()

	var order string
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		order += "A"
		next.ServeHTTP(w, r)
	})
	v.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		order += "B"
		next.ServeHTTP(w, r)
	})
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "AB", order)
}

func TestMiddleware_doesNotRunOnInternalRoutes(t *testing.T) {
	t.Parallel()

	middlewareRan := false
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		middlewareRan = true
		next.ServeHTTP(w, r)
	})
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	// Visit page first (middleware runs here)
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	require.True(t, middlewareRan, "middleware should run on page GET")

	// Reset and test internal route
	middlewareRan = false
	resp2, err := http.Get(server.URL + "/_datastar.js")
	require.NoError(t, err)
	resp2.Body.Close()

	assert.False(t, middlewareRan, "middleware should NOT run on internal routes")
}

func TestMiddleware_hasAccessToSessionData(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.HandleFunc("POST /set-user", func(w http.ResponseWriter, r *http.Request) {
		via.SetSess(w, r, testUser{Name: "dana"})
	})

	v.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		user := via.GetSess[testUser](r)
		if user.Name != "" {
			w.Header().Set("X-User", user.Name)
		}
		next.ServeHTTP(w, r)
	})

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	// Get session
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	jar := collectCookies(t, server.URL, resp.Cookies())

	// Set user
	req, _ := http.NewRequest("POST", server.URL+"/set-user", nil)
	addCookies(req, jar)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	jar = mergeCookies(jar, resp2.Cookies())

	// Visit page — middleware should see user
	req2, _ := http.NewRequest("GET", server.URL+"/", nil)
	addCookies(req2, jar)
	resp3, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	resp3.Body.Close()

	assert.Equal(t, "dana", resp3.Header.Get("X-User"))
}
