package via_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// StateTab/Ctx live in this pod's memory, so an action routed to a pod that
// doesn't hold the tab's Ctx 404s — the silent failure of running without
// sticky sessions. A well-formed via_tab this pod never registered is that
// exact symptom, and it must emit a metric so operators can detect non-sticky
// load-balancer routing instead of staring at mysterious 404s.
func TestStickyDetect_unknownTabEmitsMetric(t *testing.T) {
	t.Parallel()

	m := &captureMetrics{}
	app := via.New(via.WithMetrics(m))
	server := vt.Serve(t, app)
	via.Mount[metricsPage](app, "/")

	body := strings.NewReader(`{"via_tab":"0123456789abcdef0123456789abcdef"}`)
	resp, err := server.Client().Post(server.URL+"/_action/Bump", "application/json", body)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"an action for a tab this pod doesn't hold must 404")

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Contains(t, m.counters, "via.tab.unknown:kind,action",
		"an action for an unknown but well-formed tab must emit via.tab.unknown")
}

// A request with no via_tab at all is a malformed probe, not a routing
// symptom — it must NOT inflate the sticky-routing signal.
func TestStickyDetect_missingTabDoesNotEmitMetric(t *testing.T) {
	t.Parallel()

	m := &captureMetrics{}
	app := via.New(via.WithMetrics(m))
	server := vt.Serve(t, app)
	via.Mount[metricsPage](app, "/")

	body := strings.NewReader(`{}`)
	resp, err := server.Client().Post(server.URL+"/_action/Bump", "application/json", body)
	require.NoError(t, err)
	resp.Body.Close()

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.NotContains(t, m.counters, "via.tab.unknown:kind,action",
		"a request with no via_tab must not be counted as a wrong-pod routing event")
}
