package via_test

import (
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

type evtCounterPage struct {
	Hits via.StateAppCounter
}

func (p *evtCounterPage) Bump(ctx *via.Ctx) { p.Hits.Inc(ctx) }

func (p *evtCounterPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Span(h.ID("n"), p.Hits.Text(ctx)))
}

// A counter with no increments must read as the zero count — the empty event
// log projects to int64(0), not a blank node or a panic.
func TestCounterReadsZeroBeforeAnyIncrement(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[evtCounterPage](app, "/")
	defer server.Close()

	c := vt.NewClient(t, server, "/")
	assert.Contains(t, c.HTML(), `<span id="n">0</span>`,
		"an un-incremented counter reads as 0")
}

// Inc is the whole point: each increment raises the shared, app-wide count by
// exactly one, and the total is visible to a brand-new session (the count lives
// in the backplane, not the tab).
func TestEachIncrementRaisesTheSharedCountByOne(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[evtCounterPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	const n = 3
	for range n {
		require.Equal(t, 200, a.Action("Bump").Fire())
	}

	require.Eventually(t, func() bool {
		fresh := vt.NewClient(t, server, "/")
		return strings.Contains(fresh.HTML(), `<span id="n">3</span>`)
	}, 2*time.Second, 20*time.Millisecond,
		"three Inc calls must accumulate to 3 for a fresh session")
}

// Inc is reachable only from an action ctx; a nil ctx means the call did not
// come from a legitimate tab action, so it must panic rather than silently
// mutate the shared counter (the panic surfaces from the promoted Append).
func TestCounterIncPanicsOnNilCtx(t *testing.T) {
	t.Parallel()
	var c via.StateAppCounter
	assert.PanicsWithValue(t,
		"via: StateAppEvents.Append called with nil *Ctx",
		func() { c.Inc(nil) },
	)
}

// An increment in one tab must reach every other live tab — the projector fans
// the new count out over SSE, so a counter is genuinely shared in real time.
func TestIncrementReachesOtherLiveTabs(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[evtCounterPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	b := vt.NewClient(t, server, "/")

	framesB, cancelB := b.SSEReady()
	defer cancelB()

	require.Equal(t, 200, a.Action("Bump").Fire())
	vt.AwaitFrame(t, framesB, 2*time.Second, `<span id="n">1</span>`)
}
