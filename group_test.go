package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroup_prefixesRoutes(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	group := app.Group("/api")
	group.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("users"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/users")
	require.NoError(t, err)
	defer resp.Body.Close()

	buf, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(buf), "users")
}

func TestGroup_middlewareAppliesToHandlerFunc(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	group := app.Group("/api")
	group.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("X-Group", "yes")
		next.ServeHTTP(w, r)
	})
	group.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("users"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/users")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "yes", resp.Header.Get("X-Group"),
		"group middleware must wrap HandleFunc-registered handlers")
}

func TestGroup_middlewareCanShortCircuit(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	group := app.Group("/admin")
	group.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.WriteHeader(http.StatusForbidden)
	})
	group.HandleFunc("/secret", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("LEAK"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/admin/secret")
	require.NoError(t, err)
	defer resp.Body.Close()

	buf, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.NotContains(t, string(buf), "LEAK",
		"short-circuit middleware must prevent the inner handler from running")
}

type groupedComp struct{}

func (g *groupedComp) View(ctx *via.Ctx) h.H { return h.Div() }

func TestGroup_middlewareAppliesToMountedComposition(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	group := app.Group("/admin")
	group.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("X-Group", "wrapped")
		next.ServeHTTP(w, r)
	})
	via.Mount[groupedComp](group, "/dashboard")
	defer server.Close()

	resp, err := http.Get(server.URL + "/admin/dashboard")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "wrapped", resp.Header.Get("X-Group"),
		"Mount on a *Group must wrap the rendered route in the group's middleware")
}

type tenantPage struct {
	Tenant string `path:"tenant"`
	UserID int    `path:"id"`
}

func (p *tenantPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.Span(h.Textf("tenant=%s", p.Tenant)),
		h.Span(h.Textf("user=%d", p.UserID)))
}

func TestGroup_pathParamsUnderGroupPrefix(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	api := app.Group("/api/{tenant}")
	via.Mount[tenantPage](api, "/users/{id}")
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/acme/users/42")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := readGroupBody(t, resp)
	assert.Contains(t, body, "tenant=acme",
		"path param from group prefix should decode into the typed field")
	assert.Contains(t, body, "user=42",
		"path param from Mount route should decode alongside")
}

func readGroupBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf, _ := io.ReadAll(resp.Body)
	return string(buf)
}

func TestGroup_routes404WithoutPrefix(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	group := app.Group("/api")
	group.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("users"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/users")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGroup_handleFuncRegistersExplicitMethod(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	group := app.Group("/api")
	group.HandleFunc("POST /widgets", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("created"))
	})
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/widgets", "application/json", http.NoBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(buf), "created")

	// GET to the same path must miss — POST registration shouldn't leak.
	getResp, err := http.Get(server.URL + "/api/widgets")
	require.NoError(t, err)
	defer getResp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, getResp.StatusCode)
}

// Group middleware applies to action POSTs and SSE handshakes too

type protectedPage struct {
	N via.State[int]
}

func (p *protectedPage) Bump(ctx *via.Ctx) error {
	p.N.Set(ctx, p.N.Get(ctx)+1)
	return nil
}

func (p *protectedPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.N.Text(), h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestGroupMiddleware_appliesToActionPOST(t *testing.T) {
	t.Parallel()

	var seenAuth atomic.Bool
	var allowed atomic.Bool
	allowed.Store(true)

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))

	g := app.Group("/p")
	g.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		seenAuth.Store(true)
		if !allowed.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
	via.Mount[protectedPage](g, "/secret")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/p/secret")
	require.True(t, seenAuth.Load(), "middleware must run on the page render")

	seenAuth.Store(false)
	require.Equal(t, 200, tc.Action("Bump").Fire())
	require.True(t, seenAuth.Load(),
		"group middleware must run on the action POST too — not only on the page render")

	allowed.Store(false)
	got := tc.Action("Bump").Fire()
	assert.Equal(t, http.StatusForbidden, got,
		"middleware short-circuit on action POST should return its status")
}

func TestGroupMiddleware_appliesToSSEHandshake(t *testing.T) {
	t.Parallel()

	var seen atomic.Bool
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))

	g := app.Group("/p")
	g.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		seen.Store(true)
		next.ServeHTTP(w, r)
	})
	via.Mount[protectedPage](g, "/secret")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/p/secret")
	require.True(t, seen.Load(), "render hit middleware")

	seen.Store(false)
	_, cancel := tc.SSE()
	defer cancel()
	require.Eventually(t, seen.Load, 500*time.Millisecond, 10*time.Millisecond,
		"group middleware did not run on SSE handshake")
}
