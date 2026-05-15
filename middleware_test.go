package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddleware_addsHeaderToResponse(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("X-Middleware", "applied")
		next.ServeHTTP(w, r)
	})
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "applied", resp.Header.Get("X-Middleware"))
}

func TestMiddleware_shortCircuits(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.WriteHeader(http.StatusForbidden)
	})
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMiddleware_runsMultiple(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("X-First", "one")
		next.ServeHTTP(w, r)
	})
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("X-Second", "two")
		next.ServeHTTP(w, r)
	})
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "one", resp.Header.Get("X-First"))
	assert.Equal(t, "two", resp.Header.Get("X-Second"))
}

// HSTS

func TestHSTS_defaultHeaderHasOneYearAndSubdomains(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.HSTS())
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	got := resp.Header.Get("Strict-Transport-Security")
	assert.Equal(t, "max-age=31536000; includeSubDomains", got)
}

func TestHSTS_optionsCustomiseHeader(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.HSTS(
		via.HSTSMaxAge(60*60*24*30), // 30 days
		via.HSTSIncludeSubdomains(false),
		via.HSTSPreload(true),
	))
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	got := resp.Header.Get("Strict-Transport-Security")
	assert.Equal(t, "max-age=2592000; preload", got,
		"options should produce: 30d, no subdomains, with preload")
}

// RedirectHTTPS

func TestRedirectHTTPS_passesHTTPSThroughViaXForwardedProto(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.RedirectHTTPS())
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"X-Forwarded-Proto: https should pass through unredirected")
}

func TestRedirectHTTPS_redirectsPlainHTTP(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.RedirectHTTPS())
	app.HandleFunc("/path", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(server.URL + "/path?q=1")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	loc := resp.Header.Get("Location")
	assert.True(t, len(loc) >= 8 && loc[:8] == "https://",
		"redirect Location should start with https://, got %q", loc)
	assert.Contains(t, loc, "/path?q=1")
}
