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

func TestApp_servesDatastarJS(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	via.New(via.WithTestServer(&server))
	defer server.Close()

	resp, err := http.Get(server.URL + "/_datastar.js")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestApp_registersHandlerFunc(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	buf, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(buf), "hello")
}

func TestApp_routes404ForUnknownPath(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/known", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("known"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/unknown-path")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestApp_handlesMultipleRoutes(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/first", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("first"))
	})
	app.HandleFunc("/second", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("second"))
	})
	defer server.Close()

	resp1, err := http.Get(server.URL + "/first")
	require.NoError(t, err)
	buf1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	assert.Contains(t, string(buf1), "first")

	resp2, err := http.Get(server.URL + "/second")
	require.NoError(t, err)
	buf2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	assert.Contains(t, string(buf2), "second")
}

func TestApp_sseEndpointExists(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/_sse")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestApp_actionEndpointExists(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Post(server.URL+"/_action/test", "text/plain", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
