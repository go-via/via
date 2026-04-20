package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddleware_addsHeaderToResponse(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("X-Middleware", "applied")
		next.ServeHTTP(w, r)
	})
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "applied", resp.Header.Get("X-Middleware"))
}

func TestMiddleware_shortCircuits(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.WriteHeader(http.StatusForbidden)
	})
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMiddleware_runsMultiple(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("X-First", "one")
		next.ServeHTTP(w, r)
	})
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("X-Second", "two")
		next.ServeHTTP(w, r)
	})
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "one", resp.Header.Get("X-First"))
	assert.Equal(t, "two", resp.Header.Get("X-Second"))
}
