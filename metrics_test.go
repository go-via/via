package via_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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
	N via.StateTab[int]
}

func (p *metricsPage) Bump(ctx *via.Ctx) error {
	p.N.Set(ctx, p.N.Get(ctx)+1)
	return nil
}

func (p *metricsPage) View(ctx *via.Ctx) h.H { return h.Div() }

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
