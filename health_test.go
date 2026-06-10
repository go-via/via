package via_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Orchestrators (k8s, load balancers) need liveness/readiness probes out of the
// box; without defaults every team rolls their own. /livez and /healthz report
// the process is up; /readyz additionally reports whether the app can take new
// traffic.
func TestHealth_livenessAndHealthzReturn200(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)

	for _, path := range []string{"/livez", "/healthz", "/readyz"} {
		resp, err := server.Client().Get(server.URL + path)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"%s must report healthy on a running app", path)
	}
}

// Readiness must flip to 503 once Shutdown begins draining, so the orchestrator
// stops routing new traffic to a pod that is going away — while liveness stays
// 200 so the pod isn't force-killed mid-drain.
func TestHealth_readinessFailsWhileDraining(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)

	require.NoError(t, app.Shutdown(context.Background()))

	resp, err := server.Client().Get(server.URL + "/readyz")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
		"/readyz must report not-ready once the app is draining")

	live, err := server.Client().Get(server.URL + "/livez")
	require.NoError(t, err)
	live.Body.Close()
	assert.Equal(t, http.StatusOK, live.StatusCode,
		"/livez must stay healthy during drain so the pod isn't force-killed")
}

// The endpoints are conventional infra paths; an app that needs to own them
// can opt out and serve its own.
func TestHealth_optOutLeavesPathsUnclaimed(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithoutHealthEndpoints())
	server := vt.Serve(t, app)

	resp, err := server.Client().Get(server.URL + "/livez")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"with health endpoints disabled, /livez must not be served by via")
}
