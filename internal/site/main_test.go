package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func siteServer(t *testing.T) *httptest.Server {
	t.Helper()
	app := newApp()
	t.Cleanup(app.Close)
	srv := httptest.NewServer(newMux(app))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string, hdr http.Header) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header[k] = v
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(body)
}

func TestSite_rendersEveryMountedPage(t *testing.T) {
	t.Parallel()
	srv := siteServer(t)

	pages := []struct {
		name string
		path string
		h1   string
	}{
		{"landing", "/", "Server-rendered Go UI that stays live"},
		{"actions", "/actions", "Actions"},
		{"signals", "/signals", "Signals"},
		{"live", "/live", "Live"},
		{"islands", "/islands", "Islands"},
		{"platform", "/platform", "Platform"},
		{"reference", "/reference", "Reference"},
	}
	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			resp, body := get(t, srv, p.path, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Contains(t, body, "<h1>"+p.h1+"</h1>")
		})
	}
}

func TestSite_answersUnknownPathWithTheErrorPage(t *testing.T) {
	t.Parallel()
	srv := siteServer(t)

	resp, body := get(t, srv, "/no-such-page", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, body, "<h1>Not found</h1>")
}

func TestSite_servesHealthzWithTheVersion(t *testing.T) {
	t.Parallel()
	srv := siteServer(t)

	resp, body := get(t, srv, "/healthz", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok "+version+"\n", body)
}

func TestSite_servesRobotsAndRedirectsFavicon(t *testing.T) {
	t.Parallel()
	srv := siteServer(t)

	resp, body := get(t, srv, "/robots.txt", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "User-agent: *\nAllow: /\n", body)

	resp, _ = get(t, srv, "/favicon.ico", nil)
	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	assert.Equal(t, "/static/brand/icon-amber-ink.svg", resp.Header.Get("Location"))
}

func TestSite_answers304ForAMatchingETag(t *testing.T) {
	t.Parallel()
	srv := siteServer(t)

	resp, body := get(t, srv, "/static/site.css", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, body)
	etag := resp.Header.Get("ETag")
	require.NotEmpty(t, etag)

	resp, body = get(t, srv, "/static/site.css", http.Header{"If-None-Match": {etag}})
	assert.Equal(t, http.StatusNotModified, resp.StatusCode)
	assert.Empty(t, body)
}

func TestSite_cachesVersionedAssetsLongerThanTheRest(t *testing.T) {
	t.Parallel()
	srv := siteServer(t)

	resp, _ := get(t, srv, "/static/brand/icon-amber-ink.svg", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "public, max-age=31536000, immutable", resp.Header.Get("Cache-Control"))

	resp, _ = get(t, srv, "/static/site.css", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "public, max-age=3600, must-revalidate", resp.Header.Get("Cache-Control"))
}
