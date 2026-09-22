package site_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
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

func TestMux_healthzAndRobotsCarryHeaders(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{})

	resp, _ := get(t, srv, "/healthz", nil)
	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))

	resp, _ = get(t, srv, "/robots.txt", nil)
	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "public, max-age=3600", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
}

func TestSite_namesItsCanonicalURLOnlyWhenTheOriginIsKnown(t *testing.T) {
	t.Parallel()

	_, body := get(t, siteServer(t, site.Options{Origin: "https://go-via.dev"}), "/actions", nil)
	assert.Contains(t, body, `<link rel="canonical" href="https://go-via.dev/actions">`)

	_, body = get(t, siteServer(t, site.Options{}), "/actions", nil)
	assert.NotContains(t, body, `rel="canonical"`)
}

var voteAction = regexp.MustCompile(`@post\('([^'?]+)\?a=0'\)">vote<`)

// postAction fires the vote demo's Cast: action names are hashed per render,
// so the URL is read off the page rather than spelled.
func postAction(t *testing.T, srv *httptest.Server, origin string) *http.Response {
	t.Helper()
	_, body := get(t, srv, "/actions", nil)
	m := voteAction.FindStringSubmatch(body)
	require.NotNil(t, m, "no vote button on /actions")
	req, err := http.NewRequest(http.MethodPost, srv.URL+m[1], strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Origin", origin)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Content-Type", "application/json")
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
	assert.Equal(t, http.StatusGone, resp.StatusCode,
		"without a trusted origin the origin is not checked; the per-tab id is the CSRF token, and no render bound this action for it")
}

var signInForm = regexp.MustCompile(`<form[^>]*action="([^"]+)"`)

// sessionCookie signs in through the /platform form: no page mints a session
// on a GET, so the cookie exists only once a handler has stored something.
func sessionCookie(t *testing.T, srv *httptest.Server) *http.Cookie {
	t.Helper()
	_, body := get(t, srv, "/platform", nil)
	m := signInForm.FindStringSubmatch(body)
	require.NotNil(t, m, "no sign-in form on /platform")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// /platform streams, so the form's tab id is filled client-side from
	// $viatab. This client has none; Auth is not live, so the action runs on
	// the plain path anyway.
	require.NoError(t, mw.WriteField("_viatab", ""))
	require.NoError(t, mw.WriteField("name", "tester"))
	require.NoError(t, mw.Close())

	req, err := http.NewRequest(http.MethodPost, srv.URL+m[1], &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", srv.URL)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "via_session" {
			cookie = c
		}
	}
	require.NotNil(t, cookie, "no via_session cookie after signing in")
	return cookie
}

func TestSite_marksTheSessionCookieSecureOnlyWhenTheOriginIsSet(t *testing.T) {
	t.Parallel()

	assert.True(t, sessionCookie(t, siteServer(t, site.Options{Origin: "https://go-via.dev"})).Secure)
	assert.False(t, sessionCookie(t, siteServer(t, site.Options{})).Secure)
}
