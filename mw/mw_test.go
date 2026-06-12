package mw_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/mw"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// HSTS

func TestHSTS_defaultHeaderHasOneYearAndSubdomains(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.Use(mw.HSTS())
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	got := resp.Header.Get("Strict-Transport-Security")
	assert.Equal(t, "max-age=31536000; includeSubDomains", got)
}

func TestHSTS_optionsCustomiseHeader(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.Use(mw.HSTS(
		mw.HSTSMaxAge(60*60*24*30), // 30 days
		mw.HSTSIncludeSubdomains(false),
		mw.HSTSPreload(true),
	))
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	got := resp.Header.Get("Strict-Transport-Security")
	assert.Equal(t, "max-age=2592000; preload", got,
		"options should produce: 30d, no subdomains, with preload")
}

// RedirectHTTPS

func TestRedirectHTTPS_passesHTTPSThroughViaXForwardedProto(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.Use(mw.RedirectHTTPS())
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"X-Forwarded-Proto: https should pass through unredirected")
}

func TestRedirectHTTPS_redirectsPlainHTTP(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.Use(mw.RedirectHTTPS())
	app.HandleFunc("/path", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

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

func TestRedirectHTTPSStrict_ignoresXForwardedProto(t *testing.T) {
	t.Parallel()
	// Direct-bind variant: a forged X-Forwarded-Proto: https header must
	// NOT bypass the redirect — only real TLS does. Catches a regression
	// where Strict accidentally fell back to header sniffing.
	app := via.New()
	server := vt.Serve(t, app)
	app.Use(mw.RedirectHTTPSStrict())
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("served"))
	})

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

// CSP

func TestCSP_setsStrictPolicyWithNonceAndCallsNext(t *testing.T) {
	t.Parallel()

	var nextReq *http.Request
	rec := httptest.NewRecorder()
	mw.CSP()(rec, httptest.NewRequest("GET", "/", nil),
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { nextReq = r }))

	require.NotNil(t, nextReq, "CSP must invoke next")
	csp := rec.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "default-src 'self'")
	assert.Contains(t, csp, "object-src 'none'")
	assert.Contains(t, csp, "base-uri 'self'")
	assert.Contains(t, csp, "script-src 'self' 'nonce-")
}

func TestCSP_nonceIsBase64URLAndFreshPerRequest(t *testing.T) {
	t.Parallel()

	nonceOf := func() string {
		rec := httptest.NewRecorder()
		mw.CSP()(rec, httptest.NewRequest("GET", "/", nil),
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		csp := rec.Header().Get("Content-Security-Policy")
		const p = "'nonce-"
		i := strings.Index(csp, p)
		require.NotEqual(t, -1, i)
		rest := csp[i+len(p):]
		j := strings.Index(rest, "'")
		require.NotEqual(t, -1, j)
		return rest[:j]
	}

	n := nonceOf()
	require.GreaterOrEqual(t, len(n), 22,
		"16 bytes ≈ 22 url-safe base64 chars; got %q", n)
	for _, r := range n {
		ok := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		assert.True(t, ok, "nonce char %q must be url-safe base64", r)
	}
	assert.NotEqual(t, n, nonceOf(), "each request must get a fresh nonce")
}

func TestCSP_defaultPolicyIncludesUnsafeEval(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	mw.CSP()(rec, httptest.NewRequest("GET", "/", nil),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	assert.Regexp(t,
		`script-src 'self' 'nonce-[A-Za-z0-9_-]+' 'unsafe-eval'`,
		rec.Header().Get("Content-Security-Policy"),
		"the bundled Datastar runtime compiles data-* expressions via "+
			"Function(), which CSP gates behind 'unsafe-eval' in script-src")
}

func TestCSP_setsFrameAncestorsSelf(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	mw.CSP()(rec, httptest.NewRequest("GET", "/", nil),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	assert.Contains(t, rec.Header().Get("Content-Security-Policy"),
		"frame-ancestors 'self'")
}

func TestCSP_extraDirectivesAppended(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	mw.CSP("img-src 'self' data:")(rec, httptest.NewRequest("GET", "/", nil),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"),
		"; img-src 'self' data:")
}

// AccessLog

func TestAccessLog_statusWriterForwardsFlush(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.Use(mw.AccessLog(app))
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

	resp, err := server.Client().Get(server.URL + "/stream")
	require.NoError(t, err)
	defer resp.Body.Close()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	assert.Contains(t, string(buf[:n]), "data: a",
		"the first chunk must arrive after Flush, before the handler returns")
}

func TestAccessLog_includesRequestIDWhenPresent(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogInfo)
	app.Use(mw.RequestID())
	app.Use(mw.AccessLog(app))
	via.Mount[accessLogPage](app, "/")

	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.Header.Set("X-Request-ID", "trace-7")
	resp, _ := server.Client().Do(req)
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
	app.Use(mw.AccessLog(app))
	via.Mount[accessLogPage](app, "/")

	for range 3 {
		resp, err := server.Client().Get(server.URL + "/")
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

// TestAccessLog_stripsCRLFFromUserPath guards CWE-117: a request whose
// URL.Path contains \r\n must not be able to forge a new log line. The
// captured log record's message must be CRLF-free even though the raw
// path was not.
func TestAccessLog_stripsCRLFFromUserPath(t *testing.T) {
	t.Parallel()

	app, _, logger := newLoggedApp(t, via.LogInfo)
	access := mw.AccessLog(app)

	req := httptest.NewRequest("GET", "/legit", nil)
	req.URL.Path = "/legit\nFAKE [info] forged-entry"
	rec := httptest.NewRecorder()
	access(rec, req, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	recs := logger.snapshot()
	require.Len(t, recs, 1)
	assert.NotContains(t, recs[0].msg, "\n",
		"AccessLog must scrub LF from r.URL.Path (log injection)")
	assert.NotContains(t, recs[0].msg, "\r",
		"AccessLog must scrub CR from r.URL.Path (log injection)")
	assert.Contains(t, recs[0].msg, "/legitFAKE",
		"scrubbed path keeps the surrounding bytes, just without the line break")
}

// TestAccessLog_stripsCRLFFromRequestID guards CWE-117 on the rid
// suffix: an inbound X-Request-ID containing CR/LF must not break out
// into a new log line.
func TestAccessLog_stripsCRLFFromRequestID(t *testing.T) {
	t.Parallel()

	// net/http rejects CR/LF in inbound request headers at parse time, so we
	// can't drive the forged rid through an httptest.Server. Inject it via a
	// hand-built middleware that plants the id directly on ctx — same shape
	// RequestIDFrom would observe in production if any upstream middleware
	// (a custom rid extractor, a trace propagator) handed in an unscrubbed
	// value. Defense in depth: even though RequestID() itself is safe today,
	// the log sink must not trust whatever sits in ctx.
	app, _, logger := newLoggedApp(t, via.LogInfo)
	access := mw.AccessLog(app)

	req := via.RequestWithID(httptest.NewRequest("GET", "/", nil),
		"trace\nFAKE [info] forged")
	rec := httptest.NewRecorder()
	access(rec, req, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	recs := logger.snapshot()
	require.Len(t, recs, 1)
	assert.NotContains(t, recs[0].msg, "\n",
		"AccessLog must scrub LF from rid (log injection)")
	assert.NotContains(t, recs[0].msg, "\r",
		"AccessLog must scrub CR from rid (log injection)")
	assert.Contains(t, recs[0].msg, "rid=traceFAKE",
		"scrubbed rid keeps surrounding bytes minus the line break")
}

// Recover

func TestRecover_panicAfterPartialWriteKeepsServerAlive(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.Use(mw.Recover(app))
	app.HandleFunc("/half", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("after-write")
	})
	app.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("alive"))
	})

	resp, err := server.Client().Get(server.URL + "/half")
	require.NoError(t, err)
	body := readAll(t, resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"headers already flushed → Recover cannot rewrite to 500")
	assert.Contains(t, body, "partial")

	resp2, err := server.Client().Get(server.URL + "/ok")
	require.NoError(t, err)
	body2 := readAll(t, resp2.Body)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode,
		"server should survive panic after partial write")
	assert.Contains(t, body2, "alive")
}

func TestRecover_panicReturns500AndKeepsServerAlive(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogError)
	app.Use(mw.Recover(app))
	app.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	})
	app.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	resp, err := server.Client().Get(server.URL + "/boom")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"panicking handler should produce 500")

	// Subsequent requests still work.
	resp2, err := server.Client().Get(server.URL + "/ok")
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

// TestRecover_stripsCRLFFromUserPath guards CWE-117 on the panic-log
// path: a forged method/path must not break out into a new log line.
func TestRecover_stripsCRLFFromUserPath(t *testing.T) {
	t.Parallel()

	app, _, logger := newLoggedApp(t, via.LogError)
	rec := mw.Recover(app)

	req := httptest.NewRequest("GET", "/x", nil)
	req.Method = "GET\nINJECTED"
	req.URL.Path = "/x\nMORE"
	w := httptest.NewRecorder()
	rec(w, req, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	recs := logger.snapshot()
	require.NotEmpty(t, recs)
	for _, r := range recs {
		assert.NotContains(t, r.msg, "\n",
			"Recover must scrub LF from method/path before logging")
		assert.NotContains(t, r.msg, "\r",
			"Recover must scrub CR from method/path before logging")
	}
}

// RequestID

func TestRequestID_generatesWhenAbsent(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.Use(mw.RequestID())
	via.Mount[ridProbePage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	rid := resp.Header.Get("X-Request-ID")
	assert.NotEmpty(t, rid, "RequestID middleware should generate an id")
	assert.GreaterOrEqual(t, len(rid), 22)
}

func TestRequestID_passesThroughInboundHeader(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.Use(mw.RequestID())
	via.Mount[ridProbePage](app, "/")

	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.Header.Set("X-Request-ID", "my-trace-123")
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "my-trace-123", resp.Header.Get("X-Request-ID"),
		"inbound X-Request-ID should round-trip back unchanged")
}

// Defaults

func TestDefaults_installsRecoverRequestIDAndAccessLog(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogInfo)
	mw.Defaults(app)
	app.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("oops")
	})
	app.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// Recover survives the panic.
	resp, err := server.Client().Get(server.URL + "/boom")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// RequestID stamps a header.
	resp2, err := server.Client().Get(server.URL + "/ok")
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
