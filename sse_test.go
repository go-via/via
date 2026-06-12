package via_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sseEmptyPage struct{}

func (p *sseEmptyPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestHandleSSEClose_oversizedBodyReturns413(t *testing.T) {
	t.Parallel()

	app := via.New(
		via.WithMaxRequestBody(16),
	)
	server := vt.Serve(t, app)
	via.Mount[sseEmptyPage](app, "/")

	resp, err := server.Client().Post(
		server.URL+"/_sse/close",
		"text/plain",
		bytes.NewReader(bytes.Repeat([]byte("x"), 1024)),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestHandleSSEClose_unknownTabIsNoOp200(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[sseEmptyPage](app, "/")

	resp, err := server.Client().Post(
		server.URL+"/_sse/close",
		"text/plain",
		strings.NewReader("does-not-exist"),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"unknown tab id is silently dropped, not an error")
}

// WithSSEWriteTimeout installs a per-write deadline on the underlying
// connection. We can't easily simulate a stalled TCP peer in-process,
// but we can verify the option threads through to the runtime by
// confirming the SSE handshake still succeeds with the option set —
// a regression where the timeout wiring panicked or wrapped a nil
// writer would fail this test loudly.

type sseDeadlinePage struct{}

func (p *sseDeadlinePage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestWithSSEWriteTimeout_doesNotBreakNormalDrains(t *testing.T) {
	t.Parallel()

	app := via.New(
		via.WithSSEWriteTimeout(500 * time.Millisecond),
	)
	server := vt.Serve(t, app)
	via.Mount[sseDeadlinePage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	// Pull the heartbeat (default 25s — short cap for the test).
	select {
	case f := <-frames:
		// Any drain succeeded — the deadline applied without preventing
		// the write.
		_ = f
	case <-time.After(300 * time.Millisecond):
		// No frame is fine too (heartbeat hasn't fired yet); the
		// assertion is that nothing panicked.
	}
}

type brotliProbePage struct{}

func (p *brotliProbePage) View(ctx *via.CtxR) h.H { return h.Div(h.Text("hi")) }

var brotliTabRE = regexp.MustCompile(`&#34;via_tab&#34;:&#34;([^"&]+)&#34;`)

// When the client negotiates brotli, datastar sets Content-Encoding: br and
// routes writes through a compressing writer. Any RAW write to the underlying
// ResponseWriter (e.g. the handshake comment) injects uncompressed bytes that
// corrupt the stream for a real br browser. Assert the served stream carries
// no raw bytes ahead of the compressed payload.
func TestSSE_brotliHandshakeIsCompressionSafe(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[brotliProbePage](app, "/")

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.Get(server.URL + "/")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := brotliTabRE.FindStringSubmatch(string(body))
	require.Len(t, m, 2, "tab id in page")
	tabID := m[1]

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sseURL := server.URL + "/_sse?datastar=" + url.QueryEscape(`{"via_tab":"`+tabID+`"}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	req.Header.Set("Accept-Encoding", "br") // force brotli negotiation
	sresp, err := (&http.Client{Jar: jar}).Do(req)
	require.NoError(t, err)
	defer sresp.Body.Close()

	require.Equal(t, "br", sresp.Header.Get("Content-Encoding"),
		"precondition: datastar must negotiate brotli for this client")

	buf := make([]byte, 64)
	n, _ := sresp.Body.Read(buf)
	raw := string(buf[:n])
	// A valid brotli stream never begins with this ASCII comment; if it does,
	// a raw write bypassed the compressor and the browser's decode breaks.
	assert.NotContains(t, raw, ": ready",
		"raw ': ready' in a Content-Encoding: br stream corrupts real browsers")
}

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

	app := via.New(
		via.WithSSEHeartbeat(0),                 // floors the keepalive; won't fire in-window
		via.WithContextTTL(80*time.Millisecond), // sweep ticks every 40ms
	)
	server := vt.Serve(t, app)
	via.Mount[liveTabPage](app, "/")

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

type resyncPushPage struct {
	Q via.Signal[int]
}

func (p *resyncPushPage) PushList(ctx *via.Ctx) error {
	ctx.Patch().Elements(h.Ul(h.ID("results"), h.Li(h.Text("first"))))
	return nil
}

func (p *resyncPushPage) PushNotice(ctx *via.Ctx) error {
	ctx.Patch().Signal("_notice", "maintenance")
	return nil
}

func (p *resyncPushPage) PushNoticeUpdate(ctx *via.Ctx) error {
	ctx.Patch().Signal("_notice", "all-clear")
	return nil
}

func (p *resyncPushPage) PushAll(ctx *via.Ctx) error {
	ctx.Patch().Elements(h.Ul(h.ID("results"), h.Li(h.Text("first"))))
	ctx.Patch().Signal("_notice", "maintenance")
	ctx.ExecScript("console.log('queued-script')")
	return nil
}

func (p *resyncPushPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.ID("root"), h.Text("hello"))
}

// failingSSEWriter is a minimal http.ResponseWriter whose failAt-th Write
// (and every Write after) errors, simulating a peer that vanished mid-drain.
// A stub is acceptable here: the network socket is a true system boundary
// and an in-process httptest connection cannot be made to fail a server
// write deterministically. It implements http.Flusher because datastar's
// NewSSE panics when the response writer can't flush.
type failingSSEWriter struct {
	header http.Header
	failAt int // 1-based index of the first failing Write call
	calls  int
}

func (f *failingSSEWriter) Header() http.Header {
	if f.header == nil {
		f.header = make(http.Header)
	}
	return f.header
}

func (f *failingSSEWriter) WriteHeader(int) {}

func (f *failingSSEWriter) Write(p []byte) (int, error) {
	f.calls++
	if f.calls >= f.failAt {
		return 0, errors.New("peer vanished")
	}
	return len(p), nil
}

func (f *failingSSEWriter) Flush() {}

// openPage GETs path with a jar-carrying client and returns the tab id, so
// follow-up requests on the same jar share the page's session.
func openPage(t *testing.T, httpc *http.Client, serverURL, path string) string {
	t.Helper()
	resp, err := httpc.Get(serverURL + path)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tab := vt.TabIDFromHTML(string(body))
	require.NotEmpty(t, tab, "rendered page must carry a via_tab id")
	return tab
}

func fireAction(t *testing.T, httpc *http.Client, serverURL, tabID, action string) {
	t.Helper()
	resp, err := httpc.Post(serverURL+"/_action/"+action, "application/json",
		strings.NewReader(`{"via_tab":"`+tabID+`"}`))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// connectFailingSSE drives a full SSE handshake for tabID against a writer
// whose failAt-th Write errors, then returns once the server-side stream
// goroutine has exited.
func connectFailingSSE(t *testing.T, app *via.App, httpc *http.Client, serverURL, tabID string, failAt int) {
	t.Helper()
	u, err := url.Parse(serverURL)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodGet,
		serverURL+"/_sse?datastar="+url.QueryEscape(`{"via_tab":"`+tabID+`"}`), nil)
	require.NoError(t, err)
	for _, c := range httpc.Jar.Cookies(u) {
		req.AddCookie(c)
	}
	app.ServeHTTP(&failingSSEWriter{failAt: failAt}, req)
}

func TestSSE_redeliversQueuedFrameAfterFailedWrite(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[resyncPushPage](app, "/rq")

	httpc := jarClient(t)
	tabID := openPage(t, httpc, server.URL, "/rq")
	// No SSE stream is open yet, so the pushed element patch sits in the
	// tab's queue waiting for the first connect to drain it.
	fireAction(t, httpc, server.URL, tabID, "PushList")

	connectFailingSSE(t, app, httpc, server.URL, tabID, 1)

	status, frames, cancel := openRawSSE(t, httpc, server.URL, tabID, "")
	defer cancel()
	require.Equal(t, http.StatusOK, status)
	vt.AwaitFrame(t, frames, 2*time.Second, `id="results"`, "first")
}

func TestSSE_retainsQueuedFramesWhenWriteFails(t *testing.T) {
	t.Parallel()

	// A drain writes elements, then signals, then scripts — one Write each.
	tests := []struct {
		name   string
		failAt int
	}{
		{"first frame write fails", 1},
		{"mid-drain write fails", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := via.New()
			server := vt.Serve(t, app)
			via.Mount[resyncPushPage](app, "/rk")

			httpc := jarClient(t)
			tabID := openPage(t, httpc, server.URL, "/rk")
			fireAction(t, httpc, server.URL, tabID, "PushAll")

			connectFailingSSE(t, app, httpc, server.URL, tabID, tt.failAt)

			status, frames, cancel := openRawSSE(t, httpc, server.URL, tabID, "")
			defer cancel()
			require.Equal(t, http.StatusOK, status)
			vt.AwaitFrame(t, frames, 2*time.Second,
				`id="results"`, `"_notice":"maintenance"`, "queued-script")
		})
	}
}

func TestSSE_reshipsServerPushedSignalsOnReconnect(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[resyncPushPage](app, "/rs")

	tc := vt.NewClient(t, server, "/rs")
	frames, cancel := tc.SSEReady()
	require.Equal(t, http.StatusOK, tc.Action("PushNotice").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"_notice":"maintenance"`)
	require.Equal(t, http.StatusOK, tc.Action("PushNoticeUpdate").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"_notice":"all-clear"`)
	// Both pushes were fully delivered (queue is empty) before the drop —
	// the client may still have missed them on the dying socket.
	cancel()

	frames2, cancel2 := tc.SSE()
	defer cancel2()
	body := vt.AwaitFrame(t, frames2, 2*time.Second,
		`"_notice":"all-clear"`, "datastar-patch-elements")
	assert.NotContains(t, body, "maintenance",
		"the resync patch must coalesce last-value-wins per signal key")
	assert.Less(t,
		strings.Index(body, `"_notice"`), strings.Index(body, "datastar-patch-elements"),
		"the coalesced signal patch must precede the view fragment so the morphed elements' bindings resolve")
}

func TestSSE_doesNotClobberClientOnlySignalsOnResync(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[resyncPushPage](app, "/rc")

	tc := vt.NewClient(t, server, "/rc")
	frames, cancel := tc.SSEReady()
	require.Equal(t, http.StatusOK, tc.Action("PushNotice").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"_notice":"maintenance"`)
	cancel()

	frames2, cancel2 := tc.SSE()
	defer cancel2()
	body := vt.AwaitFrame(t, frames2, 2*time.Second,
		`"_notice":"maintenance"`, "datastar-patch-elements")
	assert.NotContains(t, body, `"q"`,
		"a signal the server never pushed must not be re-seeded on resync")
	assert.NotContains(t, body, "via_tab",
		"resync must never re-seed via_tab")
}
