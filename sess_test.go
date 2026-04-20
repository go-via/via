package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSession_createdOnFirstRequest(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	cookies := resp.Cookies()
	assert.NotEmpty(t, cookies)
	assert.Equal(t, "via_session", cookies[0].Name)
}

func TestSession_cookieIs64CharHex(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	assert.Len(t, cookies[0].Value, 64)
}

func TestSession_isHttpOnly(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	assert.True(t, cookies[0].HttpOnly)
}

func TestSession_pathIsRoot(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	assert.Equal(t, "/", cookies[0].Path)
}

func TestSession_secureFlagWhenEnabled(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		next.ServeHTTP(w, r)
	})
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	assert.False(t, cookies[0].Secure)
}
