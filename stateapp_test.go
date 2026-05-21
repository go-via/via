package via_test

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// StateApp is shared across sessions: a write from one client surfaces
// in a fresh client's render.

type appCounterPage struct {
	Visits via.StateAppNum[int]
}

func (p *appCounterPage) Bump(ctx *via.Ctx) error {
	p.Visits.Update(ctx, func(n int) int { return n + 1 })
	return nil
}

func (p *appCounterPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Span(h.ID("visits"), p.Visits.Text(ctx)))
}

func TestApp_writesAreVisibleAcrossSessions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[appCounterPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	require.Equal(t, 200, a.Action("Bump").Fire())
	require.Equal(t, 200, a.Action("Bump").Fire())

	// Fresh client (different session) must see the app-scoped value.
	b := vt.NewClient(t, server, "/")
	body := b.HTML()
	assert.Contains(t, body, `<span id="visits">2</span>`,
		"StateApp value must be shared across sessions")
}

type silentAppPage struct {
	// Same wireKey "visits" as appCounterPage, but the View never reads
	// it — used to prove that broadcasts skip non-displaying tabs.
	Visits via.StateAppNum[int]
}

func (p *silentAppPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Span(h.ID("mute"), h.Text("no readers here")))
}

func TestApp_writeWakesOnlyTabsThatReadTheKey(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[appCounterPage](app, "/reader")
	via.Mount[silentAppPage](app, "/silent")
	defer server.Close()

	reader := vt.NewClient(t, server, "/reader")
	silent := vt.NewClient(t, server, "/silent")

	framesR, cancelR := reader.SSE()
	defer cancelR()
	framesS, cancelS := silent.SSE()
	defer cancelS()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, reader.Action("Bump").Fire())

	vt.AwaitFrame(t, framesR, 2*time.Second, `<span id="visits">1</span>`)

	// Heartbeat default is 25s — any frame inside this window can only
	// come from an unintended re-render of a tab that does not display
	// the key.
	select {
	case frame := <-framesS:
		assert.Failf(t, "non-reader peer was woken",
			"StateApp write must skip tabs whose View did not read the key; got %q", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestApp_writePropagatesLiveToEveryOtherTab(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[appCounterPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	b := vt.NewClient(t, server, "/")

	framesB, cancelB := b.SSE()
	defer cancelB()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, a.Action("Bump").Fire())
	vt.AwaitFrame(t, framesB, 2*time.Second, `<span id="visits">1</span>`)
}

func TestApp_concurrentUpdatesDoNotLoseIncrements(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[appCounterPage](app, "/")
	defer server.Close()

	const writers = 4
	const perWriter = 50

	clients := make([]*vt.Client, writers)
	for i := range clients {
		clients[i] = vt.NewClient(t, server, "/")
	}

	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *vt.Client) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				require.Equal(t, 200, c.Action("Bump").Fire())
			}
		}(c)
	}
	wg.Wait()

	final := vt.NewClient(t, server, "/")
	assert.Contains(t, final.HTML(), `<span id="visits">200</span>`,
		"concurrent Update calls across sessions must converge to the exact final count")
}
