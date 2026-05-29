package via_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureMetrics struct {
	mu         sync.Mutex
	counters   []string
	histograms []string
	gauges     []string
}

func (c *captureMetrics) Counter(name string, labels ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counters = append(c.counters, name+":"+joinLabels(labels))
}

func (c *captureMetrics) Histogram(name string, _ float64, labels ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.histograms = append(c.histograms, name+":"+joinLabels(labels))
}

func (c *captureMetrics) Gauge(name string, _ float64, labels ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gauges = append(c.gauges, name+":"+joinLabels(labels))
}

func joinLabels(labels []string) string {
	out := ""
	for i, l := range labels {
		if i > 0 {
			out += ","
		}
		out += l
	}
	return out
}

type metricsPage struct {
	N via.StateTabNum[int]
}

func (p *metricsPage) Bump(ctx *via.Ctx) error {
	p.N.Write(ctx, p.N.Read(ctx)+1)
	return nil
}

func (p *metricsPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestMetrics_emitsActionAndRenderEvents(t *testing.T) {
	t.Parallel()
	// The hook is the only seam ops integrations have — pin the event
	// names and label shape so a Prometheus/OTel adapter built against
	// this contract doesn't silently break on a renamed key.
	m := &captureMetrics{}
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server), via.WithMetrics(m))
	via.Mount[metricsPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	require.Equal(t, http.StatusOK, tc.Action("Bump").Fire())

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Contains(t, m.counters, "via.render.total:route,/",
		"page render must emit via.render.total with route label")
	assert.Contains(t, m.counters, "via.action.total:method,Bump",
		"action POST must emit via.action.total with method label")
	assert.Contains(t, m.histograms, "via.action.latency:method,Bump",
		"action latency histogram must include method label")
	// At least one Gauge update for via.ctx.live (register fires on the GET).
	found := false
	for _, g := range m.gauges {
		if g == "via.ctx.live:" {
			found = true
			break
		}
	}
	assert.True(t, found, "via.ctx.live gauge must fire on tab register")
}

func TestMetrics_emitsSSEConnectAndDisconnect(t *testing.T) {
	t.Parallel()
	// The action/render test never opens an SSE stream, so the documented
	// sse.connect / sse.disconnect lifecycle counters need their own pass.
	m := &captureMetrics{}
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server), via.WithMetrics(m))
	via.Mount[metricsPage](app, "/")
	defer server.Close()

	hasCounter := func(name string) bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return slices.Contains(m.counters, name)
	}

	tc := vt.NewClient(t, server, "/")
	_, cancel := tc.SSEReady()

	require.Eventually(t, func() bool { return hasCounter("via.sse.connect:") },
		2*time.Second, 10*time.Millisecond,
		"via.sse.connect must fire when the SSE stream opens")

	// Closing the stream client-side runs runSSEStream's deferred
	// disconnect counter. The request context cancels, so the loop exits
	// via sse.Context().Done() and the documented "client" reason label
	// is emitted (see the catalogue in metrics.go).
	cancel()
	require.Eventually(t, func() bool { return hasCounter("via.sse.disconnect:reason,client") },
		2*time.Second, 10*time.Millisecond,
		"via.sse.disconnect must fire with reason=client when the client closes the stream")
}

func TestMetrics_SSEDisconnectReason_shutdown(t *testing.T) {
	t.Parallel()
	// App.Shutdown disposes every live Ctx, which wakes the SSE drain
	// loop on <-ctx.doneChan. The documented contract labels that exit
	// "shutdown" so an ops dashboard can separate graceful shutdowns
	// from client navigations. Without the fix the counter is emitted
	// label-less and this assertion fails.
	m := &captureMetrics{}
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server), via.WithMetrics(m))
	via.Mount[metricsPage](app, "/")
	defer server.Close()

	hasCounter := func(name string) bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return slices.Contains(m.counters, name)
	}

	tc := vt.NewClient(t, server, "/")
	_, cancel := tc.SSEReady()
	defer cancel()

	require.Eventually(t, func() bool { return hasCounter("via.sse.connect:") },
		2*time.Second, 10*time.Millisecond,
		"via.sse.connect must fire when the SSE stream opens")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutCancel()
	require.NoError(t, app.Shutdown(shutCtx))

	require.Eventually(t, func() bool { return hasCounter("via.sse.disconnect:reason,shutdown") },
		2*time.Second, 10*time.Millisecond,
		"via.sse.disconnect must fire with reason=shutdown when the app shuts down")
}

func TestMetrics_SSEDisconnectReason_ttl(t *testing.T) {
	t.Parallel()
	// The idle-TTL sweep evicts a Ctx that has gone quiet and disposes
	// it, waking the SSE drain loop on <-ctx.doneChan. The documented
	// contract labels that exit "ttl". A short TTL keeps the test quick;
	// the default 25s heartbeat never fires within the window, so the
	// stream stays idle long enough to be swept.
	m := &captureMetrics{}
	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithMetrics(m),
		via.WithContextTTL(40*time.Millisecond),
	)
	via.Mount[metricsPage](app, "/")
	defer server.Close()
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutCancel()
		_ = app.Shutdown(shutCtx)
	}()

	hasCounter := func(name string) bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return slices.Contains(m.counters, name)
	}

	tc := vt.NewClient(t, server, "/")
	_, cancel := tc.SSEReady()
	defer cancel()

	require.Eventually(t, func() bool { return hasCounter("via.sse.disconnect:reason,ttl") },
		2*time.Second, 10*time.Millisecond,
		"via.sse.disconnect must fire with reason=ttl when the idle-TTL sweep evicts the Ctx")
}
