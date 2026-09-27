package site_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/site"
)

func TestStatic_answers304ForAMatchingETag(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/static/site.css", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, body)
	etag := resp.Header.Get("ETag")
	require.NotEmpty(t, etag)

	cases := []struct {
		name      string
		noneMatch string
	}{
		{"exact", etag},
		{"weak", "W/" + etag},
		{"wildcard", "*"},
		{"one of several", `"other", ` + etag},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp, body := get(t, srv, "/static/site.css", http.Header{"If-None-Match": {tc.noneMatch}})
			assert.Equal(t, http.StatusNotModified, resp.StatusCode)
			assert.Empty(t, body)
		})
	}
}

func TestStatic_revalidatesEveryAsset(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	for _, path := range []string{"/static/site.css", "/static/brand/icon-amber-ink.svg", "/static/fonts/inter.woff2"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			resp, _ := get(t, srv, path, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "public, max-age=3600, must-revalidate", resp.Header.Get("Cache-Control"),
				"an unfingerprinted URL can change under a redeploy, so it is never cached immutably")
			assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		})
	}
}

func TestStatic_sendsNosniffOnAMiss(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, _ := get(t, srv, "/static/no-such-asset.css", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
}

func TestStatic_doesNotListDirectories(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	for _, path := range []string{"/static/", "/static/brand/", "/static/fonts/", "/static/data/", "/static/vendor/"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			resp, _ := get(t, srv, path, nil)
			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		})
	}
}

func TestStatic_refusesAnAliasSpelling(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, _ := get(t, srv, "/static/brand/..%2fsite.css", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestStatic_doesNotServeAnAssetToAPOST(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, err := srv.Client().Post(srv.URL+"/static/site.css", "text/css", nil)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"the assets are bound to GET, and the router behind the mux's catch-all answers what falls through")
}

var hashedCSS = regexp.MustCompile(`href="(/static/[0-9a-f]{8}/site\.css)"`)

func TestStatic_servesFingerprintedURLsImmutably(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	_, body := get(t, srv, "/signals", nil)
	m := hashedCSS.FindStringSubmatch(body)
	require.NotNil(t, m, "the stylesheet is linked by its fingerprinted URL")

	resp, css := get(t, srv, m[1], nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "public, max-age=31536000, immutable", resp.Header.Get("Cache-Control"))
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/css")
	_, plain := get(t, srv, "/static/site.css", nil)
	assert.Equal(t, plain, css)

	resp, _ = get(t, srv, "/static/00000000/site.css", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "a stale fingerprint still gets the file")
	assert.Equal(t, "public, max-age=3600, must-revalidate", resp.Header.Get("Cache-Control"))

	resp, _ = get(t, srv, "/static/00000000/no-such.css", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
