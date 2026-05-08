package via_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	c.records = append(c.records, logRec{level: level, msg: msg, kv: append([]any(nil), kv...)})
}

func (c *captureLogger) snapshot() []logRec {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]logRec, len(c.records))
	copy(out, c.records)
	return out
}

type erroringPage struct{}

func (p *erroringPage) Boom(ctx *via.Ctx) error {
	return assertError("kaboom")
}

func (p *erroringPage) View(ctx *via.Ctx) h.H { return h.Div() }

type assertError string

func (e assertError) Error() string { return string(e) }

func TestWithLogger_routesActionPanicsThroughLogger(t *testing.T) {
	t.Parallel()

	cap := &captureLogger{}
	var server *httptest.Server
	app := via.New(
		via.WithLogger(cap),
		via.WithLogLevel(via.LogDebug),
		via.WithTestServer(&server),
	)
	via.Mount[panicPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Boom").Fire())

	recs := cap.snapshot()
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

func TestAccessLog_emitsOneRecordPerRequest(t *testing.T) {
	t.Parallel()

	cap := &captureLogger{}
	var server *httptest.Server
	app := via.New(
		via.WithLogger(cap),
		via.WithLogLevel(via.LogInfo),
		via.WithTestServer(&server),
	)
	app.Use(via.AccessLog(app))
	via.Mount[accessLogPage](app, "/")
	defer server.Close()

	for i := 0; i < 3; i++ {
		resp, err := http.Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
	}

	got := 0
	for _, r := range cap.snapshot() {
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

	cap := &captureLogger{}
	var server *httptest.Server
	app := via.New(
		via.WithLogger(cap),
		via.WithLogLevel(via.LogInfo),
		via.WithTestServer(&server),
	)
	via.Mount[loggingPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("DoIt").Fire())

	recs := cap.snapshot()
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

	cap := &captureLogger{}
	var server *httptest.Server
	app := via.New(
		via.WithLogger(cap),
		via.WithLogLevel(via.LogWarn),
		via.WithTestServer(&server),
	)
	via.Mount[loggingPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("DoIt").Fire())

	recs := cap.snapshot()
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
