package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ttlGuardPage struct{}

func (p *ttlGuardPage) Ping(ctx *via.Ctx) error { return nil }

func (p *ttlGuardPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestContextSweep_disabledWhenTTLNotAboveHeartbeat(t *testing.T) {
	t.Parallel()

	logger := &captureLogger{}
	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithContextTTL(40*time.Millisecond),
		via.WithSSEHeartbeat(80*time.Millisecond), // TTL <= heartbeat → sweep disabled
		via.WithLogger(logger),
	)
	via.Mount[ttlGuardPage](app, "/")
	defer server.Close()

	warned := false
	for _, r := range logger.snapshot() {
		if r.level == via.LogWarn && strings.Contains(r.msg, "disabling the context-TTL sweep") {
			warned = true
		}
	}
	assert.True(t, warned, "a contextTTL <= sseHeartbeat misconfig must warn at startup")

	tc := vt.NewClient(t, server, "/")
	// Idle far past several would-be sweep ticks (interval = TTL/2 = 20ms). A
	// TTL no larger than the heartbeat can't be kept alive by it, so the sweep
	// is disabled rather than left to reap streams the heartbeat should hold.
	time.Sleep(300 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("Ping").Fire(),
		"contextTTL <= sseHeartbeat must disable the TTL sweep, so the Ctx is never reaped")
}
