package via_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The SSE stream is bootstrapped with @post (not @get) so the connect can carry
// the page's signals as a body — the channel multiplexing needs.
func TestLive_pageBootstrapsTheStreamViaPost(t *testing.T) {
	t.Parallel()
	_, body := do(t, newPulse(t), http.MethodGet, "/", "")
	assert.Contains(t, body, `@post('/_via/sse')`, "streaming page must bootstrap the stream via @post")
	assert.NotContains(t, body, `@get('/_via/sse')`, "the old @get bootstrap must be gone")
}

// The SSE endpoint is POST-only now; a GET must be rejected (405) rather than
// silently opening a stream the bootstrap no longer uses.
func TestLive_sseEndpointRejectsGet(t *testing.T) {
	t.Parallel()
	srv := newPulse(t)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "GET /_via/sse must be 405; the stream is POST")
}

// A plain app (no live root, no live children) has no stream: a POST to the
// SSE endpoint must 404 rather than open an empty stream.
func TestSSE_plainAppHasNoStream(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_via/sse", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a plain app must not serve the SSE stream")
}

// A stream must emit a periodic keepalive even on a child with no ticks: a
// failed write is the only in-band way to notice a half-open peer. It must be
// an SSE comment frame, not a signal/element patch, so it never mutates
// client state. Runs at the real 25s cadence — synctest makes the wait free.
func TestLive_keepaliveFiresAtDefaultCadence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(quietChild{}))
		conn := app.Connect()

		time.Sleep(24 * time.Second)
		synctest.Wait()
		// Connect() only reads up to the tab-id line of the connect-time
		// signals frame; its trailing blank line is already buffered. Drain
		// whatever the connect handshake left behind before asserting nothing
		// NEW (a keepalive) has arrived.
		for {
			line, ok := conn.Peek()
			if !ok {
				break
			}
			require.NotContains(t, line, "keepalive", "keepalive must not fire before the 25s default cadence")
		}

		time.Sleep(time.Second) // cross the 25s mark
		line := conn.Await(": keepalive")
		assert.True(t, strings.HasPrefix(strings.TrimSpace(line), ":"),
			"keepalive must be an SSE comment frame (starts with ':'), not a data/event line")

		// A single beat proves the keepalive fires at all, but not that it
		// RECURS — a time.Timer (fires once) would pass the assertion above just
		// as well as a time.Ticker. Drive another full cadence and require a
		// second beat to catch that regression.
		time.Sleep(25 * time.Second)
		synctest.Wait()
		line = conn.Await(": keepalive")
		assert.True(t, strings.HasPrefix(strings.TrimSpace(line), ":"),
			"a second keepalive must fire a full cadence after the first — the beat must recur, not fire once")
	})
}

// stalledPeer models a peer whose receive side stalled: the connect frame
// goes through, but the next write only succeeds once the write deadline set
// via http.ResponseController expires. httptest's in-memory transport buffer
// is unbounded (internal/nettest.Conn bufMax defaults to math.MaxInt), so
// driving frames until the pipe fills cannot reproduce a blocked write there.
type stalledPeer struct {
	hdr      http.Header
	deadline time.Time
}

func (s *stalledPeer) Header() http.Header {
	if s.hdr == nil {
		s.hdr = http.Header{}
	}
	return s.hdr
}

func (s *stalledPeer) WriteHeader(int) {}

func (s *stalledPeer) Flush() {}

func (s *stalledPeer) SetWriteDeadline(t time.Time) error { s.deadline = t; return nil }

func (s *stalledPeer) Write(p []byte) (int, error) {
	if !bytes.Contains(p, []byte(": keepalive")) {
		return len(p), nil
	}
	<-time.After(time.Until(s.deadline))
	return 0, os.ErrDeadlineExceeded
}

// A stalled (not vanished) peer must still be torn down once a write blocks
// past the write deadline — the deadline is what catches a peer whose write
// never fails outright, only never completes. This runs at the real
// production write timeout (10s), not a shortened override.
func TestLive_halfOpenPeerTearsDownAfterWriteDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		handler := via.Handler(disposeProbe{disposed: done})
		req := httptest.NewRequest(http.MethodPost, "/_via/sse", nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")

		go handler.ServeHTTP(&stalledPeer{}, req)

		// disposeProbe registers no ticks; only the default 25s keepalive
		// drives a write, so nothing tears down before then.
		time.Sleep(25*time.Second + time.Second)
		synctest.Wait()
		select {
		case <-done:
			require.Fail(t, "the child was disposed before the write deadline elapsed")
		default:
		}

		// The stalled keepalive write can only fail once the 10s write
		// deadline (set at the moment of that write) expires.
		time.Sleep(10*time.Second + time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			require.Fail(t, "a stalled peer must be torn down once its write deadline elapses")
		}
	})
}

// stalledAfterConnect lets the connect handshake's frame through — recording
// the tab id off it — then stalls every later write. Models an SSE reader
// that stopped draining mid-stream: connected and dispatchable, but nothing
// streamed afterward ever lands.
type stalledAfterConnect struct {
	hdr          http.Header
	deadline     time.Time
	tab          string
	handshook    bool
	blockedWrite []byte
}

func (s *stalledAfterConnect) Header() http.Header {
	if s.hdr == nil {
		s.hdr = http.Header{}
	}
	return s.hdr
}

func (s *stalledAfterConnect) WriteHeader(int) {}

func (s *stalledAfterConnect) Flush() {}

func (s *stalledAfterConnect) SetWriteDeadline(t time.Time) error { s.deadline = t; return nil }

func (s *stalledAfterConnect) Write(p []byte) (int, error) {
	if !s.handshook {
		if m := tabRe.FindSubmatch(p); m != nil {
			s.tab = string(m[1])
		}
		if bytes.HasSuffix(p, []byte("\n\n")) {
			s.handshook = true
		}
		return len(p), nil
	}
	s.blockedWrite = append(s.blockedWrite[:0:0], p...) // capture what a later frame attempted to send
	<-time.After(time.Until(s.deadline))
	return 0, os.ErrDeadlineExceeded
}

// A stalled reader must not delay an action POST behind the deferred push it
// triggers. dispatchOverStream used to run the re-render/push inline, so the
// POST's goroutine sat parked in tabStream.run for as long as the write took,
// even though the action had already succeeded.
func TestLive_stalledWriteDoesNotBlockActionPOST(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		handler := via.Handler(liveClicker{})
		peer := &stalledAfterConnect{}
		connReq := httptest.NewRequest(http.MethodPost, "/_via/sse", nil)
		connReq.Header.Set("Sec-Fetch-Site", "same-origin")
		go handler.ServeHTTP(peer, connReq)
		synctest.Wait()
		require.NotEmpty(t, peer.tab, "the connect handshake must have handed out a tab id")

		getRec := httptest.NewRecorder()
		getReq := httptest.NewRequest(http.MethodGet, "/", nil)
		handler.ServeHTTP(getRec, getReq)
		url := actionURL(t, getRec.Body.String(), "r", 0)

		actionRec := httptest.NewRecorder()
		actionReq := httptest.NewRequest(http.MethodPost, url, strings.NewReader(withTab(peer.tab, "{}")))
		actionReq.Header.Set("Sec-Fetch-Site", "same-origin")
		actionReq.Header.Set("Datastar-Request", "true")
		actionDone := make(chan struct{})
		go func() {
			handler.ServeHTTP(actionRec, actionReq)
			close(actionDone)
		}()

		synctest.Wait()
		select {
		case <-actionDone:
		default:
			require.Fail(t, "the action POST must not still be parked behind the stalled peer's write")
		}
		assert.Equal(t, http.StatusNoContent, actionRec.Code,
			"the action ran — the response must not report failure just because its push hasn't landed")

		// Let the deferred push's write time out and tear the stream down, so
		// every bubble goroutine is gone (not merely idle) before this returns.
		time.Sleep(11 * time.Second)
		synctest.Wait()
		assert.Contains(t, string(peer.blockedWrite), "datastar-patch-elements",
			"the action's push must still have been attempted (deferred, not dropped) even though its write stalls")
	})
}

// halfOpenFlusher lets the connect handshake succeed but fails the keepalive
// write, standing in for a peer that vanished mid-session without a FIN.
// Failing the keepalive (not the first frame) exercises the realistic
// half-open moment: healthy, then a later write is the first to notice.
type halfOpenFlusher struct{ hdr http.Header }

func (f *halfOpenFlusher) Header() http.Header {
	if f.hdr == nil {
		f.hdr = http.Header{}
	}
	return f.hdr
}

func (f *halfOpenFlusher) WriteHeader(int) {}

func (f *halfOpenFlusher) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(": keepalive")) {
		return 0, errors.New("broken pipe")
	}
	return len(p), nil
}

func (f *halfOpenFlusher) Flush() {}

// A half-open peer (gone without a FIN) never cancels the request context, so a
// failed frame write is the only in-band signal that it's gone. The stream must
// react to it by tearing the child down — running disposers, stopping ticks —
// not by looping its single goroutine against a dead socket forever.
func TestLive_failedStreamWriteTearsDownTheChildSoItDoesNotLeak(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		handler := via.Handler(disposeProbe{disposed: done})
		// httptest.NewRequest's context is never cancelled, so the ONLY thing that
		// can end the stream here is the failed keepalive write — isolating that path.
		req := httptest.NewRequest(http.MethodPost, "/_via/sse", nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin") // past the origin floor, as a real browser would

		go handler.ServeHTTP(&halfOpenFlusher{}, req)

		// disposeProbe registers no ticks; only the default 25s keepalive
		// drives the write that fails here.
		time.Sleep(25*time.Second + time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			require.Fail(t, "a failed keepalive write must tear the child down (run disposers); it leaked instead")
		}
	})
}

// pulse is a live child: implementing OnInit opts it into a server-push SSE
// stream. A server-side ticker increments a beat count; via re-renders and
// pushes the fragment, so the browser updates with no client code.
type pulse struct{ beats via.State[int] }

func (p *pulse) OnInit(ctx *via.Ctx) error {
	ctx.Tick(20*time.Millisecond, p.beat)
	return nil
}

func (p *pulse) beat(ctx *via.Ctx) { p.beats.Set(p.beats.Get() + 1) }

func (p *pulse) View() h.H {
	return h.Div(h.H1(h.Str("pulse")), h.P(h.Str("beats: "), p.beats.Display()))
}

// liveServer starts handler on httptest's in-memory network, required for the
// server's goroutines to run inside a synctest bubble, and forces srv.URL to
// populate (it's set lazily, on first Client/Start/StartTLS call).
func liveServer(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewTestServer(t, handler)
	srv.Client()
	return srv
}

func newPulse(t *testing.T) *httptest.Server {
	t.Helper()
	return liveServer(t, via.Handler(pulse{}))
}

// multiline is a live child whose rendered content contains a newline. The SSE
// framing must survive it.
type multiline struct{ s string }

func (m *multiline) OnInit(ctx *via.Ctx) error {
	ctx.Tick(15*time.Millisecond, m.set)
	return nil
}

func (m *multiline) set(*via.Ctx) { m.s = "top\nbottom" }

func (m *multiline) View() h.H { return h.Div(h.P(h.Str(m.s))) }

// quietChild is a live composition that registers no ticks — the stream must
// still open and hold cleanly, not panic or wedge.
type quietChild struct{ n via.State[int] }

func (q *quietChild) View() h.H { return h.Div(h.Str("quiet"), q.n.Display()) }

// readFirstFrame returns the lines of the first SSE event from the stream
// (everything up to the first blank-line terminator), cancelling the request.
func readFirstFrame(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin") // mimic a same-origin browser SSE fetch past the origin floor
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	lines := make(chan string, 128)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			assert.NoError(t, err, "sse scan failed")
		}
		close(lines)
	}()

	// Return the first datastar-patch-ELEMENTS frame, skipping the connect-time
	// viatab patch-signals frame that now precedes every stream.
	var frame []string
	inElements := false
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			require.Fail(t, "no datastar-patch-elements frame arrived")
		case line, ok := <-lines:
			require.True(t, ok, "stream closed before a patch-elements frame")
			switch {
			case strings.HasPrefix(line, "event:"):
				inElements = strings.Contains(line, "datastar-patch-elements")
				frame = nil
				if inElements {
					frame = append(frame, line)
				}
			case line == "": // blank line terminates the event
				if inElements && len(frame) > 0 {
					cancel()
					return frame
				}
				inElements = false
			default:
				if inElements {
					frame = append(frame, line)
				}
			}
		}
	}
}

// openStream opens the SSE stream and returns its lines plus a cancel. The Do()
// returns once headers are flushed — which happens after OnInit — so the
// subscription is registered by the time this returns.
func openStream(t *testing.T, srv *httptest.Server) (<-chan string, context.CancelFunc) {
	t.Helper()
	return openStreamAt(t, srv, "/_via/sse")
}

// openStreamAt is openStream against an arbitrary mount's SSE path — a
// parametrised mount's stream lives at its concrete base, not "/_via/sse".
func openStreamAt(t *testing.T, srv *httptest.Server, path string) (<-chan string, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin") // mimic a same-origin browser SSE fetch past the origin floor
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
		// A caller can end the stream from the server side (e.g. forcing a
		// disconnect) without cancelling ctx, so a scan error here isn't
		// necessarily a bug — just log it so a spurious "frame never
		// arrived" failure elsewhere in the test carries the real reason.
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			t.Logf("sse scan ended: %v", err)
		}
	}()
	return lines, cancel
}

func awaitLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			require.Fail(t, fmt.Sprintf("timed out waiting for a line containing %q", want))
		case line, ok := <-lines:
			require.True(t, ok, "stream closed before %q arrived", want)
			if strings.Contains(line, want) {
				return
			}
		}
	}
}

var tabRe = regexp.MustCompile(`"viatab":"([^"]+)"`)

// awaitTabID reads the SSE stream until the connect-time signals frame that
// carries the per-connection tab id, and returns it.
func awaitTabID(t *testing.T, lines <-chan string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			require.Fail(t, "no viatab signals frame arrived")
		case line, ok := <-lines:
			require.True(t, ok, "stream closed before the tab-id frame")
			if m := tabRe.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
}

// A rendered fragment with an embedded newline must remain ONE SSE event:
// every content line after the event line must be a `data:` field. A bare,
// unprefixed line is read by the client as a junk field, silently truncating
// the patch, then applying broken HTML.
func TestLive_multilineFragmentStaysOneSSEEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(multiline{}))

		frame := readFirstFrame(t, srv)
		require.NotEmpty(t, frame)
		assert.Equal(t, "event: datastar-patch-elements", frame[0])
		for _, line := range frame[1:] {
			assert.Truef(t, strings.HasPrefix(line, "data:"),
				"every content line must be a data: field, got a bare line that would truncate the patch: %q", line)
		}
		whole := strings.Join(frame, "\n")
		assert.Contains(t, whole, "top")
		assert.Contains(t, whole, "bottom")
	})
}

// A live child that registers no ticks must still open the stream cleanly.
func TestLive_streamOpensWithNoTicks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(quietChild{}))

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin") // mimic a same-origin browser SSE fetch past the origin floor
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
		cancel() // disconnect; the no-ticks branch must return without panic/leak
		synctest.Wait()
	})
}

// A streaming page must server-render its initial View (no empty flash) and carry a
// single bootstrap that opens the per-tab SSE stream, or the child never goes
// live in the browser.
func TestLivePage_serverRendersAndBootstrapsTheStream(t *testing.T) {
	t.Parallel()
	_, body := do(t, newPulse(t), http.MethodGet, "/", "")
	assert.Contains(t, body, `<div id="root"`)
	assert.Contains(t, body, "beats: 0",
		"State must render its zero value at first paint (before OnInit) without panicking")
	assert.Contains(t, body, `data-init="@post('/_via/sse')"`, "page must bootstrap the SSE stream")
}

// The SSE endpoint must stream Datastar element-patch frames: text/event-stream,
// an `event: datastar-patch-elements` line, and a `data: elements <#root …>`
// line carrying the re-rendered fragment with an advanced beat — the live push.
func TestLive_streamsElementPatchFramesThatMorphRoot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := newPulse(t)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin") // mimic a same-origin browser SSE fetch past the origin floor
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

		lines := make(chan string, 64)
		go func() {
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				lines <- sc.Text()
			}
			if err := sc.Err(); err != nil && ctx.Err() == nil {
				assert.NoError(t, err, "sse scan failed")
			}
			close(lines)
		}()

		var sawEvent bool
		deadline := time.After(1500 * time.Millisecond)
		for {
			select {
			case <-deadline:
				require.Fail(t, "no datastar-patch-elements frame arrived")
			case line, ok := <-lines:
				require.True(t, ok, "stream closed before a frame arrived")
				if line == "event: datastar-patch-elements" {
					sawEvent = true
					continue
				}
				if sawEvent && strings.HasPrefix(line, "data: elements ") {
					assert.Contains(t, line, `<div id="root"`, "frame must carry the #root morph target")
					assert.Contains(t, line, "beats: ", "frame must re-render the child")
					cancel()
					return
				}
			}
		}
	})
}

// boomOnDiscovery panics in View on every call, including the connect
// handshake's own discovery render — before OnInit ever runs and before any
// header has gone out on the response.
type boomOnDiscovery struct{}

func (boomOnDiscovery) View() h.H { panic("via_test: discovery render exploded") }

// A panic before the stream's headers are sent must answer 500, not fall
// through to Go's default of 200 with an empty body — the client would read
// that as a successful (if empty) connect.
func TestLive_connectPanicBeforeHeadersAnswers500(t *testing.T) {
	t.Parallel()
	resp, body := do(t, serve(t, via.Handler(boomOnDiscovery{})), http.MethodPost, "/_via/sse", "")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.NotEmpty(t, body)
}

// notFoundConnect's OnInit returns via.ErrNotFound — the live analogue of a
// page whose data vanished. The connect must answer 404, not 500: the world
// changed, the request is honest. Fails if the sentinel stops mapping to 404.
type notFoundConnect struct{}

func (f *notFoundConnect) OnInit(ctx *via.Ctx) error { return via.ErrNotFound }

func (f *notFoundConnect) View() h.H { return h.Div(h.Str("x")) }

func TestLive_onConnectErrNotFoundIs404(t *testing.T) {
	t.Parallel()
	resp, _ := do(t, serve(t, via.Handler(notFoundConnect{})), http.MethodPost, "/_via/sse", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// openStreamAt's reader goroutine has two return paths — ctx.Done (client
// cancels) and the scanner running dry (server closes the body). Both must
// close lines, or a caller that ranges over it (or does `cancel(); <-done`
// on a goroutine that ranges over it) hangs forever.
func TestOpenStreamAt_closesLinesOnClientCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		srv := liveServer(t, via.Handler(disposeProbe{disposed: done}))

		lines, cancel := openStream(t, srv)
		cancel()

		drained := make(chan struct{})
		go func() {
			for range lines {
			}
			close(drained)
		}()
		synctest.Wait()

		select {
		case <-drained:
		default:
			require.Fail(t, "lines was not closed after the client canceled")
		}
	})
}

// Neighbour path: the scanner running dry (the server ends the stream, not
// the caller canceling ctx) must also close lines — the reader goroutine
// falls out of `for sc.Scan()` without ever taking the ctx.Done arm.
func TestOpenStreamAt_closesLinesOnServerClose(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Handler(disposeProbe{disposed: make(chan struct{})}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	srv.CloseClientConnections() // ends the connection from the server side, without canceling ctx

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return // closed, as required
			}
		case <-deadline:
			require.Fail(t, "lines was not closed after the server closed the stream")
		}
	}
}

// clicker is a live child whose action mutates its OWN server State. The proof
// of correct routing: after the POST, the patch must arrive over THIS
// connection's SSE (not as the POST body), which only happens if the action ran
// against this connection's child instance — not a throwaway per-request copy.
type clicker struct{ count via.State[int] }

func (c *clicker) Bump(ctx *via.Ctx) { c.count.Set(c.count.Get() + 1) }

func (c *clicker) View() h.H {
	return h.Div(h.P(h.Str("count: "), c.count.Display()), h.Button(via.On("click", c.Bump), h.Str("+")))
}

func TestLiveAction_mutatesThisConnectionsStateAndPushesOverItsSSE(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(clicker{}))
		conn := app.Connect()
		require.NotEmpty(t, conn.TabID(), "the SSE must hand the client its tab id")

		status, _ := app.Action(0).Over(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status, "a live action acks 204; the patch ships over the SSE")

		conn.Await("count: 1") // the mutation reaches THIS connection
	})
}

// flakyRender panics inside View once boom is set — modelling a push (a
// pulse item) whose render fails, as opposed to liveRunAction's own mutation,
// which already recovers separately.
type flakyRender struct {
	boom via.State[bool]
	n    via.State[int]
}

func (f *flakyRender) Trigger(ctx *via.Ctx) { f.boom.Set(true); f.n.Set(f.n.Get() + 1) }

func (f *flakyRender) Fix(ctx *via.Ctx) { f.boom.Set(false) }

func (f *flakyRender) View() h.H {
	if f.boom.Get() {
		panic("via_test: render exploded")
	}
	return h.Div(
		h.P(h.Str("n: "), f.n.Display()),
		h.Button(via.On("click", f.Trigger)),
		h.Button(via.On("click", f.Fix)),
	)
}

// A panic in the re-render a dispatched action's pushWork triggers must not
// take the whole stream goroutine down — a dead goroutine here would strand
// every later action behind a stream that looks alive but never dispatches
// again. Trigger's mutation succeeds and its render then panics; a second
// action (Fix) must still dispatch and push normally.
func TestLive_pushPanicDoesNotKillTheStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(flakyRender{}))
		conn := app.Connect()
		require.NotEmpty(t, conn.TabID())

		status, _ := app.Action(0).Over(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status, "the mutation applies and answers before its render panics")

		status, _ = app.Action(1).Over(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status, "the stream goroutine must still be alive to dispatch a second action")
		conn.Await("n: 1")
	})
}

// racyDirtySignal is a live counter: every Inc dispatch's own push
// (dispatchOverStream's pushWork replaces lc.units[0] after every action) is
// the race — one dispatch's read can land on a unit a concurrent dispatch's
// push is about to make stale.
type racyDirtySignal struct {
	n    via.Signal[int]
	beat via.State[int]
}

func (r *racyDirtySignal) Inc(ctx *via.Ctx) { r.n.Set(r.n.Get() + 1) }

func (r *racyDirtySignal) View() h.H {
	return h.Div(r.n.Display(), r.beat.Display(), h.Button(via.On("click", r.Inc), h.Str("inc")))
}

// Every Inc dispatch that acks must also ship its value over the SSE
// signals-patch, even under concurrent Incs. Before the fix (dispatchOverStream
// looked up its unit before handing off to the child goroutine, not inside
// it), a concurrent push could replace the unit in between, dropping values.
func TestLiveAction_signalPatchSurvivesARacingPush(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Handler(racyDirtySignal{}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, "r", 0)

	var seen sync.Map // values (int) observed in the counter signal's signals-patch frame
	re := regexp.MustCompile(`"n":(\d+)`)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for line := range lines {
			if m := re.FindStringSubmatch(line); m != nil {
				if v, err := strconv.Atoi(m[1]); err == nil {
					seen.Store(v, struct{}{})
				}
			}
		}
	}()

	const goroutines, perGoroutine = 8, 100
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				req, err := http.NewRequest(http.MethodPost, srv.URL+url, strings.NewReader(withTab(tab, "{}")))
				if err != nil {
					continue
				}
				req.Header.Set("Datastar-Request", "true")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				resp, err := srv.Client().Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		})
	}
	wg.Wait()

	total := goroutines * perGoroutine
	missing := func() []int {
		var m []int
		for i := 1; i <= total; i++ {
			if _, ok := seen.Load(i); !ok {
				m = append(m, i)
			}
		}
		return m
	}
	deadline := time.After(3 * time.Second)
	for len(missing()) > 0 {
		select {
		case <-deadline:
			require.Empty(t, missing(), "signal patches for some Inc dispatches never reached the client")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	<-done
}

// C9 regression: liveRunAction's push rides back as actionResult.pushWork
// and runs on the connection's serialized goroutine right after acking, in
// commit order — not on a detached goroutine racing other actions. The old
// fire-and-forget enqueue could reorder pushes under concurrent dispatch.
func TestLiveAction_pushesStayInCommitOrderUnderConcurrentDispatch(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Handler(racyDirtySignal{}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, "r", 0)

	re := regexp.MustCompile(`"n":(\d+)`)
	var mu sync.Mutex
	var seq []int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for line := range lines {
			if m := re.FindStringSubmatch(line); m != nil {
				if v, err := strconv.Atoi(m[1]); err == nil {
					mu.Lock()
					seq = append(seq, v)
					mu.Unlock()
				}
			}
		}
	}()

	const goroutines, perGoroutine = 8, 100
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				req, err := http.NewRequest(http.MethodPost, srv.URL+url, strings.NewReader(withTab(tab, "{}")))
				if err != nil {
					continue
				}
				req.Header.Set("Datastar-Request", "true")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				resp, err := srv.Client().Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		})
	}
	wg.Wait()

	total := goroutines * perGoroutine
	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		n := len(seq)
		mu.Unlock()
		if n >= total {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d/%d signal patches arrived before the deadline", n, total)
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	<-done

	mu.Lock()
	got := append([]int(nil), seq...)
	mu.Unlock()
	want := make([]int, total)
	for i := range want {
		want[i] = i + 1
	}
	require.Equal(t, want, got,
		"a stream's pushes must ship in the order their mutations committed")
}

// firstElementsFrame drains lines until the first datastar-patch-elements
// frame and returns its joined element lines.
func firstElementsFrame(t *testing.T, lines <-chan string) string {
	t.Helper()
	var frame []string
	in := false
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for an element patch")
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed before an element patch")
			}
			switch {
			case strings.HasPrefix(line, "event:"):
				in = strings.Contains(line, "datastar-patch-elements")
			case line == "":
				if in && len(frame) > 0 {
					return strings.Join(frame, "\n")
				}
				in = false
			case in:
				frame = append(frame, line)
			}
		}
	}
}

// plainWriter is the ResponseWriter an ordinary middleware hands down: the
// three methods of the interface plus Unwrap, and no Flusher. The stock
// net/http writer is a Flusher, so nothing but a wrapper produces this shape.
type plainWriter struct{ http.ResponseWriter }

func (p plainWriter) Unwrap() http.ResponseWriter { return p.ResponseWriter }

func TestLive_streamOpensThroughAResponseWriterWrapper(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/", pulse{})
	t.Cleanup(r.Close)
	mw := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.ServeHTTP(plainWriter{w}, req)
	})
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_via/sse", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"a middleware that wraps the writer must not cost the app its stream")
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
}

// wrapRW is the ResponseWriter a user's own middleware installs — a logger, a
// gzip layer, anything that needs to see the bytes. It answers Unwrap (as
// net/http asks middleware to) but not Flush, which is the shape a raw
// w.(http.Flusher) assertion gets wrong.
type wrapRW struct{ http.ResponseWriter }

func (w wrapRW) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// via must find the flusher THROUGH a user's middleware. A raw http.Flusher
// assertion answers no for the wrapper above, and the connect would 500
// "streaming unsupported" — so every live page behind an ordinary logging or
// compression middleware would simply never go live.
func TestLive_connectsThroughAMiddlewareThatWrapsTheWriter(t *testing.T) {
	t.Parallel()
	app := via.Handler(disposeProbe{disposed: make(chan struct{})})
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		app.ServeHTTP(wrapRW{ResponseWriter: w}, req)
	}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	require.NotNil(t, lines)
}

// deadRW is a ResponseWriter and nothing more — no Flush, no Unwrap, the shape
// a middleware that buffers the whole response entirely leaves behind. It
// records what via answered so the outer handler can mirror it back.
type deadRW struct {
	hdr  http.Header
	code int
	body strings.Builder
}

func (d *deadRW) Header() http.Header         { return d.hdr }
func (d *deadRW) Write(p []byte) (int, error) { return d.body.Write(p) }
func (d *deadRW) WriteHeader(code int)        { d.code = code }

// The other end of the probe: a writer that genuinely cannot stream must be
// refused BEFORE the stream goroutine and its timers are allocated, and with a
// status that says why. Flushing to find out would commit a 200 first.
func TestLive_connectIsRefusedWhenTheWriterCannotStream(t *testing.T) {
	t.Parallel()
	app := via.Handler(disposeProbe{disposed: make(chan struct{})})
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		d := &deadRW{hdr: w.Header(), code: http.StatusOK}
		app.ServeHTTP(d, req)
		w.WriteHeader(d.code)
		io.WriteString(w, d.body.String())
	}))

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"a connect on a writer that cannot stream must be refused, not half-opened")
	assert.Contains(t, string(b), "streaming unsupported")
}
