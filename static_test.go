package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleStatic_servesFromFS(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"app.css":       {Data: []byte("body { color: amber; }")},
		"sub/inner.txt": {Data: []byte("hello")},
	}

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleStatic("/static/", fsys)
	defer server.Close()

	resp, err := http.Get(server.URL + "/static/app.css")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "amber")

	resp2, err := http.Get(server.URL + "/static/sub/inner.txt")
	require.NoError(t, err)
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	assert.Equal(t, "hello", strings.TrimSpace(string(body2)),
		"nested files should serve under the same prefix")
}

func TestHandleStatic_notFoundFallsThrough(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"existing.txt": {Data: []byte("ok")},
	}

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleStatic("/assets/", fsys)
	defer server.Close()

	resp, err := http.Get(server.URL + "/assets/missing.txt")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleStatic_routeAppearsInIntrospection(t *testing.T) {
	t.Parallel()

	app := via.New()
	app.HandleStatic("/files/", fstest.MapFS{})

	found := false
	for _, r := range app.Routes() {
		if r.Pattern == "GET /files/" && r.RegisteredBy == "HandleStatic" {
			found = true
		}
	}
	assert.True(t, found, "app.Routes() should list the static handler")
}
