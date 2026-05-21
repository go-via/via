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
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/mw"
	"github.com/go-via/via/vt"
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

func (p *erroringPage) View(ctx *via.CtxR) h.H { return h.Div() }

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

	tc := vt.NewClient(t, server, "/")
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
func (p *panicPage) View(ctx *via.CtxR) h.H  { return h.Div() }

type accessLogPage struct{}

func (p *accessLogPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestLogLevel_warnDefault_noNoiseOnHealthyRequest(t *testing.T) {
	t.Parallel()

	// LogLevel defaults to LogWarn — no info/debug records should leak.
	app, server, logger := newLoggedApp(t, via.LogWarn)
	mw.Defaults(app)
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

func (p *ridLogPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestLog_includesRequestIDFromCtxRequest(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogInfo)
	app.Use(mw.RequestID())
	via.Mount[ridLogPage](app, "/")

	tc := vt.NewClient(t, server, "/")
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

type leakyLogPage struct{}

func (p *leakyLogPage) Start(ctx *via.Ctx) error {
	// Goroutine outlives the action: by the time it runs, runAction's
	// exit defer is racing to clear ctx.r under ctx.mu. via.Log reading
	// ctx.r without that lock would be flagged by the race detector.
	go func() {
		for range 200 {
			via.Log(ctx).Log(via.LogInfo, "from-goroutine")
		}
	}()
	return nil
}

func (p *leakyLogPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestLog_isRaceFreeWhenCalledOffActionGoroutine(t *testing.T) {
	t.Parallel()

	app, server, _ := newLoggedApp(t, via.LogInfo)
	app.Use(mw.RequestID())
	via.Mount[leakyLogPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	// Fire many actions back to back so each entry/exit write of ctx.r
	// overlaps in time with leakyLogPage's still-running goroutines.
	for range 20 {
		require.Equal(t, 200, tc.Action("Start").Fire())
	}
	// Drain time for the spawned goroutines so the race window stays open.
	time.Sleep(100 * time.Millisecond)
}

type loggingPage struct{}

func (p *loggingPage) DoIt(ctx *via.Ctx) error {
	via.Log(ctx).Log(via.LogInfo, "checkout", "amount", 42)
	return nil
}

func (p *loggingPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestLog_emitsThroughConfiguredLoggerWithTabContext(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogInfo)
	via.Mount[loggingPage](app, "/")

	tc := vt.NewClient(t, server, "/")
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

	tc := vt.NewClient(t, server, "/")
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

	tc := vt.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Boom").Fire())

	out := buf.String()
	require.Contains(t, out, `"level":"ERROR"`)
	require.Contains(t, out, `"msg":"action \"Boom\" panicked: boom"`)
	require.Contains(t, out, `"via_tab":`)
}
