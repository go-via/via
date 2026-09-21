package site_test

import (
	"net/http"
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
				"nothing here is content-addressed, so no asset may be cached immutably")
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
