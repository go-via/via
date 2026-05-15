package via_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureLogger struct {
	mu      sync.Mutex
	records []logRec
}

type logRec struct {
	level via.LogLevel
	msg   string
	kv    []any
}

func (c *captureLogger) Log(level via.LogLevel, msg string, kv ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, logRec{level: level, msg: msg, kv: slices.Clone(kv)})
}

func (c *captureLogger) snapshot() []logRec {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.records)
}

type erroringPage struct{}

func (p *erroringPage) Boom(ctx *via.Ctx) error {
	return assertError("kaboom")
}

func (p *erroringPage) View(ctx *via.Ctx) h.H { return h.Div() }

type assertError string

func (e assertError) Error() string { return string(e) }

// newLoggedApp wires a captureLogger + httptest.Server onto a fresh App,
// applies any extra options, and registers a t.Cleanup so callers don't
// have to track server.Close themselves.
func newLoggedApp(t *testing.T, level via.LogLevel, opts ...via.Option) (*via.App, *httptest.Server, *captureLogger) {
	t.Helper()
	logger := &captureLogger{}
	var server *httptest.Server
	full := append([]via.Option{
		via.WithLogger(logger),
		via.WithLogLevel(level),
		via.WithTestServer(&server),
	}, opts...)
	app := via.New(full...)
	t.Cleanup(func() { server.Close() })
	return app, server, logger
}

func TestWithLogger_routesActionPanicsThroughLogger(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogDebug)
	via.Mount[panicPage](app, "/")

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Boom").Fire())

	recs := logger.snapshot()
	require.NotEmpty(t, recs, "panic should have produced a log record")

	found := false
	for _, r := range recs {
		if r.level == via.LogError && strings.Contains(r.msg, "panicked") {
			found = true
			// kv should include via_tab=<tabID>
			require.GreaterOrEqual(t, len(r.kv), 2)
			assert.Equal(t, "via_tab", r.kv[0])
			break
		}
	}
	assert.True(t, found, "expected an error-level record mentioning panicked")
}

type panicPage struct{}

func (p *panicPage) Boom(ctx *via.Ctx) error { panic("boom") }
func (p *panicPage) View(ctx *via.Ctx) h.H   { return h.Div() }

type accessLogPage struct{}

func (p *accessLogPage) View(ctx *via.Ctx) h.H { return h.Div() }

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

func TestLogLevel_warnDefault_noNoiseOnHealthyRequest(t *testing.T) {
	t.Parallel()

	// LogLevel defaults to LogWarn — no info/debug records should leak.
	app, server, logger := newLoggedApp(t, via.LogWarn)
	via.Defaults(app)
	via.Mount[accessLogPage](app, "/")

	for range 5 {
		resp, err := http.Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
	}

	// Healthy renders + the AccessLog info records they produce should
	// be filtered out at WarnLevel; the captureLogger ends empty.
	for _, r := range logger.snapshot() {
		if r.level < via.LogWarn {
			t.Errorf("unexpected info/debug record leaked at WarnLevel default: %+v", r)
		}
	}
}

type ridLogPage struct{}

func (p *ridLogPage) Trace(ctx *via.Ctx) error {
	via.Log(ctx).Log(via.LogInfo, "doing-it")
	return nil
}

func (p *ridLogPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestLog_includesRequestIDFromCtxRequest(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogInfo)
	app.Use(via.RequestID())
	via.Mount[ridLogPage](app, "/")

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Trace").Fire())

	// The action's Log call should have appended both via_tab and rid.
	got := false
	for _, r := range logger.snapshot() {
		if r.msg != "doing-it" {
			continue
		}
		// kv has via_tab=…, rid=…, then any user kv. Check rid present.
		for i := 0; i+1 < len(r.kv); i += 2 {
			if r.kv[i] == "rid" && r.kv[i+1].(string) != "" {
				got = true
			}
		}
	}
	assert.True(t, got, "via.Log should include rid when RequestID middleware ran")
}

func TestRequestID_generatesWhenAbsent(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.RequestID())
	via.Mount[accessLogPage](app, "/")
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
	via.Mount[accessLogPage](app, "/")
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.Header.Set("X-Request-ID", "my-trace-123")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "my-trace-123", resp.Header.Get("X-Request-ID"),
		"inbound X-Request-ID should round-trip back unchanged")
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

type loggingPage struct{}

func (p *loggingPage) DoIt(ctx *via.Ctx) error {
	via.Log(ctx).Log(via.LogInfo, "checkout", "amount", 42)
	return nil
}

func (p *loggingPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestLog_emitsThroughConfiguredLoggerWithTabContext(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogInfo)
	via.Mount[loggingPage](app, "/")

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("DoIt").Fire())

	recs := logger.snapshot()
	var got *logRec
	for i := range recs {
		if recs[i].msg == "checkout" {
			got = &recs[i]
			break
		}
	}
	require.NotNil(t, got, "via.Log(ctx).Log should reach the configured logger")
	require.Equal(t, via.LogInfo, got.level)
	require.GreaterOrEqual(t, len(got.kv), 4,
		"kv should include via_tab and amount=42")
	assert.Equal(t, "via_tab", got.kv[0])
	assert.Equal(t, "amount", got.kv[2])
	assert.Equal(t, 42, got.kv[3])
}

func TestLog_respectsLogLevelFilter(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogWarn)
	via.Mount[loggingPage](app, "/")

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("DoIt").Fire())

	recs := logger.snapshot()
	for _, r := range recs {
		if r.msg == "checkout" {
			t.Fatal("checkout (LogInfo) record should be filtered out at LogWarn level")
		}
	}
}

func TestSlogLogger_routesRecordsToProvidedSlog(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	sl := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var server *httptest.Server
	app := via.New(
		via.WithLogger(via.SlogLogger(sl)),
		via.WithLogLevel(via.LogDebug),
		via.WithTestServer(&server),
	)
	via.Mount[panicPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Boom").Fire())

	out := buf.String()
	require.Contains(t, out, `"level":"ERROR"`)
	require.Contains(t, out, `"msg":"action \"Boom\" panicked: boom"`)
	require.Contains(t, out, `"via_tab":`)
}
