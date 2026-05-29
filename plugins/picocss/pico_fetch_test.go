package picocss

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Internal test: fetchCSS's failure modes can't be driven through the
// public API (the CDN URL is a constant — the real CDN can't be made to
// fail on demand), so exercise the error branches directly.

func TestFetchCSS_errorsOnNon200(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := fetchCSS(srv.URL)
	require.Error(t, err, "a non-200 CDN response must be an error, not silently-empty CSS")
	assert.Contains(t, err.Error(), "status 500",
		"the error must name the upstream status so a sick CDN is diagnosable")
}

func TestFetchCSS_errorsWhenUpstreamUnreachable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // closed listener → connection refused

	_, err := fetchCSS(url)
	require.Error(t, err, "an unreachable CDN must surface the transport error")
}
