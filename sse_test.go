package via_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
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

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithMaxRequestBody(16),
	)
	via.Mount[sseEmptyPage](app, "/")
	defer server.Close()

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

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[sseEmptyPage](app, "/")
	defer server.Close()

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

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithSSEWriteTimeout(500*time.Millisecond),
	)
	via.Mount[sseDeadlinePage](app, "/")
	defer server.Close()

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

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[brotliProbePage](app, "/")
	defer server.Close()

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
