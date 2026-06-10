package via_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type actRecoverPage struct{}

func (p *actRecoverPage) Bump(ctx *via.Ctx)      {}
func (p *actRecoverPage) View(ctx *via.CtxR) h.H { return h.Div(on.Click(p.Bump)) }

const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// An action routed to a pod that doesn't hold the tab's Ctx (non-sticky LB, TTL
// sweep, restart) used to 404 — silently dropping the click — while the SSE path
// already re-bootstraps the same stale tab. That asymmetry leaves a wrong-pod
// click dead. A recoverable (well-formed, mounted-route) tab must instead get a
// reload directive so a fresh page GET re-bootstraps it, matching the SSE path.
func TestActionRecover_unknownButRecoverableTabReloads(t *testing.T) {
	t.Parallel()

	m := &captureMetrics{}
	app := via.New(via.WithMetrics(m))
	server := vt.Serve(t, app)
	via.Mount[actRecoverPage](app, "/")

	// "/_<64hex>" is genTabID's shape for the "/" route: well-formed and
	// owned by a mounted route, but never registered on this fresh app.
	body := strings.NewReader(`{"via_tab":"/_` + hex64 + `"}`)
	resp, err := server.Client().Post(server.URL+"/_action/Bump", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"a recoverable wrong-pod action must not 404")
	assert.Contains(t, string(raw), "location.reload",
		"it must push a reload so a fresh page GET re-bootstraps the tab")

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Contains(t, m.counters, "via.action.recover:mode,reload",
		"the action recovery must be observable as a metric")
}

// A forged / garbage tab id (no mounted route prefix) must keep the historical
// 404 — junk traffic must never trigger recovery work or mint contexts.
func TestActionRecover_forgedTabStill404s(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[actRecoverPage](app, "/")

	body := strings.NewReader(`{"via_tab":"no/such/route_` + hex64 + `"}`)
	resp, err := server.Client().Post(server.URL+"/_action/Bump", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"a tab id naming no mounted route is forged — still 404")
}
