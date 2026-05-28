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

type marshalUnfriendly struct {
	C chan int
}

type unmarshalablePage struct {
	Bad via.Signal[marshalUnfriendly]
}

func (p *unmarshalablePage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestWritePageDocument_marshalFailureStillRenders(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[unmarshalablePage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	body := readAll(t, resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// A typed Signal whose value can't be JSON-encoded must not poison
	// the whole page render; the document still ships, the bad signal
	// is just omitted from the initial data-signals payload.
	assert.Contains(t, body, "<div>")
}

type panicViewPage struct{}

func (p *panicViewPage) View(ctx *via.CtxR) h.H { panic("view boom") }

func TestView_panicReachesTheConfiguredLoggerNotBareStderr(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogError)
	via.Mount[panicViewPage](app, "/")

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	found := false
	for _, r := range logger.snapshot() {
		if r.level == via.LogError && strings.Contains(r.msg, "panicked") {
			found = true
			require.GreaterOrEqual(t, len(r.kv), 2)
			assert.Equal(t, "via_tab", r.kv[0])
			break
		}
	}
	assert.True(t, found,
		"a View panic must surface as a structured via log record, "+
			"not escape to the embedding http.Server")
}

func TestView_panicProducesControlled500NotADroppedConnection(t *testing.T) {
	t.Parallel()

	app, server, _ := newLoggedApp(t, via.LogError)
	via.Mount[panicViewPage](app, "/")

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err,
		"View panic must yield an HTTP response, not a dropped connection")
	body := readAll(t, resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	// The 500 must short-circuit the page document — a half-rendered
	// envelope appended after the error body would be a corrupt response.
	assert.NotContains(t, body, "<html",
		"recovered render must not also emit a partial page document")
}

type rerenderPanicPage struct {
	N via.StateTabNum[int]
}

func (p *rerenderPanicPage) Trip(ctx *via.Ctx) error {
	return p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil })
}

func (p *rerenderPanicPage) View(ctx *via.CtxR) h.H {
	// Initial render (N==0) succeeds so the page loads; the panic only
	// fires on the post-action re-render, which is the path under test.
	if p.N.Read(ctx) > 0 {
		panic("rerender boom")
	}
	return h.Div()
}

func TestView_panicDuringReRenderDoesNotEscapeToTheServer(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogError)
	via.Mount[rerenderPanicPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	assert.Equal(t, http.StatusOK, tc.Action("Trip").Fire(),
		"the action body succeeded; only the re-render panicked, "+
			"which must not drop the connection")

	found := false
	for _, r := range logger.snapshot() {
		if r.level == via.LogError && strings.Contains(r.msg, "panicked") {
			found = true
			break
		}
	}
	assert.True(t, found,
		"a View panic on the re-render path must surface as a via log record")
}

type rerenderPanicSignalPage struct {
	N   via.StateTabNum[int]
	Sig via.SignalNum[int] `via:"sig"`
}

func (p *rerenderPanicSignalPage) Trip(ctx *via.Ctx) error {
	_ = p.Sig.Update(ctx, func(int) (int, error) { return 7, nil })
	return p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil })
}

func (p *rerenderPanicSignalPage) View(ctx *via.CtxR) h.H {
	if p.N.Read(ctx) > 0 {
		panic("rerender boom")
	}
	return h.Div()
}

func TestView_panicDuringReRenderStillFlushesDirtySignals(t *testing.T) {
	t.Parallel()

	app, server, _ := newLoggedApp(t, via.LogError)
	via.Mount[rerenderPanicSignalPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("Trip").Fire())
	// The render panicked and was swallowed — but recovering the render
	// must not abandon the rest of the flush: the dirty signal still has
	// to reach the browser.
	vt.AwaitFrame(t, frames, 2*time.Second, `"sig":7`)
}

type broadcastPanicPage struct {
	Shared via.StateAppNum[int]
}

func (p *broadcastPanicPage) Bump(ctx *via.Ctx) error {
	return p.Shared.Update(ctx, func(n int) (int, error) { return n + 1, nil })
}

func (p *broadcastPanicPage) View(ctx *via.CtxR) h.H {
	// Read subscribes this tab to the app key so a peer's Update wakes it
	// for a broadcast re-render; the panic only fires once bumped.
	if p.Shared.Read(ctx) > 0 {
		panic("broadcast rerender boom")
	}
	return h.Div()
}

func TestView_panicInBroadcastReRenderIsRecoveredNotProcessCrashing(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogError)
	via.Mount[broadcastPanicPage](app, "/")

	// Two independent tabs share the app-scoped state. The peer re-renders
	// on its own SyncNow goroutine (broadcast.go), which has no action
	// handler defer to fall back on — an unrecovered panic there crashes
	// the whole process, so the fix must live inside flushDirty itself.
	peer := vt.NewClient(t, server, "/")
	peerFrames, cancel := peer.SSEReady()
	defer cancel()

	writer := vt.NewClient(t, server, "/")
	require.Equal(t, http.StatusOK, writer.Action("Bump").Fire())

	require.Eventually(t, func() bool {
		for _, r := range logger.snapshot() {
			if r.level == via.LogError && strings.Contains(r.msg, "panicked") {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond,
		"the broadcast goroutine's View panic must be recovered and logged")

	// The peer's SSE stream must still be alive (server didn't crash and
	// didn't tear the connection down on the swallowed render).
	require.Equal(t, http.StatusOK, peer.Action("Bump").Fire())
	_ = peerFrames
}
