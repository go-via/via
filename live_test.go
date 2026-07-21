package via_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
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

// A live stream must emit a periodic keepalive even on an island with no ticks:
// a successful write proves the peer is still there, and a FAILED write is the
// only in-band way to notice a silently-dropped (half-open) peer so the island
// goroutine and its timers don't leak. It must be an SSE comment frame, not a
// signal/element patch, so it never mutates client state.
func TestKeepalive_quietStreamStillBeatsSoHalfOpenPeersAreDetected(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(quietIsland{}, via.WithSSEHeartbeat(15*time.Millisecond)))
	conn := app.Connect()

	line := conn.Await(": keepalive")
	assert.True(t, strings.HasPrefix(strings.TrimSpace(line), ":"),
		"keepalive must be an SSE comment frame (starts with ':'), not a data/event line")

	// A second beat proves the keepalive is periodic, not a one-shot frame on
	// connect — only a recurring write can detect a peer that vanishes later.
	conn.Await(": keepalive")
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
	t.Parallel()
	done := make(chan struct{})
	handler := via.Register(disposeProbe{disposed: done}, via.WithSSEHeartbeat(15*time.Millisecond))
	// httptest.NewRequest's context is never cancelled, so the ONLY thing that
	// can end the stream here is the failed keepalive write — isolating that path.
	req := httptest.NewRequest(http.MethodPost, "/_via/sse", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin") // past the origin floor, as a real browser would

	go handler.ServeHTTP(&halfOpenFlusher{}, req)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a failed keepalive write must tear the island down (run disposers); it leaked instead")
	}
}

// The per-frame write deadline guards against a stalled peer pinning the single
// goroutine — but it must not kill a healthy, merely-slow client. A normal
// stream with a write timeout configured must still deliver its frames.
func TestLive_perFrameWriteDeadlineDoesNotBreakAHealthyStream(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(quietIsland{},
		via.WithSSEHeartbeat(15*time.Millisecond), via.WithSSEWriteTimeout(2*time.Second)))
	conn := app.Connect()

	conn.Await(": keepalive")
}

// pulse is a live island: implementing OnConnect opts it into a server-push SSE
// stream. A server-side ticker increments a beat count; via re-renders and
// pushes the fragment, so the browser updates with no client code.
type pulse struct{ beats via.State[int] }

func (p *pulse) OnConnect(ctx *via.Ctx) error {
	ctx.Tick(20*time.Millisecond, p.beat)
	return nil
}
func (p *pulse) beat(ctx *via.Ctx) { p.beats.Set(p.beats.Get() + 1) }
func (p *pulse) View() h.H {
	return h.Div(h.H1(h.Str("pulse")), h.P(h.Str("beats: "), p.beats.Display()))
}

func newPulse(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(via.Register(pulse{}))
	t.Cleanup(srv.Close)
	return srv
}

// multiline is a live island whose rendered content contains a newline. The SSE
// framing must survive it.
type multiline struct{ s string }

func (m *multiline) OnConnect(ctx *via.Ctx) error {
	ctx.Tick(15*time.Millisecond, m.set)
	return nil
}
func (m *multiline) set(*via.Ctx) { m.s = "top\nbottom" }
func (m *multiline) View() h.H    { return h.Div(h.P(h.Str(m.s))) }

// quietIsland is a live composition that registers no ticks — the stream must
// still open and hold cleanly, not panic or wedge.
type quietIsland struct{}

func (q *quietIsland) OnConnect(*via.Ctx) error { return nil }
func (q *quietIsland) View() h.H                { return h.Div(h.Str("quiet")) }

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
			t.Fatal("no datastar-patch-elements frame arrived")
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
// returns once headers are flushed — which happens after OnConnect — so the
// subscription is registered by the time this returns.
func openStream(t *testing.T, srv *httptest.Server) (<-chan string, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin") // mimic a same-origin browser SSE fetch past the origin floor
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	lines := make(chan string, 256)
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
		close(lines)
	}()
	return lines, cancel
}

func awaitLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for a line containing %q", want)
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
			t.Fatal("no _viatab signals frame arrived")
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
	t.Parallel()
	srv := httptest.NewServer(via.Register(multiline{}))
	t.Cleanup(srv.Close)

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
}

// A live island that registers no ticks must still open the stream cleanly.
func TestLive_streamOpensWithNoTicks(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(quietIsland{}))
	t.Cleanup(srv.Close)

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
}

// A live page must server-render its initial View (no empty flash) and carry a
// single bootstrap that opens the per-tab SSE stream, or the island never goes
// live in the browser.
func TestLivePage_serverRendersAndBootstrapsTheStream(t *testing.T) {
	t.Parallel()
	_, body := do(t, newPulse(t), http.MethodGet, "/", "")
	assert.Contains(t, body, `<div id="root"`)
	assert.Contains(t, body, "beats: 0",
		"State must render its zero value at first paint (before OnConnect) without panicking")
	assert.Contains(t, body, `data-init="@post('/_via/sse')"`, "page must bootstrap the SSE stream")
}

// The SSE endpoint must stream Datastar element-patch frames: text/event-stream,
// an `event: datastar-patch-elements` line, and a `data: elements <#root …>`
// line carrying the re-rendered fragment with an advanced beat — the live push.
func TestLive_streamsElementPatchFramesThatMorphRoot(t *testing.T) {
	t.Parallel()
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
			t.Fatal("no datastar-patch-elements frame arrived")
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
}

// feed is a live island driven by a shared Topic: every connection subscribes,
// and a published message fans out to all of them and is shown live.
type feed struct {
	room *topic.Topic[string]
	last via.State[string]
}

func (f *feed) OnConnect(ctx *via.Ctx) error {
	sub := f.room.Subscribe()
	ctx.OnDispose(sub.Stop) // method value — deterministic teardown
	via.Subscribe(ctx, sub.C(), f.recv)
	return nil
}
func (f *feed) recv(ctx *via.Ctx, msg string) { f.last.Set(msg) }
func (f *feed) View() h.H {
	return h.Div(h.H1(h.Str("feed")), h.P(h.Str("latest: "), f.last.Display()))
}

// disposeProbe signals a channel from its OnDispose so a test can observe that
// teardown ran on disconnect.
type disposeProbe struct{ disposed chan struct{} }

func (d *disposeProbe) OnConnect(ctx *via.Ctx) error {
	ctx.OnDispose(d.markDisposed)
	return nil
}
func (d *disposeProbe) markDisposed() { close(d.disposed) }
func (d *disposeProbe) View() h.H     { return h.Div(h.Str("probe")) }

// One publish must reach EVERY connected island — that's the multi-user
// headline. Two streams subscribe; a single Publish to the shared Topic shows up
// on both.
func TestFeed_publishFansOutToEveryConnection(t *testing.T) {
	t.Parallel()
	room := topic.New[string]()
	srv := httptest.NewServer(via.Register(feed{room: room}))
	t.Cleanup(srv.Close)

	l1, c1 := openStream(t, srv)
	defer c1()
	l2, c2 := openStream(t, srv)
	defer c2()

	room.Publish("hello-everyone")

	awaitLine(t, l1, "latest: hello-everyone")
	awaitLine(t, l2, "latest: hello-everyone")
}

// mixedIsland registers BOTH a tick and a subscription, plus a dispose probe.
type mixedIsland struct {
	room     *topic.Topic[string]
	beats    via.State[int]
	last     via.State[string]
	disposed chan struct{}
}

func (m *mixedIsland) OnConnect(ctx *via.Ctx) error {
	ctx.Tick(15*time.Millisecond, m.beat)
	sub := m.room.Subscribe()
	ctx.OnDispose(sub.Stop)
	ctx.OnDispose(m.markDispose)
	via.Subscribe(ctx, sub.C(), m.recv)
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
	t.Parallel()
	room := topic.New[string]()
	done := make(chan struct{})
	srv := httptest.NewServer(via.Register(mixedIsland{room: room, disposed: done}))
	t.Cleanup(srv.Close)

	lines, cancel := openStream(t, srv)
	awaitLine(t, lines, "beats: ") // a tick frame flows
	room.Publish("from-topic")
	awaitLine(t, lines, "last: from-topic") // a sub frame flows alongside ticks

	cancel() // disconnect while the ticker is running
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnDispose did not run when a ticking, subscribed island disconnected")
	}
}

// failConnect registers a disposer, then OnConnect fails.
type failConnect struct{ disposed chan struct{} }

func (f *failConnect) OnConnect(ctx *via.Ctx) error {
	ctx.OnDispose(f.markDisposed)
	return errConnectBoom
}
func (f *failConnect) markDisposed() { close(f.disposed) }
func (f *failConnect) View() h.H     { return h.Div(h.Str("x")) }

var errConnectBoom = errorString("connect boom")

type errorString string

func (e errorString) Error() string { return string(e) }

// If OnConnect fails after registering disposers (a Topic Subscribe is paired
// with OnDispose(sub.Stop) before a later step errors), those disposers must
// still run — otherwise the subscription is orphaned in the Topic forever and
// presence stays inflated.
func TestLive_disposersRunWhenOnConnectFails(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	resp, _ := do(t, serve(t, via.Register(failConnect{disposed: done})), http.MethodPost, "/_via/sse", "")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OnConnect-error path did not run disposers — the subscription leaks")
	}
}

// notFoundConnect's OnConnect returns via.ErrNotFound — the live analogue of a
// page whose data vanished. The connect must answer 404, not 500: the world
// changed, the request is honest. Fails if the sentinel stops mapping to 404.
type notFoundConnect struct{}

func (f *notFoundConnect) OnConnect(ctx *via.Ctx) error { return via.ErrNotFound }
func (f *notFoundConnect) View() h.H                    { return h.Div(h.Str("x")) }

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
	srv := httptest.NewServer(via.Register(disposeProbe{disposed: done}))
	t.Cleanup(srv.Close)

	_, cancel := openStream(t, srv)
	cancel() // disconnect

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnDispose did not run on disconnect")
	}
}

// clicker is a live island whose action mutates its OWN server State. The proof
// of correct routing: after the POST, the patch must arrive over THIS
// connection's SSE (not as the POST body), which only happens if the action ran
// against this connection's island instance — not a throwaway per-request copy.
type clicker struct{ count via.State[int] }

func (c *clicker) Bump(ctx *via.Ctx)            { c.count.Set(c.count.Get() + 1) }
func (c *clicker) OnConnect(ctx *via.Ctx) error { return nil }
func (c *clicker) View() h.H {
	return h.Div(h.P(h.Str("count: "), c.count.Display()), h.Button(via.OnClick(c.Bump), h.Str("+")))
}

func TestLiveAction_mutatesThisConnectionsStateAndPushesOverItsSSE(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(clicker{}))
	t.Cleanup(srv.Close)

	lines, cancel := openStream(t, srv)
	defer cancel()

	tab := awaitTabID(t, lines)
	require.NotEmpty(t, tab, "the SSE must hand the client its tab id")

	// Simulate Datastar's @post(...,{headers:{'X-Via-Tab':$_viatab}}).
	resp, _ := post(t, srv, "/_via/a/0", "{}", map[string]string{
		"Sec-Fetch-Site": "same-origin",
		"X-Via-Tab":      tab,
	})
	assert.Equal(t, http.StatusNoContent, resp.StatusCode, "a live action acks 204; the patch ships over the SSE")

	awaitLine(t, lines, "count: 1") // the mutation reaches THIS connection
}

// A live action POST with no/unknown tab id (no live connection to route to)
// must 410 so a stale client re-bootstraps, never silently mutate a throwaway.
func TestLiveAction_unknownTabIsGone(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(clicker{}))
	t.Cleanup(srv.Close)

	resp, _ := post(t, srv, "/_via/a/0", "{}", map[string]string{
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

func (c *chatIsland) OnConnect(ctx *via.Ctx) error {
	sub := c.room.bus.Subscribe()
	ctx.OnDispose(sub.Stop)
	via.Subscribe(ctx, sub.C(), c.recv)
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
		h.Form(via.OnSubmit(c.Send), h.Input(c.Draft.Bind())),
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
	t.Parallel()
	room := &chatRoom{bus: topic.New[string]()}
	srv := httptest.NewServer(via.Register(chatIsland{room: room}))
	t.Cleanup(srv.Close)

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
}

// liveReqEchoer is a live island whose action copies a header off the request
// that triggered it into State.
type liveReqEchoer struct{ echo via.State[string] }

func (e *liveReqEchoer) Grab(ctx *via.Ctx)            { e.echo.Set(ctx.Request().Header.Get("X-Echo")) }
func (e *liveReqEchoer) OnConnect(ctx *via.Ctx) error { return nil }
func (e *liveReqEchoer) View() h.H {
	return h.Div(h.P(h.Str("echo: "), e.echo.Display()), h.Button(via.OnClick(e.Grab), h.Str("x")))
}

// A live action runs on the island goroutine, yet it must still see the request
// that TRIGGERED it — the action POST. That POST carried X-Echo; the connect
// request never did, so the value surfacing over the SSE proves the triggering
// action request is threaded through (not the connect request).
func TestLiveAction_seesTheTriggeringActionRequest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(liveReqEchoer{}))
	t.Cleanup(srv.Close)

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	resp, _ := post(t, srv, "/_via/a/0", "{}", map[string]string{
		"Sec-Fetch-Site": "same-origin",
		"X-Via-Tab":      tab,
		"X-Echo":         "from-the-action-post",
	})
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	awaitLine(t, lines, "echo: from-the-action-post")
}

// connReqEchoer reads the connect request in OnConnect and a no-op tick forces a
// push so the read value is observable on the stream.
type connReqEchoer struct{ host via.State[string] }

func (e *connReqEchoer) OnConnect(ctx *via.Ctx) error {
	e.host.Set(ctx.Request().Host)
	ctx.Tick(20*time.Millisecond, e.push)
	return nil
}
func (e *connReqEchoer) push(ctx *via.Ctx) {}
func (e *connReqEchoer) View() h.H {
	return h.Div(h.P(h.Str("host: "), e.host.Display()))
}

// OnConnect must see the SSE connect request, so an island can authorize or
// inspect the connection at open time (the same request ticks and subscriptions
// then run under). The request Host is the server's own address; a pushed frame
// must reflect it.
func TestOnConnect_seesTheConnectRequest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(connReqEchoer{}))
	t.Cleanup(srv.Close)

	lines, cancel := openStream(t, srv)
	defer cancel()
	awaitLine(t, lines, "host: 127.0.0.1")
}

// tickReqEchoer reads the connect request from inside a TICK body, not OnConnect.
type tickReqEchoer struct{ host via.State[string] }

func (e *tickReqEchoer) OnConnect(ctx *via.Ctx) error {
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
	t.Parallel()
	srv := httptest.NewServer(via.Register(tickReqEchoer{}))
	t.Cleanup(srv.Close)

	lines, cancel := openStream(t, srv)
	defer cancel()
	awaitLine(t, lines, "tick-host: 127.0.0.1")
}
