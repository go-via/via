package vt_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingRoundTripper struct {
	inner http.RoundTripper
	hits  *atomic.Int32
}

func (r recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.hits.Add(1)
	return r.inner.RoundTrip(req)
}

// swapDefaultTransport replaces http.DefaultTransport with a recording
// wrapper so a test can prove a constructed http.Client doesn't fall back
// to the package-global pool. Restored via t.Cleanup; the test must not
// call t.Parallel because the mutation is process-global.
func swapDefaultTransport(t *testing.T) *atomic.Int32 {
	t.Helper()
	orig := http.DefaultTransport
	var hits atomic.Int32
	http.DefaultTransport = recordingRoundTripper{inner: orig, hits: &hits}
	t.Cleanup(func() { http.DefaultTransport = orig })
	return &hits
}

func TestNewClient_usesIsolatedHTTPTransport(t *testing.T) { //nolint:paralleltest // swaps the process-global http.DefaultTransport
	hits := swapDefaultTransport(t)

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Bump").Fire())

	assert.Zero(t, hits.Load(),
		"NewClient's http.Client must have its own Transport so parallel "+
			"tests' server.Close() can't disturb each other's idle pools")
}

func TestClient_Fork_usesIsolatedHTTPTransport(t *testing.T) { //nolint:paralleltest // swaps the process-global http.DefaultTransport
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	hits := swapDefaultTransport(t)

	forked := tc.Fork("/")
	require.Equal(t, 200, forked.Action("Bump").Fire())

	assert.Zero(t, hits.Load(),
		"Fork's http.Client must use its own Transport, not http.DefaultTransport")
}

func TestClient_SSE_usesIsolatedHTTPTransport(t *testing.T) { //nolint:paralleltest // swaps the process-global http.DefaultTransport
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	hits := swapDefaultTransport(t)

	_, cancel := tc.SSEReady()
	defer cancel()

	assert.Zero(t, hits.Load(),
		"SSE's http.Client must use its own Transport, not http.DefaultTransport")
}
