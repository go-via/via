package site_test

import (
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/shell"
	"go-via.dev/site/site"
)

func TestSite_pagerLinksTheNeighboursOutsideMain(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	written := shell.Pages()
	for i, p := range written {
		t.Run(p.Path, func(t *testing.T) {
			t.Parallel()
			_, body := get(t, srv, p.Path, nil)
			pager := strings.Index(body, `<nav class="pager"`)
			require.NotEqual(t, -1, pager, "no pager")
			assert.Greater(t, pager, strings.Index(body, "</main>"), "the pager sits outside <main>, where search does not read")
			if i > 0 {
				assert.Contains(t, body, `<a class="pager-prev" href="`+written[i-1].Path+`" rel="prev">`)
			}
			if p.Path == "/" {
				assert.Contains(t, body, `<a class="pager-next" href="/why" rel="next">`, "the front page leads to Why Via")
				return
			}
			if i+1 < len(written) {
				assert.Contains(t, body, `<a class="pager-next" href="`+written[i+1].Path+`" rel="next">`)
				assert.Contains(t, body, html.EscapeString(written[i+1].Title))
			} else {
				assert.NotContains(t, body, `class="pager-next"`)
			}
		})
	}
}

func TestSite_listsContentsFromTwoHeadings(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	_, body := get(t, srv, "/signals", nil)
	assert.Contains(t, body, `<nav class="toc toc-rail" aria-label="On this page">`)
	assert.Contains(t, body, `<details class="toc toc-inline">`)
	assert.Contains(t, body, `<a href="#greeting">Greeting</a>`, "a demo card's heading is an h3 entry")
	assert.Contains(t, body, `<a href="#top" class="to-top">`)

	_, body = get(t, srv, "/actions", nil)
	if strings.Count(body, `<h2 id=`)+strings.Count(body, `<h3 id=`) < 2 {
		assert.NotContains(t, body, `class="toc`)
	}
}

func TestSite_linksEachPageToItsSource(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	const src = "https://github.com/go-via/via/blob/main/internal/site/content/"
	_, body := get(t, srv, "/signals", nil)
	assert.Contains(t, body, `href="`+src+`signals.go"`)
	_, body = get(t, srv, "/", nil)
	assert.Contains(t, body, `href="`+src+`landing.go"`)
}

func TestSite_redirectsATrailingSlashToTheCanonicalPath(t *testing.T) {
	t.Parallel()

	resp, _ := get(t, siteServer(t, site.Options{}), "/reference/?x=1", nil)
	assert.Equal(t, http.StatusPermanentRedirect, resp.StatusCode)
	assert.Equal(t, "/reference?x=1", resp.Header.Get("Location"))

	resp, _ = get(t, siteServer(t, site.Options{}), "/no-such-page/", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp, _ = get(t, siteServer(t, site.Options{
		Base:     "/v0.8",
		Versions: []shell.Version{{Label: "v0.9", Base: ""}, {Label: "v0.8", Base: "/v0.8"}},
	}), "/v0.8/actions/", nil)
	assert.Equal(t, http.StatusPermanentRedirect, resp.StatusCode)
	assert.Equal(t, "/v0.8/actions", resp.Header.Get("Location"))
}

func TestSite_notFoundSuggestsAPageAndSearches(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/refrence", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, body, `Did you mean <a href="/reference">`)
	assert.Contains(t, body, `name="q"`)

	_, body = get(t, srv, "/nothing-here?q=shutdown+order", nil)
	assert.Contains(t, body, `href="/deploy#shutdown-order"`)
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

func TestSite_writesSocialMetaOffTheOrigin(t *testing.T) {
	t.Parallel()

	_, body := get(t, siteServer(t, site.Options{Origin: "https://go-via.dev"}), "/signals", nil)
	assert.Contains(t, body, `<meta property="og:image" content="https://go-via.dev/static/brand/punch-dark.png">`)
	assert.Contains(t, body, `<meta property="og:url" content="https://go-via.dev/signals">`)
	assert.Contains(t, body, `<meta name="twitter:card" content="summary_large_image">`)
	assert.Contains(t, body, `<meta name="theme-color" content="#1b1e24">`)
	assert.Contains(t, body, `rel="apple-touch-icon"`)

	_, body = get(t, siteServer(t, site.Options{}), "/signals", nil)
	assert.NotContains(t, body, `og:image`, "og:image must be absolute, so no origin is no image")
	assert.NotContains(t, body, `og:url`)
}

func TestSite_servesASitemapOfEveryPage(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{Origin: "https://go-via.dev"})

	resp, body := get(t, srv, "/sitemap.xml", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/xml")
	assert.Contains(t, body, "<loc>https://go-via.dev/signals</loc>")
	assert.Contains(t, body, "<loc>https://go-via.dev/</loc>")
	for _, p := range shell.Pages() {
		assert.Contains(t, body, "<loc>https://go-via.dev"+p.Path+"</loc>")
	}

	_, body = get(t, srv, "/robots.txt", nil)
	assert.Contains(t, body, "Sitemap: https://go-via.dev/sitemap.xml\n")

	resp, _ = get(t, siteServer(t, site.Options{}), "/sitemap.xml", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a sitemap needs absolute URLs")
}

func TestSite_tablesHaveAHeaderRow(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	_, body := get(t, srv, "/reference", nil)
	assert.Contains(t, body, `<thead><tr><th scope="col">`)
	assert.Contains(t, body, `<tbody>`)
}
