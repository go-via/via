package via_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The SSE stream is bootstrapped with @post (not @get) so the connect can carry
// the page's signals as a body — the channel multiplexing needs.
func TestLive_pageBootstrapsTheStreamViaPost(t *testing.T) {
	t.Parallel()
	_, body := do(t, newPulse(t), http.MethodGet, "/", "")
	assert.Contains(t, body, `@post('/_via/sse')`, "live page must bootstrap the stream via @post")
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

// A stateless app (no live root, no live islands) has no stream: a POST to the
// SSE endpoint must 404 rather than open an empty stream.
func TestSSE_statelessAppHasNoLiveStream(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_via/sse", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a stateless app must not serve the SSE stream")
}

// A live stream must emit a periodic keepalive even on an island with no
// ticks: a successful write proves the peer is still there, and a FAILED
// write is the only in-band way to notice a silently-dropped (half-open) peer
// so the island goroutine and its timers don't leak. It must be an SSE
// comment frame, not a signal/element patch, so it never mutates client
// state. This runs at the fixed 25s cadence — synctest makes the wait free in
// wall time.
func TestLive_keepaliveFiresAtDefaultCadence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(quietIsland{}))
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

// stalledPeer models a peer whose receive side has stalled: the connect-time
// frame goes through (a real handshake reaches this far), but the next write
// — the keepalive — can only succeed once the peer reads again (never, here)
// or the per-frame write deadline set via http.ResponseController expires.
// The in-memory httptest network's transport buffer turned out to be
// unbounded (internal/nettest.Conn defaults bufMax to math.MaxInt), so
// "drive frames until the pipe is full" cannot reproduce a blocked write over
// it; a ResponseWriter that itself honors SetWriteDeadline is the direct way
// to exercise the deadline path, in the same style as halfOpenFlusher above.
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
func (s *stalledPeer) WriteHeader(int)                    {}
func (s *stalledPeer) Flush()                             {}
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
		handler := via.Register(disposeProbe{disposed: done})
		req := httptest.NewRequest(http.MethodPost, "/_via/sse", nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")

		go handler.ServeHTTP(&stalledPeer{}, req)

		// disposeProbe registers no ticks; only the default 25s keepalive
		// drives a write, so nothing tears down before then.
		time.Sleep(25*time.Second + time.Second)
		synctest.Wait()
		select {
		case <-done:
			require.Fail(t, "the island was disposed before the write deadline elapsed")
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

// stalledAfterConnect lets the connect handshake's own frame (the tab-id
// signals patch) through — recording the tab id off it — then stalls every
// later write until its deadline elapses. That models an SSE reader that
// stopped draining while the stream keeps producing frames (a tick, or an
// action's own deferred push): the handshake completed, so the connection is
// registered and dispatchable, but nothing it streams afterward ever lands.
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
func (s *stalledAfterConnect) WriteHeader(int)                    {}
func (s *stalledAfterConnect) Flush()                             {}
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
// triggers: the mutation is already done, and the response must say so,
// before the write ever gets a chance to block. dispatchLive used to run the
// re-render/push inline, so the POST's own goroutine sat parked in
// liveConn.run for as long as the write took (up to the write timeout, or
// forever if disabled) even though the action had already succeeded.
func TestLive_stalledWriteDoesNotBlockActionPOST(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		handler := via.Register(liveClicker{})
		peer := &stalledAfterConnect{}
		connReq := httptest.NewRequest(http.MethodPost, "/_via/sse", nil)
		connReq.Header.Set("Sec-Fetch-Site", "same-origin")
		go handler.ServeHTTP(peer, connReq)
		synctest.Wait()
		require.NotEmpty(t, peer.tab, "the connect handshake must have handed out a tab id")

		getRec := httptest.NewRecorder()
		getReq := httptest.NewRequest(http.MethodGet, "/", nil)
		handler.ServeHTTP(getRec, getReq)
		url := actionURL(t, getRec.Body.String(), 0, 0)

		actionRec := httptest.NewRecorder()
		actionReq := httptest.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
		actionReq.Header.Set("Sec-Fetch-Site", "same-origin")
		actionReq.Header.Set("Datastar-Request", "true")
		actionReq.Header.Set("X-Via-Tab", peer.tab)
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
// write, standing in for a peer that vanished mid-session without a FIN (a
// broken pipe surfacing on the next write). Failing the keepalive specifically —
// not the first frame — exercises the realistic half-open moment: the stream was
// healthy, then a later write is the first to notice the peer is gone.
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
// react to it by tearing the island down — running disposers, stopping ticks —
// not by looping its single goroutine against a dead socket forever.
func TestLive_failedStreamWriteTearsDownTheIslandSoItDoesNotLeak(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		handler := via.Register(disposeProbe{disposed: done})
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
			require.Fail(t, "a failed keepalive write must tear the island down (run disposers); it leaked instead")
		}
	})
}

// pulse is a live island: implementing OnInit opts it into a server-push SSE
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

// liveServer starts handler on httptest's in-memory network — required for
// the server's own goroutines (ticks, subscriptions) to run inside a
// synctest bubble at all — and forces srv.URL to populate (it is set lazily,
// on the first call to Client/Start/StartTLS) before any caller builds a
// request string from it.
func liveServer(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewTestServer(t, handler)
	srv.Client()
	return srv
}

func newPulse(t *testing.T) *httptest.Server {
	t.Helper()
	return liveServer(t, via.Register(pulse{}))
}

// multiline is a live island whose rendered content contains a newline. The SSE
// framing must survive it.
type multiline struct{ s string }

func (m *multiline) OnInit(ctx *via.Ctx) error {
	ctx.Tick(15*time.Millisecond, m.set)
	return nil
}
func (m *multiline) set(*via.Ctx) { m.s = "top\nbottom" }
func (m *multiline) View() h.H    { return h.Div(h.P(h.Str(m.s))) }

// quietIsland is a live composition that registers no ticks — the stream must
// still open and hold cleanly, not panic or wedge.
type quietIsland struct{ n via.State[int] }

func (q *quietIsland) View() h.H { return h.Div(h.Str("quiet"), q.n.Display()) }

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
		close(lines)
	}()

	// Return the first datastar-patch-ELEMENTS frame, skipping the connect-time
	// _viatab patch-signals frame that now precedes every live stream.
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

var tabRe = regexp.MustCompile(`"_viatab":"([^"]+)"`)

// awaitTabID reads the SSE stream until the connect-time signals frame that
// carries the per-connection tab id, and returns it.
func awaitTabID(t *testing.T, lines <-chan string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			require.Fail(t, "no _viatab signals frame arrived")
		case line, ok := <-lines:
			require.True(t, ok, "stream closed before the tab-id frame")
			if m := tabRe.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
}

// A rendered fragment with an embedded newline must remain ONE SSE event: every
// content line after the event line must be a `data:` field. A bare,
// unprefixed line (the naive single-data-line framing) is read by the client as
// a junk field, silently truncating the patch — the morph then applies broken
// HTML. This guards that the framing splits multi-line payloads correctly.
func TestLive_multilineFragmentStaysOneSSEEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Register(multiline{}))

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

// A live island that registers no ticks must still open the stream cleanly.
func TestLive_streamOpensWithNoTicks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Register(quietIsland{}))

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

// A live page must server-render its initial View (no empty flash) and carry a
// single bootstrap that opens the per-tab SSE stream, or the island never goes
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
					assert.Contains(t, line, "beats: ", "frame must re-render the island")
					cancel()
					return
				}
			}
		}
	})
}

// feed is a live island driven by a shared Topic: every connection subscribes,
// and a published message fans out to all of them and is shown live.
type feed struct {
	room *topic.Topic[string]
	last via.State[string]
}

func (f *feed) OnInit(ctx *via.Ctx) error {
	ctx.Listen(f.room, f.recv)
	return nil
}
func (f *feed) recv(ctx *via.Ctx, msg string) { f.last.Set(msg) }
func (f *feed) View() h.H {
	return h.Div(h.H1(h.Str("feed")), h.P(h.Str("latest: "), f.last.Display()))
}

// disposeProbe signals a channel from its OnDispose so a test can observe that
// teardown ran on disconnect.
type disposeProbe struct {
	disposed chan struct{}
	n        via.State[int]
}

func (d *disposeProbe) OnInit(ctx *via.Ctx) error {
	ctx.OnDispose(d.markDisposed)
	return nil
}
func (d *disposeProbe) markDisposed() { close(d.disposed) }
func (d *disposeProbe) View() h.H     { return h.Div(h.Str("probe"), d.n.Display()) }

// One publish must reach EVERY connected island — that's the multi-user
// headline. Two streams subscribe; a single Publish to the shared Topic shows up
// on both.
func TestFeed_publishFansOutToEveryConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		room := topic.New[string]()
		srv := liveServer(t, via.Register(feed{room: room}))

		l1, c1 := openStream(t, srv)
		defer c1()
		l2, c2 := openStream(t, srv)
		defer c2()

		room.Publish("hello-everyone")

		awaitLine(t, l1, "latest: hello-everyone")
		awaitLine(t, l2, "latest: hello-everyone")
	})
}

// mixedIsland registers BOTH a tick and a subscription, plus a dispose probe.
type mixedIsland struct {
	room     *topic.Topic[string]
	beats    via.State[int]
	last     via.State[string]
	disposed chan struct{}
}

func (m *mixedIsland) OnInit(ctx *via.Ctx) error {
	ctx.Tick(15*time.Millisecond, m.beat)
	ctx.OnDispose(m.markDispose)
	ctx.Listen(m.room, m.recv)
	return nil
}
func (m *mixedIsland) beat(ctx *via.Ctx)             { m.beats.Set(m.beats.Get() + 1) }
func (m *mixedIsland) recv(ctx *via.Ctx, msg string) { m.last.Set(msg) }
func (m *mixedIsland) markDispose()                  { close(m.disposed) }
func (m *mixedIsland) View() h.H {
	return h.Div(
		h.P(h.Str("beats: "), m.beats.Display()),
		h.P(h.Str("last: "), m.last.Display()),
	)
}

// Ticks and subscriptions share one island loop: a ticking island must also
// deliver published messages, and disconnecting a ticking+subscribed island
// must still tear down cleanly (the ticker goroutine exits, disposers run).
func TestLive_tickAndSubscribeShareOneIslandLoopAndTearDownCleanly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		room := topic.New[string]()
		done := make(chan struct{})
		srv := liveServer(t, via.Register(mixedIsland{room: room, disposed: done}))

		lines, cancel := openStream(t, srv)
		awaitLine(t, lines, "beats: ") // a tick frame flows
		room.Publish("from-topic")
		awaitLine(t, lines, "last: from-topic") // a sub frame flows alongside ticks

		cancel() // disconnect while the ticker is running
		synctest.Wait()
		select {
		case <-done:
		default:
			require.Fail(t, "OnDispose did not run when a ticking, subscribed island disconnected")
		}
	})
}

// failInit acquires nothing and fails: OnInit runs on every request that
// renders the unit, so the pair it registers must stay unrun when the connect
// never opens.
type failInit struct {
	acquired chan struct{}
	disposed chan struct{}
	n        via.State[int]
}

func (f *failInit) OnInit(ctx *via.Ctx) error {
	ctx.OnLive(f.markAcquired)
	ctx.OnDispose(f.markDisposed)
	return errConnectBoom
}
func (f *failInit) markAcquired() { close(f.acquired) }
func (f *failInit) markDisposed() { close(f.disposed) }
func (f *failInit) View() h.H     { return h.Div(h.Str("x"), f.n.Display()) }

var errConnectBoom = errorString("init boom")

type errorString string

func (e errorString) Error() string { return string(e) }

// A failed OnInit opens no connection, so neither half of an OnLive/OnDispose
// pair may run: registering is not acquiring, and a Listen registered before
// the failure never subscribed either — nothing is left orphaned in a Topic.
func TestLive_failedInitRunsNeitherHalfOfThePair(t *testing.T) {
	t.Parallel()
	acquired, disposed := make(chan struct{}), make(chan struct{})
	resp, _ := do(t, serve(t, via.Register(failInit{acquired: acquired, disposed: disposed})),
		http.MethodPost, "/_via/sse", "")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	select {
	case <-acquired:
		require.Fail(t, "OnLive ran on a connect that never opened")
	default:
	}
	select {
	case <-disposed:
		require.Fail(t, "OnDispose ran on a connect that never opened")
	default:
	}
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
	resp, body := do(t, serve(t, via.Register(boomOnDiscovery{})), http.MethodPost, "/_via/sse", "")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.NotEmpty(t, body)
}

// notFoundConnect's OnInit returns via.ErrNotFound — the live analogue of a
// page whose data vanished. The connect must answer 404, not 500: the world
// changed, the request is honest. Fails if the sentinel stops mapping to 404.
type notFoundConnect struct{}

func (f *notFoundConnect) OnInit(ctx *via.Ctx) error { return via.ErrNotFound }
func (f *notFoundConnect) View() h.H                 { return h.Div(h.Str("x")) }

func TestLive_onConnectErrNotFoundIs404(t *testing.T) {
	t.Parallel()
	resp, _ := do(t, serve(t, via.Register(notFoundConnect{})), http.MethodPost, "/_via/sse", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// On disconnect the island's OnDispose must run, so subscriptions and producers
// are torn down rather than leaked for the life of the process.
func TestLive_onDisposeRunsWhenClientDisconnects(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Register(disposeProbe{disposed: done}))

		_, cancel := openStream(t, srv)
		cancel() // disconnect
		synctest.Wait()
	})

	// The server's stream goroutine runs on a real httptest listener, outside
	// the bubble (see I4): synctest.Wait() only settles bubble goroutines, so
	// the OS actually delivering the close can still be in flight once the
	// bubble above returns. This wait runs after the bubble, in real wall-clock
	// time, so it is a genuine (bounded) wait rather than a fake-clock no-op.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.Fail(t, "OnDispose did not run on disconnect")
	}
}

// panicThenDisposeProbe registers two disposers — the first always panics —
// so a test can prove the second still runs.
type panicThenDisposeProbe struct {
	disposed chan struct{}
	n        via.State[int]
}

func (p *panicThenDisposeProbe) OnInit(ctx *via.Ctx) error {
	ctx.OnDispose(func() { panic("disposer boom") })
	ctx.OnDispose(p.markDisposed)
	return nil
}
func (p *panicThenDisposeProbe) markDisposed() { close(p.disposed) }
func (p *panicThenDisposeProbe) View() h.H     { return h.Div(h.Str("probe"), p.n.Display()) }

// A panicking disposer must not skip every disposer registered after it — a
// skipped one (e.g. sub.Stop) would otherwise leak for the life of the
// process.
func TestLive_onDisposeContinuesAfterAPanickingDisposer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		srv := liveServer(t, via.Register(panicThenDisposeProbe{disposed: done}))

		_, cancel := openStream(t, srv)
		cancel() // disconnect
		synctest.Wait()

		select {
		case <-done:
		default:
			require.Fail(t, "the disposer after the panicking one did not run")
		}
	})
}

// openStreamAt's reader goroutine has two return paths — ctx.Done (client
// cancels) and the scanner running dry (server closes the body). Both must
// close lines, or a caller that ranges over it (or does `cancel(); <-done`
// on a goroutine that ranges over it) hangs forever.
func TestOpenStreamAt_closesLinesOnClientCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		srv := liveServer(t, via.Register(disposeProbe{disposed: done}))

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
	srv := liveServer(t, via.Register(disposeProbe{disposed: make(chan struct{})}))

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

// clicker is a live island whose action mutates its OWN server State. The proof
// of correct routing: after the POST, the patch must arrive over THIS
// connection's SSE (not as the POST body), which only happens if the action ran
// against this connection's island instance — not a throwaway per-request copy.
type clicker struct{ count via.State[int] }

func (c *clicker) Bump(ctx *via.Ctx) { c.count.Set(c.count.Get() + 1) }
func (c *clicker) View() h.H {
	return h.Div(h.P(h.Str("count: "), c.count.Display()), h.Button(via.On("click", c.Bump), h.Str("+")))
}

func TestLiveAction_mutatesThisConnectionsStateAndPushesOverItsSSE(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(clicker{}))
		conn := app.Connect()
		require.NotEmpty(t, conn.TabID(), "the SSE must hand the client its tab id")

		status, _ := app.Action(0).Live(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status, "a live action acks 204; the patch ships over the SSE")

		conn.Await("count: 1") // the mutation reaches THIS connection
	})
}

// A live action POST with no/unknown tab id (no live connection to route to)
// must 410 so a stale client re-bootstraps, never silently mutate a throwaway.
func TestLiveAction_unknownTabIsGone(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(clicker{}))
	t.Cleanup(srv.Close)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, 0, 0)

	resp, _ := post(t, srv, url, "{}", map[string]string{
		"Sec-Fetch-Site": "same-origin",
		"X-Via-Tab":      "nonexistent",
	})
	assert.Equal(t, http.StatusGone, resp.StatusCode)
}

// chatIsland mirrors example/chat (string messages) for an httptest fan-out
// check: a Send on one connection must reach every connection via the Topic.
type chatRoom struct{ bus *topic.Topic[string] }

type chatIsland struct {
	room  *chatRoom
	Draft via.Signal[string]
	Log   via.List[string]
}

func (c *chatIsland) OnInit(ctx *via.Ctx) error {
	ctx.Listen(c.room.bus, c.recv)
	return nil
}
func (c *chatIsland) recv(ctx *via.Ctx, m string) { c.Log.Append(m) }
func (c *chatIsland) Send(ctx *via.Ctx) {
	c.room.bus.Publish(c.Draft.Get())
	c.Draft.Set("")
}
func (c *chatIsland) row(m string) h.H { return h.Li(h.Str(m)) }
func (c *chatIsland) View() h.H {
	return h.Div(
		h.Ul(via.Each(c.Log.Get(), c.row)),
		h.Form(via.On("submit", c.Send), h.Input(c.Draft.Bind())),
	)
}

var actionIDRe = regexp.MustCompile(`/_via/a/([^')]+)`)

func actionID(t *testing.T, body string) string {
	t.Helper()
	m := actionIDRe.FindStringSubmatch(body)
	require.NotNil(t, m, "no action endpoint in page")
	return m[1]
}

// The headline: a message sent on one connection's live island fans out — via
// the Room's Topic — to EVERY connection, including a second tab.
func TestChat_messageFromOneTabFansOutToAnother(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		room := &chatRoom{bus: topic.New[string]()}
		srv := liveServer(t, via.Register(chatIsland{room: room}))

		la, ca := openStream(t, srv)
		defer ca()
		tabA := awaitTabID(t, la)
		lb, cb := openStream(t, srv)
		defer cb()
		_ = awaitTabID(t, lb)

		// Learn A's Send action id + the Draft signal slot from a page render
		// (positional/handle identity is deterministic, so it matches A's island).
		_, page := do(t, srv, http.MethodGet, "/", "")
		draftSlot := attrValue(t, page, "data-bind")
		sendID := actionID(t, page)

		resp, _ := post(t, srv, "/_via/a/"+sendID, `{"`+draftSlot+`":"hello-room"}`, map[string]string{
			"Sec-Fetch-Site": "same-origin",
			"X-Via-Tab":      tabA,
		})
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		awaitLine(t, lb, "hello-room") // the OTHER tab receives it — fan-out
		awaitLine(t, la, "hello-room") // and the sender does too
	})
}

// liveReqEchoer is a live island whose action copies a header off the request
// that triggered it into State.
type liveReqEchoer struct{ echo via.State[string] }

func (e *liveReqEchoer) Grab(ctx *via.Ctx) { e.echo.Set(ctx.Request().Header.Get("X-Echo")) }
func (e *liveReqEchoer) View() h.H {
	return h.Div(h.P(h.Str("echo: "), e.echo.Display()), h.Button(via.On("click", e.Grab), h.Str("x")))
}

// A live action runs on the island goroutine, yet it must still see the request
// that TRIGGERED it — the action POST. That POST carried X-Echo; the connect
// request never did, so the value surfacing over the SSE proves the triggering
// action request is threaded through (not the connect request).
func TestLiveAction_seesTheTriggeringActionRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(liveReqEchoer{}))
		conn := app.Connect()

		// X-Echo has no vt.Action builder method, so this posts by hand — but
		// the URL and tab still come off the connection, not a separate GET.
		req, err := http.NewRequest(http.MethodPost, app.URL()+conn.ActionURL(0, 0), strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Datastar-Request", "true")
		req.Header.Set("X-Via-Tab", conn.TabID())
		req.Header.Set("X-Echo", "from-the-action-post")
		resp, err := app.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		conn.Await("echo: from-the-action-post")
	})
}

// connReqEchoer reads the connect request in OnInit and a no-op tick forces a
// push so the read value is observable on the stream.
type connReqEchoer struct{ host via.State[string] }

func (e *connReqEchoer) OnInit(ctx *via.Ctx) error {
	e.host.Set(ctx.Request().Host)
	ctx.Tick(20*time.Millisecond, e.push)
	return nil
}
func (e *connReqEchoer) push(ctx *via.Ctx) {}
func (e *connReqEchoer) View() h.H {
	return h.Div(h.P(h.Str("host: "), e.host.Display()))
}

// OnInit must see the SSE connect request, so an island can authorize or
// inspect the connection at open time (the same request ticks and subscriptions
// then run under). The request Host is the server's own address; a pushed frame
// must reflect it — "example.com" is the fixed host of httptest's in-memory
// network, not a real loopback address.
func TestOnInit_seesTheConnectRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Register(connReqEchoer{}))

		lines, cancel := openStream(t, srv)
		defer cancel()
		awaitLine(t, lines, "host: example.com")
	})
}

// tickReqEchoer reads the connect request from inside a TICK body, not OnInit.
type tickReqEchoer struct{ host via.State[string] }

func (e *tickReqEchoer) OnInit(ctx *via.Ctx) error {
	ctx.Tick(20*time.Millisecond, e.tick)
	return nil
}
func (e *tickReqEchoer) tick(ctx *via.Ctx) { e.host.Set(ctx.Request().Host) }
func (e *tickReqEchoer) View() h.H {
	return h.Div(h.P(h.Str("tick-host: "), e.host.Display()))
}

// Ticks (and subscriptions) run under the island ctx, so a tick body reading
// ctx.Request() must see the connection's connect request — there is no
// triggering request for a timer, and the connection's is the honest answer.
// This locks that inherited contract, distinct from a handler that triggered an
// action.
func TestTick_seesTheConnectRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Register(tickReqEchoer{}))

		lines, cancel := openStream(t, srv)
		defer cancel()
		awaitLine(t, lines, "tick-host: example.com")
	})
}

// pathTicker's Tick reads ctx.Request().URL.Path — a live action's own POST
// (to /_via/a/0/0) must never be visible from there: before S8, dispatch
// wrote req/sessW/redirect directly onto the render-time Ctx a Tick holds for
// the life of the connection, so firing an action left every later tick
// reading the ACTION's request instead of the connect one.
type pathTicker struct {
	path via.State[string]
	n    via.State[int]
}

func (p *pathTicker) OnInit(ctx *via.Ctx) error {
	ctx.Tick(20*time.Millisecond, p.tick)
	return nil
}
func (p *pathTicker) tick(ctx *via.Ctx) { p.path.Set(ctx.Request().URL.Path) }
func (p *pathTicker) Bump(ctx *via.Ctx) { p.n.Set(p.n.Get() + 1) }
func (p *pathTicker) View() h.H {
	return h.Div(
		h.P(h.Str("path: "), p.path.Display()),
		h.P(h.Str("n: "), p.n.Display()),
		h.Button(via.On("click", p.Bump)),
	)
}

func TestLive_actionDoesNotOverwriteTheConnectCtxATickHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(pathTicker{}))
		conn := app.Connect()

		// Fire BEFORE the first tick has pushed — no fake time has advanced
		// yet, so the connection's current unit is still the exact Ctx object
		// OnInit handed to Tick. Dispatch writing req/sessW straight onto
		// that shared object (instead of a fresh per-action Ctx) would only be
		// observable in this narrow window; waiting for a push first (as the
		// old version of this test did) replaces the unit with a fresh render
		// Ctx, making the corruption invisible to any later tick.
		status, _ := app.Action(0).Live(conn).Fire()
		require.Equal(t, http.StatusNoContent, status)
		conn.Await("n: 1") // the action landed

		synctest.Wait()
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		conn.Await("path: /_via/sse") // the tick must still see the connect request, not the action's
	})
}

// An OnInit Redirect must gate the SSE connect itself, beyond the page GET
// and the action route — before A3 the stream handler ran OnInit at all, so
// a session check left a live page's push channel open to anyone who knew
// the URL even though the page and its actions were protected.
func TestLive_streamRunsOnInitRedirect(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/secret", secret{})
	srv := serve(t, r)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/secret/_via/sse", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := (&http.Client{CheckRedirect: noFollow}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode,
		"an OnInit Redirect must gate the SSE connect too, not just the page and action routes")
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

// onInitLive loads a field in OnInit, before the connect render ever runs —
// a live island's ticks fire on the connection's own goroutine, so its first
// pushed frame is the proof OnInit's field reached the persistent instance.
type onInitLive struct{ label string }

func (p *onInitLive) OnInit(ctx *via.Ctx) error {
	p.label = "loaded"
	ctx.Tick(time.Millisecond, func(*via.Ctx) {})
	return nil
}
func (p *onInitLive) View() h.H { return h.Div(h.Str(p.label)) }

// OnInit must run before the connect render binds the live unit, so a field
// it loads is already set by the time the first push renders — before A3 the
// stream handler never ran OnInit at all.
func TestLive_onInitRunsBeforeConnectRender(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Register(onInitLive{}))

		frame := readFirstFrame(t, srv)
		require.NotEmpty(t, frame)
		assert.Contains(t, strings.Join(frame, "\n"), "loaded")
	})
}

// livePushIsland is a live embedded island under a parametrised mount; Bump
// changes its visible count so its action's push is a real patch.
type livePushIsland struct {
	n    int
	seen via.State[int]
}

func (k *livePushIsland) Bump(ctx *via.Ctx) { k.n++ }
func (k *livePushIsland) View() h.H {
	return h.Div(h.Str(k.n), k.seen.Display(), h.Button(via.On("click", k.Bump)))
}

type livePushParent struct{ I livePushIsland }

func (p *livePushParent) View() h.H { return h.Div(via.Embed(p.I)) }

// A live push under a parametrised mount must carry the concrete path
// segment in its action URLs, not the literal "{id}" pattern wildcard —
// before A3 mount.connect closed over the pattern base instead of computing
// the concrete base per connection, so every push rendered a dead "{id}" URL.
func TestLive_pushUnderParamMountRendersConcreteBase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := via.NewRouter()
		r.Mount("/thread/{id}", livePushParent{})
		srv := liveServer(t, r)

		lines, cancel := openStreamAt(t, srv, "/thread/7/_via/sse")
		defer cancel()
		tab := awaitTabID(t, lines)

		_, page := do(t, srv, http.MethodGet, "/thread/7", "")
		resp, _ := post(t, srv, actionURL(t, page, 1, 0), "{}", map[string]string{
			"Sec-Fetch-Site": "same-origin", "X-Via-Tab": tab,
		})
		assert.Equal(t, http.StatusNoContent, resp.StatusCode, "the live action answers 204; the push carries the patch")

		awaitLine(t, lines, "/thread/7/_via/a/1/0")
	})
}

// renderCounter counts its own View calls so a test can assert on renders
// without reaching into via's internals — views is a pointer so it survives
// Register's per-connection value copy.
type renderCounter struct {
	views *atomic.Int64
	count via.State[int]
}

func (r *renderCounter) Bump(*via.Ctx) { r.count.Set(r.count.Get() + 1) }
func (r *renderCounter) View() h.H {
	r.views.Add(1)
	return h.Div(h.P(h.Str("count: "), r.count.Display()), h.Button(via.On("click", r.Bump)))
}

// A live action must run against the last render's table, not a fresh one of
// its own: connect renders once, and the action's own push renders once —
// never a bind render in between just to locate the action.
func TestLive_actionRunsWithoutPreRender(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		views := &atomic.Int64{}
		srv := liveServer(t, via.Register(renderCounter{views: views}))

		lines, cancel := openStream(t, srv)
		defer cancel()

		tab := awaitTabID(t, lines)
		require.NotEmpty(t, tab)
		require.EqualValues(t, 1, views.Load(), "the connect render is the only render so far")

		// A throwaway instance (its own views counter) discovers the current
		// action URL without adding a render to the instance under test — the
		// whole point of this test is counting THAT instance's View calls.
		digestSrv := liveServer(t, via.Register(renderCounter{views: &atomic.Int64{}}))
		_, page := do(t, digestSrv, http.MethodGet, "/", "")

		resp, _ := post(t, srv, actionURL(t, page, 0, 0), "{}", map[string]string{
			"Sec-Fetch-Site": "same-origin",
			"X-Via-Tab":      tab,
		})
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		awaitLine(t, lines, "count: 1")
		assert.EqualValues(t, 2, views.Load(), "a live action renders once — for the push — not twice")
	})
}

// A stale action id (a click racing a push, or a branched View that shifted
// the table) must 410, not silently do nothing — the client can then
// re-bootstrap instead of a click quietly having no effect. This carries a
// genuinely valid shape digest (read off the rendered page) with only n
// forged: a digest mismatch alone would 410 for the wrong reason ("stale
// page") and never reach the index-range check this guards.
func TestLive_outOfRangeActionAnswers410(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(clicker{}))
		conn := app.Connect()
		require.NotEmpty(t, conn.TabID())

		url := swapActionIndex(t, conn.ActionURL(0, 0), "99")
		status, _ := app.Action(0).Raw(url).Live(conn).Fire()
		assert.Equal(t, http.StatusGone, status)
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
func (f *flakyRender) Fix(ctx *via.Ctx)     { f.boom.Set(false) }
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
// take the whole island goroutine down: the action's own result (204) is
// already sent to the waiting POST before pushWork runs, so a dead goroutine
// here would strand every action after it behind a stream that looks alive
// but never dispatches again. Trigger's mutation succeeds and its 204
// answers; the render it provokes then panics. A second action (Fix) must
// still be dispatched and pushed normally, proving the island goroutine
// survived.
func TestLive_pushPanicDoesNotKillTheStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(flakyRender{}))
		conn := app.Connect()
		require.NotEmpty(t, conn.TabID())

		status, _ := app.Action(0).Live(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status, "the mutation applies and answers before its render panics")

		status, _ = app.Action(1).Live(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status, "the island goroutine must still be alive to dispatch a second action")
		conn.Await("n: 1")
	})
}

// liveArg is a live island with one value-carrying action, so a malformed
// ?a= can be exercised on the live dispatch path too (dispatchStateless has
// its own via_test coverage).
type liveArg struct{ last via.State[int] }

func (l *liveArg) Set(ctx *via.Ctx, v int) { l.last.Set(v) }
func (l *liveArg) View() h.H {
	return h.Div(l.last.Display(), h.Button(via.OnArg("click", l.Set, 7)))
}

// A malformed ?a= on a LIVE action must answer 400, not run the handler with
// a zero value nor 500 — and the island goroutine must survive to answer a
// later, well-formed action normally.
func TestLive_malformedActionArgAnswers400(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(liveArg{}))
		conn := app.Connect()

		url := strings.Replace(conn.ActionURL(0, 0), "a=7", "a=%22bad%22", 1)
		status, _ := app.Action(0).Raw(url).Live(conn).Fire()
		assert.Equal(t, http.StatusBadRequest, status)

		status, _ = app.Action(0).Live(conn).Fire() // well-formed, same slot
		assert.Equal(t, http.StatusNoContent, status, "the island goroutine must still be alive")
		conn.Await("7")
	})
}

// Neighbour of the malformed-arg case: a missing ?a= (empty, or "null") on a
// LIVE action must also answer 400, not run Set with the zero value.
func TestLive_missingActionArgAnswers400(t *testing.T) {
	tests := []struct {
		name string
		a    string
	}{
		{"empty", "a="},
		{"null", "a=null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				app := vt.Serve(t, via.Register(liveArg{}))
				conn := app.Connect()

				url := strings.Replace(conn.ActionURL(0, 0), "a=7", tt.a, 1)
				status, _ := app.Action(0).Raw(url).Live(conn).Fire()
				assert.Equal(t, http.StatusBadRequest, status)

				status, _ = app.Action(0).Live(conn).Fire() // well-formed, same slot
				assert.Equal(t, http.StatusNoContent, status, "the island goroutine must still be alive")
				conn.Await("7")
			})
		})
	}
}

// reTicker's beat handler calls Tick again on the same (already-connected)
// Ctx — nonsensical user code, but it must not silently register a second,
// invisible ticker; it must log loudly and otherwise no-op.
type reTicker struct{ n via.State[int] }

func (r *reTicker) OnInit(ctx *via.Ctx) error { ctx.Tick(10*time.Millisecond, r.beat); return nil }
func (r *reTicker) beat(ctx *via.Ctx) {
	r.n.Set(r.n.Get() + 1)
	ctx.Tick(time.Millisecond, r.beat) // called after OnInit returned
}
func (r *reTicker) View() h.H { return h.Div(r.n.Display()) }

// Sequential: it captures the global log output.
func TestLive_tickCalledAfterConnectIsALoudNoOp(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(reTicker{}))
		_ = app.Connect()
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
	})

	assert.Contains(t, buf.String(), "Tick called after OnInit returned",
		"a Tick call after OnInit must log loudly instead of silently registering nothing")
}

// reListener's recv handler calls Listen again on the same (already-connected)
// Ctx — same nonsensical case as reTicker, for Listen.
type reListener struct {
	room *topic.Topic[string]
	last via.State[string]
}

func (r *reListener) OnInit(ctx *via.Ctx) error {
	ctx.Listen(r.room, r.recv)
	return nil
}
func (r *reListener) recv(ctx *via.Ctx, msg string) {
	r.last.Set(msg)
	ctx.Listen(r.room, r.recv) // called after OnInit returned
}
func (r *reListener) View() h.H { return h.Div(r.last.Display()) }

// Sequential: it captures the global log output.
func TestLive_listenCalledAfterConnectIsALoudNoOp(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	room := topic.New[string]()
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Register(reListener{room: room}))
		lines, cancel := openStream(t, srv)
		defer cancel()
		_ = awaitTabID(t, lines)
		room.Publish("hello")
		awaitLine(t, lines, "hello")
	})

	assert.Contains(t, buf.String(), "Listen called after OnInit returned",
		"a Listen call after OnInit must log loudly instead of silently registering nothing")
}

// racyTicker ticks as fast as time.Ticker allows so its OnInit-scheduled
// push races liveConn.replace (island goroutine) against Bump's dispatchLive,
// which reads liveConn.units via unit() on the POST's own goroutine.
type racyTicker struct{ n via.State[int] }

func (r *racyTicker) OnInit(ctx *via.Ctx) error { ctx.Tick(time.Microsecond, r.tick); return nil }
func (r *racyTicker) tick(*via.Ctx)             { r.n.Set(r.n.Get() + 1) }
func (r *racyTicker) Bump(*via.Ctx)             {}
func (r *racyTicker) View() h.H {
	return h.Div(r.n.Display(), h.Button(via.On("click", r.Bump)))
}

// liveConn.mu guards units: a background tick's push runs replace
// on the island goroutine while a concurrent action POST reads the same maps
// via unit() on the dispatching request's own goroutine. This test runs both
// in real (not synctest-serialized) time and depth so -race — or the Go
// runtime's own concurrent-map-access panic — catches a missing lock; it
// asserts nothing about outcomes because the property under test is the
// absence of a race, not any particular response.
func TestLive_tickAndActionPOSTDoNotRaceOnConnState(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Register(racyTicker{}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, 0, 0)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				req, err := http.NewRequest(http.MethodPost, srv.URL+url, strings.NewReader("{}"))
				if err != nil {
					continue
				}
				req.Header.Set("Datastar-Request", "true")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				req.Header.Set("X-Via-Tab", tab)
				resp, err := srv.Client().Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}()
	}
	wg.Wait()
}

// racyNativeForm ticks as fast as time.Ticker allows so its OnInit-scheduled
// push races dispatchLive's native-form re-render, which (before the fix) ran
// renderRootBase against lc.pageRoot on the POST's own goroutine instead of
// the island goroutine.
type racyNativeForm struct{ n via.State[int] }

func (r *racyNativeForm) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Microsecond, r.tick)
	return nil
}
func (r *racyNativeForm) tick(*via.Ctx) { r.n.Set(r.n.Get() + 1) }
func (r *racyNativeForm) Save(*via.Ctx) {}
func (r *racyNativeForm) View() h.H {
	return h.Div(r.n.Display(), via.PostForm(r.Save, h.Button(h.Str("save"))))
}

// A native <form> submit on a live page re-renders lc.pageRoot in full
// (dispatchLive's modeNative branch) so the browser gets a whole document, not
// a patch. That render must run on the island's own serialized goroutine like
// every other read/write of live state — otherwise it races a concurrent tick.
// This runs in real (not synctest-serialized) time and concurrency so -race
// catches a regression; it asserts nothing about outcomes because the
// property under test is the absence of a race.
func TestLive_nativeFormPostAndTickDoNotRaceOnPageState(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Register(racyNativeForm{}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, 0, 0)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				body, ctype := multipartForm(t, map[string]string{"_viatab": tab})
				req, err := http.NewRequest(http.MethodPost, srv.URL+url, body)
				if err != nil {
					continue
				}
				req.Header.Set("Content-Type", ctype)
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				resp, err := srv.Client().Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}()
	}
	wg.Wait()
}

// abandonedAction is a trivial live action dispatched with a request context
// that is already canceled before ServeHTTP is even called — so
// req.Context().Err() is guaranteed non-nil from the very first instruction
// dispatch runs, isolating liveConn.run's first select (pulse-send vs.
// reqCtx.Done(), both ready at once) from any timing noise in an actual
// client/server round trip.
type abandonedAction struct {
	applied *atomic.Int32 // shared across Register's per-connection copy and the test's own handle
	n       via.State[int]
}

func (a *abandonedAction) Act(ctx *via.Ctx) { a.applied.Add(1) }
func (a *abandonedAction) View() h.H {
	return h.Div(a.n.Display(), h.Button(via.On("click", a.Act)))
}

// A live action must never mutate state once its own caller has already given
// up on it — before the fix, a closure handed off to the island goroutine
// (see liveConn.run) ran to completion regardless of whether the dispatching
// request's context was already done by the time the goroutine picked it up.
// Every one of these requests is abandoned from the start (its context is
// canceled before dispatch even begins), so every response this handler
// could possibly produce is one of ok=false or gone — never a mutation.
func TestLiveAction_abandonedRequestNeverAppliesAfterClientGivesUp(t *testing.T) {
	t.Parallel()
	root := &abandonedAction{applied: new(atomic.Int32)}
	handler := via.Register(*root)
	srv := liveServer(t, handler)

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	actURL := actionURL(t, page, 0, 0)

	const n = 3000
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // abandoned before the request is even dispatched
			req := httptest.NewRequest(http.MethodPost, actURL, strings.NewReader("{}")).WithContext(ctx)
			req.Header.Set("Datastar-Request", "true")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("X-Via-Tab", tab)
			handler.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(0), root.applied.Load(),
		"an action dispatched with an already-canceled request context must never apply")
}

// paramInTick calls ctx.Param from a Tick handler to prove the connection's
// bind carries a real request throughout its life (set once at OnInit),
// not just during the dispatched action that started the connection.
type paramInTick struct{ panics chan any }

func (p *paramInTick) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Millisecond, p.check)
	return nil
}
func (p *paramInTick) check(ctx *via.Ctx) {
	defer func() {
		// Non-blocking: the test only ever reads the first result, and Tick
		// keeps firing every 1ms — a blocking send here wedges the island
		// goroutine forever once the buffer is full, hanging server Close.
		select {
		case p.panics <- recover():
		default:
		}
	}()
	ctx.Param[int]("id") // this mount declares no {id} segment
}
func (p *paramInTick) View() h.H { return h.Div() }

// Param must not nil-dereference the request from a Tick handler: the
// connection's Ctx keeps the request it was connected with for its whole
// life, so Param answers the same "no such segment" panic a request-bound
// call would, not a raw nil-pointer crash.
func TestLive_paramInTickReadsConnectRequestNotNil(t *testing.T) {
	t.Parallel()
	panics := make(chan any, 1)
	srv := liveServer(t, via.Register(paramInTick{panics: panics}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	awaitTabID(t, lines)

	rec := <-panics
	require.NotNil(t, rec, "Param for an undeclared name must still panic")
	assert.Contains(t, fmt.Sprint(rec), "the mount pattern has no {id} segment",
		"a Tick's Ctx must carry the connect request, not nil")
}

// racyDirtySignal is a live counter with nothing else going on — every Inc
// dispatch's own push (dispatchLive's pushWork replaces lc.units[0] after
// every action, mutating or not) is the race: many concurrent Incs each read
// the connection's current unit and each replace it, so one dispatch's read
// can land on a unit a concurrent dispatch's push is about to make stale.
type racyDirtySignal struct {
	n    via.Signal[int]
	beat via.State[int]
}

func (r *racyDirtySignal) Inc(ctx *via.Ctx) { r.n.Set(r.n.Get() + 1) }
func (r *racyDirtySignal) View() h.H {
	return h.Div(r.n.Display(), r.beat.Display(), h.Button(via.On("click", r.Inc), h.Str("inc")))
}

// TestLiveAction_signalPatchSurvivesARacingPush proves every Inc dispatch that
// acks also ships its value over the SSE stream's signals-patch, even under a
// storm of concurrent Incs on the same connection. Before the fix
// (dispatchLive looking up its unit before handing off to the island
// goroutine instead of inside it), a concurrent push could replace the unit
// in between, and some values in 1..total never arrived.
func TestLiveAction_signalPatchSurvivesARacingPush(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Register(racyDirtySignal{}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, 0, 0)

	var seen sync.Map // values (int) observed in an "s0" signals-patch frame
	re := regexp.MustCompile(`"s0":(\d+)`)
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perGoroutine {
				req, err := http.NewRequest(http.MethodPost, srv.URL+url, strings.NewReader("{}"))
				if err != nil {
					continue
				}
				req.Header.Set("Datastar-Request", "true")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				req.Header.Set("X-Via-Tab", tab)
				resp, err := srv.Client().Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}()
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

// TestLiveAction_pushesStayInCommitOrderUnderConcurrentDispatch is the C9
// regression: liveRunAction's push (the dirty-signals patch, then the
// element patch) rides back as actionResult.pushWork and runs on the
// connection's own serialized goroutine, right after acking, in the exact
// order its mutation committed — not on a detached goroutine racing every
// other concurrent action's. n increments monotonically at commit time, so a
// correct stream shows every value in 1..total in strict, gapless order;
// reordering (the old fire-and-forget enqueue) breaks that order under
// concurrent dispatch.
func TestLiveAction_pushesStayInCommitOrderUnderConcurrentDispatch(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Register(racyDirtySignal{}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, 0, 0)

	re := regexp.MustCompile(`"s0":(\d+)`)
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perGoroutine {
				req, err := http.NewRequest(http.MethodPost, srv.URL+url, strings.NewReader("{}"))
				if err != nil {
					continue
				}
				req.Header.Set("Datastar-Request", "true")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				req.Header.Set("X-Via-Tab", tab)
				resp, err := srv.Client().Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}()
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
		"a live connection's pushes must ship in the order their mutations committed")
}

// listenOnly goes live purely by subscribing: OnInit registers a Listen and
// nothing else, and the View renders the last value it received.
type listenOnly struct {
	bus  *topic.Topic[string]
	last via.State[string]
}

func (l *listenOnly) OnInit(ctx *via.Ctx) error    { ctx.Listen(l.bus, l.onMsg); return nil }
func (l *listenOnly) onMsg(ctx *via.Ctx, s string) { l.last.Set(s) }
func (l *listenOnly) View() h.H                    { return h.Div(h.Str("last="), l.last.Display()) }

// OnInit runs on every request that renders the unit, plain GETs included, so
// Listen must register a starter rather than subscribe on the spot: a page
// fetched but never connected would otherwise orphan one Sub per GET in the
// Topic forever.
func TestListen_plainGetLeaksNoSubscription(t *testing.T) {
	t.Parallel()
	bus := topic.New[string]()
	app := vt.Serve(t, via.Register(listenOnly{bus: bus}))

	for range 3 {
		status, _ := app.Get("/")
		require.Equal(t, http.StatusOK, status)
	}
	assert.Zero(t, bus.Subs(), "a GET that never opened a stream must leave no subscription behind")

	c := app.Connect()
	defer c.Close()
	require.Eventually(t, func() bool { return bus.Subs() == 1 }, 2*time.Second, 5*time.Millisecond,
		"the connect itself must subscribe exactly once")
}

// A disconnect must give the subscription back — the lazy Subscribe is still
// paired with the disposer that stops it.
func TestListen_disconnectReturnsTheSubscription(t *testing.T) {
	t.Parallel()
	bus := topic.New[string]()
	app := vt.Serve(t, via.Register(listenOnly{bus: bus}))

	c := app.Connect()
	require.Eventually(t, func() bool { return bus.Subs() == 1 }, 2*time.Second, 5*time.Millisecond)
	c.Close()
	require.Eventually(t, func() bool { return bus.Subs() == 0 }, 2*time.Second, 5*time.Millisecond,
		"closing the stream must stop the subscription it started")
}
