package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/require"
)

type liveTabPage struct {
	N via.StateTabNum[int]
}

func (p *liveTabPage) Bump(ctx *via.Ctx) error {
	return p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil })
}

func (p *liveTabPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.ID("n"), p.N.Text(ctx))
}

func TestSSE_connectedTabSurvivesContextTTLWithoutHeartbeat(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithSSEHeartbeat(0),                 // floors the keepalive; won't fire in-window
		via.WithContextTTL(80*time.Millisecond), // sweep ticks every 40ms
	)
	via.Mount[liveTabPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	// Sit idle well past several TTL sweeps with no patch traffic and no
	// keepalive tick (floored to 25s, far longer than this window) —
	// liveness must come from the open SSE connection (Ctx.connected), not
	// from a timer refreshing lastAccess.
	time.Sleep(400 * time.Millisecond)

	// A swept ctx would have closed this tab's stream and 404'd the action.
	require.Equal(t, http.StatusOK, tc.Action("Bump").Fire(),
		"a connected tab must not be TTL-swept while its SSE stream is live")
	vt.AwaitFrame(t, frames, 2*time.Second, ">1<")
}
