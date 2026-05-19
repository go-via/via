package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
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

func TestAccessLog_statusWriterForwardsFlush(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.AccessLog(app))
	app.HandleFunc("/stream", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: a\n\n"))
		f, ok := w.(http.Flusher)
		require.True(t, ok,
			"the AccessLog-wrapped writer must expose http.Flusher; "+
				"SSE handlers rely on it to push frames in real time")
		f.Flush()
		w.Write([]byte("data: b\n\n"))
		f.Flush()
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/stream")
	require.NoError(t, err)
	defer resp.Body.Close()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	assert.Contains(t, string(buf[:n]), "data: a",
		"the first chunk must arrive after Flush, before the handler returns")
}

func TestRecover_panicAfterPartialWriteKeepsServerAlive(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.Recover(app))
	app.HandleFunc("/half", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("after-write")
	})
	app.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("alive"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/half")
	require.NoError(t, err)
	body := readAll(t, resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"headers already flushed → Recover cannot rewrite to 500")
	assert.Contains(t, body, "partial")

	resp2, err := http.Get(server.URL + "/ok")
	require.NoError(t, err)
	body2 := readAll(t, resp2.Body)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode,
		"server should survive panic after partial write")
	assert.Equal(t, "alive", body2)
}

type ridProbePage struct{}

func (p *ridProbePage) View(*via.Ctx) h.H { return h.Div() }

func TestRequestID_generatesWhenAbsent(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.RequestID())
	via.Mount[ridProbePage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	rid := resp.Header.Get("X-Request-ID")
	assert.NotEmpty(t, rid, "RequestID middleware should generate an id")
	assert.GreaterOrEqual(t, len(rid), 22)
}

func TestRequestID_passesThroughInboundHeader(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.RequestID())
	via.Mount[ridProbePage](app, "/")
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.Header.Set("X-Request-ID", "my-trace-123")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "my-trace-123", resp.Header.Get("X-Request-ID"),
		"inbound X-Request-ID should round-trip back unchanged")
}

func TestDefaults_installsRecoverRequestIDAndAccessLog(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogInfo)
	via.Defaults(app)
	app.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("oops")
	})
	app.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// Recover survives the panic.
	resp, err := http.Get(server.URL + "/boom")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// RequestID stamps a header.
	resp2, err := http.Get(server.URL + "/ok")
	require.NoError(t, err)
	resp2.Body.Close()
	assert.NotEmpty(t, resp2.Header.Get("X-Request-ID"))

	// AccessLog logs both, including rid.
	logs := logger.snapshot()
	sawAccess := 0
	for _, r := range logs {
		if strings.Contains(r.msg, "rid=") &&
			(strings.Contains(r.msg, "/boom") || strings.Contains(r.msg, "/ok")) {
			sawAccess++
		}
	}
	assert.GreaterOrEqual(t, sawAccess, 2,
		"AccessLog should record both requests with rid")
}

func TestRecover_panicReturns500AndKeepsServerAlive(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogError)
	app.Use(via.Recover(app))
	app.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	})
	app.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	resp, err := http.Get(server.URL + "/boom")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"panicking handler should produce 500")

	// Subsequent requests still work.
	resp2, err := http.Get(server.URL + "/ok")
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode,
		"server should survive the panic")

	logged := false
	for _, r := range logger.snapshot() {
		if r.level == via.LogError && strings.Contains(r.msg, "panic") {
			logged = true
		}
	}
	assert.True(t, logged, "Recover should log an error record on panic")
}

func TestAccessLog_includesRequestIDWhenPresent(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogInfo)
	app.Use(via.RequestID())
	app.Use(via.AccessLog(app))
	via.Mount[accessLogPage](app, "/")

	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.Header.Set("X-Request-ID", "trace-7")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	found := false
	for _, r := range logger.snapshot() {
		if strings.Contains(r.msg, "rid=trace-7") {
			found = true
		}
	}
	assert.True(t, found, "AccessLog should include rid=… when RequestID middleware ran")
}

func TestAccessLog_emitsOneRecordPerRequest(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogInfo)
	app.Use(via.AccessLog(app))
	via.Mount[accessLogPage](app, "/")

	for range 3 {
		resp, err := http.Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
	}

	got := 0
	for _, r := range logger.snapshot() {
		if strings.Contains(r.msg, "GET /") &&
			strings.Contains(r.msg, "status=200") {
			got++
		}
	}
	assert.Equal(t, 3, got, "AccessLog should emit one record per request")
}

func TestRedirectHTTPSStrict_ignoresXForwardedProto(t *testing.T) {
	t.Parallel()
	// Direct-bind variant: a forged X-Forwarded-Proto: https header must
	// NOT bypass the redirect — only real TLS does. Catches a regression
	// where Strict accidentally fell back to header sniffing.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.RedirectHTTPSStrict())
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("served"))
	})
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.Header.Set("X-Forwarded-Proto", "https") // forged header
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode,
		"Strict variant must ignore X-Forwarded-Proto and still redirect")
}
