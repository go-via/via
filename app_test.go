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

func TestApp_builtinEndpointsReject404OnUnknownTab(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	via.New(via.WithTestServer(&server))
	t.Cleanup(func() { server.Close() })

	cases := []struct {
		name string
		do   func() (*http.Response, error)
	}{
		{"GET /_sse", func() (*http.Response, error) {
			return http.Get(server.URL + "/_sse")
		}},
		{"POST /_action/Inc", func() (*http.Response, error) {
			return http.Post(server.URL+"/_action/Inc", "text/plain", nil)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			resp, err := c.do()
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		})
	}
}

func TestApp_implementsHTTPHandler(t *testing.T) {
	t.Parallel()
	var _ http.Handler = via.New()
}
