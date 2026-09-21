package site_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/site"
)

const testVersion = "test-build"

func siteServer(t *testing.T, opts site.Options) *httptest.Server {
	t.Helper()
	opts.Version = testVersion
	app, mux := site.New(opts)
	t.Cleanup(app.Close)
	srv := httptest.NewServer(mux)
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
	srv := siteServer(t, site.Options{})

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
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/no-such-page", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, body, "<h1>Not found</h1>")
}

func TestSite_servesHealthzWithTheVersion(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/healthz", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok "+testVersion+"\n", body)
}

func TestSite_servesRobotsAndRedirectsFavicon(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, body := get(t, srv, "/robots.txt", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "User-agent: *\nAllow: /\n", body)

	resp, _ = get(t, srv, "/favicon.ico", nil)
	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	assert.Equal(t, "/static/brand/icon-amber-ink.svg", resp.Header.Get("Location"))
}

func TestSite_namesItsCanonicalURLOnlyWhenTheOriginIsKnown(t *testing.T) {
	t.Parallel()

	_, body := get(t, siteServer(t, site.Options{Origin: "https://go-via.dev"}), "/actions", nil)
	assert.Contains(t, body, `<link rel="canonical" href="https://go-via.dev/actions">`)

	_, body = get(t, siteServer(t, site.Options{}), "/actions", nil)
	assert.NotContains(t, body, `rel="canonical"`)
}

func postAction(t *testing.T, srv *httptest.Server, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/actions/_via/a/1/Cast", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Origin", origin)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestSite_refusesACrossOriginActionWhenTheOriginIsSet(t *testing.T) {
	t.Parallel()

	resp := postAction(t, siteServer(t, site.Options{Origin: "https://go-via.dev"}), "https://evil.example")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp = postAction(t, siteServer(t, site.Options{}), "https://evil.example")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode,
		"without a trusted origin every origin is admitted; the per-tab id is the CSRF token")
}

func sessionCookie(t *testing.T, srv *httptest.Server) *http.Cookie {
	t.Helper()
	resp, _ := get(t, srv, "/actions", nil)
	for _, c := range resp.Cookies() {
		if c.Name == "via_session" {
			return c
		}
	}
	require.Fail(t, "no via_session cookie on a page that calls shell.EnsureSession")
	return nil
}

func TestSite_marksTheSessionCookieSecureOnlyWhenTheOriginIsSet(t *testing.T) {
	t.Parallel()

	assert.True(t, sessionCookie(t, siteServer(t, site.Options{Origin: "https://go-via.dev"})).Secure)
	assert.False(t, sessionCookie(t, siteServer(t, site.Options{})).Secure)
}
