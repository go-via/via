package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
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

func TestGroup_registersHandlerFunc(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	group := app.Group("/v1")
	group.HandleFunc("/items", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("items"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/items")
	require.NoError(t, err)
	defer resp.Body.Close()

	buf, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(buf), "items")
}

func TestGroup_storesMiddleware(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	group := app.Group("/api")
	group.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		next.ServeHTTP(w, r)
	})
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
