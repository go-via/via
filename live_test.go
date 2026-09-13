package via_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime"
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

// feed is a live embed driven by a shared Topic: every connection subscribes,
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

func (d *disposeProbe) View() h.H { return h.Div(h.Str("probe"), d.n.Display()) }

// One publish must reach EVERY connected embed — that's the multi-user
// headline. Two streams subscribe; a single Publish to the shared Topic shows up
// on both.
func TestFeed_publishFansOutToEveryConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		room := topic.New[string]()
		srv := liveServer(t, via.Handler(feed{room: room}))

		l1, c1 := openStream(t, srv)
		defer c1()
		l2, c2 := openStream(t, srv)
		defer c2()

		room.Publish("hello-everyone")

		awaitLine(t, l1, "latest: hello-everyone")
		awaitLine(t, l2, "latest: hello-everyone")
	})
}

// mixedEmbed registers BOTH a tick and a subscription, plus a dispose probe.
type mixedEmbed struct {
	room     *topic.Topic[string]
	beats    via.State[int]
	last     via.State[string]
	disposed chan struct{}
}

func (m *mixedEmbed) OnInit(ctx *via.Ctx) error {
	ctx.Tick(15*time.Millisecond, m.beat)
	ctx.OnDispose(m.markDispose)
	ctx.Listen(m.room, m.recv)
	return nil
}

func (m *mixedEmbed) beat(ctx *via.Ctx) { m.beats.Set(m.beats.Get() + 1) }

func (m *mixedEmbed) recv(ctx *via.Ctx, msg string) { m.last.Set(msg) }

func (m *mixedEmbed) markDispose() { close(m.disposed) }

func (m *mixedEmbed) View() h.H {
	return h.Div(
		h.P(h.Str("beats: "), m.beats.Display()),
		h.P(h.Str("last: "), m.last.Display()),
	)
}

// Ticks and subscriptions share one embed loop: a ticking embed must also
// deliver published messages, and disconnecting a ticking+subscribed embed
// must still tear down cleanly (the ticker goroutine exits, disposers run).
func TestLive_tickAndSubscribeShareOneEmbedLoopAndTearDownCleanly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		room := topic.New[string]()
		done := make(chan struct{})
		srv := liveServer(t, via.Handler(mixedEmbed{room: room, disposed: done}))

		lines, cancel := openStream(t, srv)
		awaitLine(t, lines, "beats: ") // a tick frame flows
		room.Publish("from-topic")
		awaitLine(t, lines, "last: from-topic") // a sub frame flows alongside ticks

		cancel() // disconnect while the ticker is running
		synctest.Wait()
		select {
		case <-done:
		default:
			require.Fail(t, "OnDispose did not run when a ticking, subscribed embed disconnected")
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
	ctx.OnConnect(f.markAcquired)
	ctx.OnDispose(f.markDisposed)
	return errConnectBoom
}

func (f *failInit) markAcquired() { close(f.acquired) }

func (f *failInit) markDisposed() { close(f.disposed) }

func (f *failInit) View() h.H { return h.Div(h.Str("x"), f.n.Display()) }

var errConnectBoom = errorString("init boom")

type errorString string

func (e errorString) Error() string { return string(e) }

// A failed OnInit opens no connection, so neither half of an OnConnect/OnDispose
// pair may run: registering is not acquiring, and a Listen registered before
// the failure never subscribed either — nothing is left orphaned in a Topic.
func TestLive_failedInitRunsNeitherHalfOfThePair(t *testing.T) {
	t.Parallel()
	acquired, disposed := make(chan struct{}), make(chan struct{})
	resp, _ := do(t, serve(t, via.Handler(failInit{acquired: acquired, disposed: disposed})),
		http.MethodPost, "/_via/sse", "")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	select {
	case <-acquired:
		require.Fail(t, "OnConnect ran on a connect that never opened")
	default:
	}
	select {
	case <-disposed:
		require.Fail(t, "OnDispose ran on a connect that never opened")
	default:
	}
}

// On disconnect the embed's OnDispose must run, so subscriptions and producers
// are torn down rather than leaked for the life of the process.
func TestLive_onDisposeRunsWhenClientDisconnects(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(disposeProbe{disposed: done}))

		_, cancel := openStream(t, srv)
		cancel() // disconnect
		synctest.Wait()
	})

	// The stream goroutine runs on a real httptest listener, outside the
	// bubble (see I4): synctest.Wait() only settles bubble goroutines, so the
	// OS delivering the close can still be in flight after it returns. This
	// wait runs after the bubble, in real wall-clock time.
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

func (p *panicThenDisposeProbe) View() h.H { return h.Div(h.Str("probe"), p.n.Display()) }

// A panicking disposer must not skip every disposer registered after it — a
// skipped one (e.g. sub.Stop) would otherwise leak for the life of the
// process.
func TestLive_onDisposeContinuesAfterAPanickingDisposer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		srv := liveServer(t, via.Handler(panicThenDisposeProbe{disposed: done}))

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

// chatEmbed mirrors example/chat (string messages) for an httptest fan-out
// check: a Send on one connection must reach every connection via the Topic.
type chatRoom struct{ bus *topic.Topic[string] }

type chatEmbed struct {
	room  *chatRoom
	Draft via.Signal[string]
	Log   via.List[string]
}

func (c *chatEmbed) OnInit(ctx *via.Ctx) error {
	ctx.Listen(c.room.bus, c.recv)
	return nil
}

func (c *chatEmbed) recv(ctx *via.Ctx, m string) { c.Log.Append(m) }

func (c *chatEmbed) Send(ctx *via.Ctx) {
	c.room.bus.Publish(c.Draft.Get())
	c.Draft.Set("")
}

func (c *chatEmbed) row(m string) h.H { return h.Li(h.Str(m)) }

func (c *chatEmbed) View() h.H {
	return h.Div(
		h.Ul(via.Each(c.Log.Get(), c.row)),
		h.Form(via.On("submit", c.Send), h.Input(c.Draft.Bind())),
	)
}

// The headline: a message sent on one connection's live embed fans out — via
// the Room's Topic — to EVERY connection, including a second tab.
func TestChat_messageFromOneTabFansOutToAnother(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		room := &chatRoom{bus: topic.New[string]()}
		srv := liveServer(t, via.Handler(chatEmbed{room: room}))

		la, ca := openStream(t, srv)
		defer ca()
		tabA := awaitTabID(t, la)
		lb, cb := openStream(t, srv)
		defer cb()
		_ = awaitTabID(t, lb)

		// Learn A's Send action id + the Draft signal slot from a page render
		// (positional/handle identity is deterministic, so it matches A's embed).
		_, page := do(t, srv, http.MethodGet, "/", "")
		draftSlot := attrValue(t, page, "data-bind")
		sendID := actionID(t, page)

		resp, _ := post(t, srv, "/_via/a/"+sendID, withTab(tabA, `{"`+draftSlot+`":"hello-room"}`), map[string]string{
			"Sec-Fetch-Site": "same-origin",
		})
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		awaitLine(t, lb, "hello-room") // the OTHER tab receives it — fan-out
		awaitLine(t, la, "hello-room") // and the sender does too
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

// Ticks run under the embed ctx, so a tick body reading ctx.Request() must
// see the connection's connect request — there is no triggering request for
// a timer, and the connection's is the honest answer.
func TestTick_seesTheConnectRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(tickReqEchoer{}))

		lines, cancel := openStream(t, srv)
		defer cancel()
		awaitLine(t, lines, "tick-host: example.com")
	})
}

// pathTicker's Tick reads ctx.Request().URL.Path — a live action's own POST
// must never be visible from there. Before S8, dispatch wrote req/sessW/
// redirect directly onto the render-time Ctx a Tick holds for the life of
// the connection, so firing an action left every later tick reading the
// ACTION's request instead of the connect one.
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
		app := vt.Serve(t, via.Handler(pathTicker{}))
		conn := app.Connect()

		// Fire BEFORE the first tick pushes, while the connection's unit is
		// still the exact Ctx OnInit handed to Tick. Waiting for a push first
		// (the old version of this test) replaces the unit with a fresh
		// render Ctx, hiding the corruption this test exists to catch.
		status, _ := app.Action(0).Over(conn).Fire()
		require.Equal(t, http.StatusNoContent, status)
		conn.Await("n: 1") // the action landed

		synctest.Wait()
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		conn.Await("path: /_via/sse") // the tick must still see the connect request, not the action's
	})
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
		app := vt.Serve(t, via.Handler(reTicker{}))
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
		srv := liveServer(t, via.Handler(reListener{room: room}))
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
// push races tabStream.replace (stream goroutine) against Bump's dispatchOverStream,
// which reads tabStream.units via unit() on the POST's own goroutine.
type racyTicker struct{ n via.State[int] }

func (r *racyTicker) OnInit(ctx *via.Ctx) error { ctx.Tick(time.Microsecond, r.tick); return nil }

func (r *racyTicker) tick(*via.Ctx) { r.n.Set(r.n.Get() + 1) }

func (r *racyTicker) Bump(*via.Ctx) {}

func (r *racyTicker) View() h.H {
	return h.Div(r.n.Display(), h.Button(via.On("click", r.Bump)))
}

// tabStream.mu guards units: a background tick's push runs replace on the
// stream goroutine while a concurrent action POST reads it via unit() on its
// own goroutine. Runs in real (not synctest) time so -race, or Go's own
// concurrent-map-access panic, catches a missing lock; it asserts nothing
// about outcomes since the property under test is the absence of a race.
func TestLive_tickAndActionPOSTDoNotRaceOnConnState(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Handler(racyTicker{}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, "r", 0)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
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
		}()
	}
	wg.Wait()
}

// racyNativeForm ticks as fast as time.Ticker allows so its OnInit-scheduled
// push races dispatchOverStream's native-form re-render, which (before the fix) ran
// renderRootBase against lc.pageRoot on the POST's own goroutine instead of
// the stream goroutine.
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

// A native <form> submit on a streaming page re-renders lc.pageRoot in full
// (dispatchOverStream's modeNative branch), so that render must run on the
// embed's own serialized goroutine like every other live-state read/write —
// otherwise it races a concurrent tick. Runs in real time/concurrency so
// -race catches a regression; asserts nothing about outcomes.
func TestLive_nativeFormPostAndTickDoNotRaceOnPageState(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Handler(racyNativeForm{}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, "r", 0)

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
		// keeps firing every 1ms — a blocking send here wedges the embed
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
	srv := liveServer(t, via.Handler(paramInTick{panics: panics}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	awaitTabID(t, lines)

	rec := <-panics
	require.NotNil(t, rec, "Param for an undeclared name must still panic")
	assert.Contains(t, fmt.Sprint(rec), "the mount pattern has no {id} segment",
		"a Tick's Ctx must carry the connect request, not nil")
}

// listenOnly goes live purely by subscribing: OnInit registers a Listen and
// nothing else, and the View renders the last value it received.
type listenOnly struct {
	bus  *topic.Topic[string]
	last via.State[string]
}

func (l *listenOnly) OnInit(ctx *via.Ctx) error { ctx.Listen(l.bus, l.onMsg); return nil }

func (l *listenOnly) onMsg(ctx *via.Ctx, s string) { l.last.Set(s) }

func (l *listenOnly) View() h.H { return h.Div(h.Str("last="), l.last.Display()) }

// OnInit runs on every request that renders the unit, plain GETs included, so
// Listen must register a starter rather than subscribe on the spot: a page
// fetched but never connected would otherwise orphan one Sub per GET in the
// Topic forever.
func TestListen_plainGetLeaksNoSubscription(t *testing.T) {
	t.Parallel()
	bus := topic.New[string]()
	app := vt.Serve(t, via.Handler(listenOnly{bus: bus}))

	for range 3 {
		status, _ := app.Get("/")
		require.Equal(t, http.StatusOK, status)
	}
	assert.Zero(t, bus.NumSubs(), "a GET that never opened a stream must leave no subscription behind")

	c := app.Connect()
	defer c.Close()
	require.Eventually(t, func() bool { return bus.NumSubs() == 1 }, 2*time.Second, 5*time.Millisecond,
		"the connect itself must subscribe exactly once")
}

// A disconnect must give the subscription back — the lazy Subscribe is still
// paired with the disposer that stops it.
func TestListen_disconnectReturnsTheSubscription(t *testing.T) {
	t.Parallel()
	bus := topic.New[string]()
	app := vt.Serve(t, via.Handler(listenOnly{bus: bus}))

	c := app.Connect()
	require.Eventually(t, func() bool { return bus.NumSubs() == 1 }, 2*time.Second, 5*time.Millisecond)
	c.Close()
	require.Eventually(t, func() bool { return bus.NumSubs() == 0 }, 2*time.Second, 5*time.Millisecond,
		"closing the stream must stop the subscription it started")
}

type leakRoom struct {
	held  *atomic.Int32
	boom  bool
	beats *topic.Topic[int]
}

func (r *leakRoom) join() {
	r.held.Add(1)
	if r.boom {
		panic("via_test: OnConnect exploded")
	}
}

func (r *leakRoom) part() { r.held.Add(-1) }

func (r *leakRoom) got(ctx *via.Ctx, n int) {}

func (r *leakRoom) OnInit(ctx *via.Ctx) error {
	ctx.Listen(r.beats, r.got) // makes the unit live
	ctx.OnConnect(r.join)
	ctx.OnDispose(r.part)
	return nil
}

func (r *leakRoom) View() h.H { return h.Div(h.Str("room")) }

type leakPage struct{ A, B leakRoom }

func (p *leakPage) View() h.H { return h.Div(via.Embed(p.A), via.Embed(p.B)) }

// The second unit's OnConnect panics after the first has already acquired. The
// connect answers 500 — and the first unit's OnDispose must still run, or the
// acquire is stranded for the life of the process.
func TestLive_onLivePanicReleasesEarlierAcquires(t *testing.T) {
	t.Parallel()
	var held atomic.Int32
	beats := topic.New[int]()
	app := via.Handler(leakPage{
		A: leakRoom{held: &held, beats: beats},
		B: leakRoom{held: &held, beats: beats, boom: true},
	})
	resp, _ := do(t, serve(t, app), http.MethodPost, "/_via/sse", "")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Zero(t, held.Load(), "an acquire made before the panic was never released")
	assert.Zero(t, beats.NumSubs(), "the Listen subscription was never stopped")
}

// burstUnit counts handler calls and renders separately, which is the whole
// point of the contract: the two numbers must NOT be equal under a burst.
type burstUnit struct {
	bus     *topic.Topic[int]
	got     *atomic.Int64
	renders *atomic.Int64
	sum     via.State[int]
}

func (b *burstUnit) OnInit(ctx *via.Ctx) error { ctx.Listen(b.bus, b.recv); return nil }

func (b *burstUnit) recv(ctx *via.Ctx, v int) { b.got.Add(1); b.sum.Set(b.sum.Get() + v) }

func (b *burstUnit) View() h.H {
	b.renders.Add(1)
	return h.Div(h.Str("sum="), b.sum.Display())
}

// The defect: a burst wider than the old 64-slot buffer silently skipped
// handler calls, giving an accumulating handler a WRONG answer. The same
// burst must also cost far fewer renders than messages — drained into one
// batch, handled in order, pushed once.
func TestListen_burstLosesNoHandlerCallAndCoalescesRenders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const msgs = 1000
		bus := topic.New[int]()
		var got, renders atomic.Int64
		srv := liveServer(t, via.Handler(burstUnit{bus: bus, got: &got, renders: &renders}))

		lines, cancel := openStream(t, srv)
		defer cancel()
		go func() { //nolint:staticcheck // drain so a full lines channel never stalls the stream
			for range lines {
			}
		}()
		synctest.Wait()
		require.Equal(t, 1, bus.NumSubs(), "the stream must be subscribed before the burst")

		renders.Store(0)
		for range msgs {
			bus.Publish(1)
		}
		synctest.Wait()

		assert.Equal(t, int64(msgs), got.Load(), "a handler call was lost: the app's accumulated state is wrong")
		r := renders.Load()
		assert.Positive(t, r, "the batch must still produce a render")
		assert.Less(t, r, int64(msgs), "renders did not coalesce: %d messages cost %d renders", msgs, r)
		t.Logf("%d messages -> %d handler calls, %d renders", msgs, got.Load(), r)
	})
}

// selfPublisher listens to the very topic its handler publishes to. Its
// handler runs on the stream's push goroutine while the queue's only reader is
// the subscription goroutine blocked handing the next batch to that same
// (unbuffered) push channel — so a Publish that could block on a full
// subscriber queue closes the cycle and wedges the connection for good.
type selfPublisher struct {
	bus  *topic.Topic[int]
	got  *atomic.Int64
	seen via.State[int]
}

func (s *selfPublisher) OnInit(ctx *via.Ctx) error { ctx.Listen(s.bus, s.recv); return nil }

func (s *selfPublisher) recv(ctx *via.Ctx, v int) {
	s.got.Add(1)
	s.seen.Set(v)
	if v > 0 {
		s.bus.Publish(v - 1)
	}
}

func (s *selfPublisher) View() h.H { return h.Div(h.Str("n="), s.seen.Display()) }

func TestListen_unitPublishingToItsOwnTopicDoesNotDeadlock(t *testing.T) {
	t.Parallel()
	const seeds = 50
	bus := topic.New[int]()
	var got atomic.Int64
	srv := serve(t, via.Handler(selfPublisher{bus: bus, got: &got}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	go func() { //nolint:staticcheck
		for range lines {
		}
	}()
	require.Eventually(t, func() bool { return bus.NumSubs() == 1 }, 2*time.Second, 5*time.Millisecond)

	for range seeds {
		bus.Publish(1)
	}
	// Each seed is handled once and republished once: 2 handler calls apiece.
	require.Eventually(t, func() bool { return got.Load() == 2*seeds }, 5*time.Second, 5*time.Millisecond,
		"a unit publishing to the topic it listens to wedged its own stream (%d/%d handler calls)", got.Load(), 2*seeds)
}

// One client that never reads its socket must not hold up anyone else's
// delivery: each connection owns its goroutine and its own queue.
func TestListen_aClientThatNeverReadsDoesNotBlockOthers(t *testing.T) {
	t.Parallel()
	bus := topic.New[int]()
	var got, renders atomic.Int64
	srv := serve(t, via.Handler(burstUnit{bus: bus, got: &got, renders: &renders}))

	ctx, stall := context.WithCancel(context.Background())
	defer stall()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	stuck, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer stuck.Body.Close()

	lines, cancel := openStream(t, srv)
	defer cancel()
	require.Eventually(t, func() bool { return bus.NumSubs() == 2 }, 2*time.Second, 5*time.Millisecond)

	for range 500 {
		bus.Publish(1)
	}
	awaitLine(t, lines, "sum=")
	require.Eventually(t, func() bool { return got.Load() >= 500 }, 5*time.Second, 5*time.Millisecond,
		"a healthy client's deliveries were held up by a client that never reads")
}

// BenchmarkListen_burstFrames quantifies the coalescing half of the fix:
// renders (and therefore SSE frames + flush syscalls) per published message.
func BenchmarkListen_burstFrames(b *testing.B) {
	const msgs = 1000
	bus := topic.New[int]()
	var got, renders atomic.Int64
	srv := serve(b, via.Handler(burstUnit{bus: bus, got: &got, renders: &renders}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	if err != nil {
		b.Fatal(err)
	}
	defer resp.Body.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := resp.Body.Read(buf); err != nil {
				return
			}
		}
	}()
	for bus.NumSubs() == 0 {
		time.Sleep(time.Millisecond)
	}
	var total int64
	renders.Store(0)
	got.Store(0)
	for b.Loop() {
		for range msgs {
			bus.Publish(1)
		}
		total += msgs
		for got.Load() < total {
			time.Sleep(time.Millisecond)
		}
	}
	b.ReportMetric(float64(renders.Load())/float64(total), "renders/msg")
	b.ReportMetric(float64(got.Load())/float64(total)*100, "%delivered")
}

// orderUnit registers TWO Listens on one topic. Each used to own a reader
// goroutine, so which handler saw a value first was a scheduler race; both now
// run from the connection's single select loop, in registration order.
type orderUnit struct {
	bus  *topic.Topic[int]
	mu   *sync.Mutex
	seen *[]string
}

func (u *orderUnit) OnInit(ctx *via.Ctx) error {
	ctx.Listen(u.bus, u.first)
	ctx.Listen(u.bus, u.second)
	ctx.Tick(time.Hour, u.idle)
	return nil
}

func (u *orderUnit) idle(ctx *via.Ctx) {}

func (u *orderUnit) record(tag string, v int) {
	u.mu.Lock()
	*u.seen = append(*u.seen, tag+strconv.Itoa(v))
	u.mu.Unlock()
}

func (u *orderUnit) first(ctx *via.Ctx, v int) { u.record("a", v) }

func (u *orderUnit) second(ctx *via.Ctx, v int) { u.record("b", v) }

func (u *orderUnit) View() h.H { return h.Div(h.Str("order")) }

func TestListen_handlerOrderIsRegistrationOrderNotAGoroutineRace(t *testing.T) {
	t.Parallel()
	bus := topic.New[int]()
	var mu sync.Mutex
	var seen []string
	srv := serve(t, via.Handler(orderUnit{bus: bus, mu: &mu, seen: &seen}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	awaitTabID(t, lines)
	require.Eventually(t, func() bool { return bus.NumSubs() == 2 }, 2*time.Second, time.Millisecond)

	const n = 50
	for i := range n {
		bus.Publish(i)
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(seen) == 2*(i+1)
		}, 2*time.Second, time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	var want []string
	for i := range n {
		want = append(want, "a"+strconv.Itoa(i), "b"+strconv.Itoa(i))
	}
	assert.Equal(t, want, seen, "two Listens on one unit must run in registration order, every time")
}

// BenchmarkListen_goroutinesPerConnection reports the thing the fold was for:
// how many goroutines each live connection costs when it holds three Listens.
// One reader goroutine per subscription per connection was ~1/3 of all
// goroutines at scale.
func BenchmarkListen_goroutinesPerConnection(b *testing.B) {
	bus := topic.New[int]()
	var got atomic.Int64
	var renders atomic.Int64
	srv := serve(b, via.Handler(triListen{bus: bus, got: &got, renders: &renders}))

	const conns = 200
	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for range conns {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		resp, err := srv.Client().Do(req)
		if err != nil {
			b.Fatal(err)
		}
		go func() {
			defer resp.Body.Close()
			buf := make([]byte, 4096)
			for {
				if _, err := resp.Body.Read(buf); err != nil {
					return
				}
			}
		}()
	}
	for bus.NumSubs() < conns*3 {
		time.Sleep(time.Millisecond)
	}
	perConn := float64(runtime.NumGoroutine()-base) / conns
	for b.Loop() {
		bus.Publish(1)
	}
	b.ReportMetric(perConn, "goroutines/conn")
}

// triListen is the benchmark's shape: one live unit holding three Listens.
type triListen struct {
	bus     *topic.Topic[int]
	got     *atomic.Int64
	renders *atomic.Int64
	n       via.State[int]
}

func (u *triListen) OnInit(ctx *via.Ctx) error {
	ctx.Listen(u.bus, u.take)
	ctx.Listen(u.bus, u.take)
	ctx.Listen(u.bus, u.take)
	return nil
}

func (u *triListen) take(ctx *via.Ctx, v int) { u.got.Add(1) }

func (u *triListen) View() h.H {
	u.renders.Add(1)
	return h.Div(u.n.Display())
}

type panicListener struct {
	bus  *topic.Topic[int]
	got  *atomic.Int64
	seen *atomic.Int64 // bitmask of the values whose handler ran to completion
}

func (b *panicListener) OnInit(ctx *via.Ctx) error { ctx.Listen(b.bus, b.recv); return nil }

func (b *panicListener) recv(ctx *via.Ctx, v int) {
	b.got.Add(1)
	if v == 2 {
		panic("handler blew up on value 2")
	}
	b.seen.Or(1 << v)
}

func (b *panicListener) View() h.H { return h.Div(h.Str("seen="), h.Str(int(b.seen.Load()))) }

// The defect: the whole drained batch ran as ONE push item, so a panic on value
// 2 skipped 3,4,5 AND the re-render — a bad value cost the rest of the backlog
// and the frame the survivors earned.
func TestLive_panicInOneListenHandlerDoesNotDropTheBatch(t *testing.T) {
	t.Parallel()
	bus := topic.New[int]()
	var got, seen atomic.Int64
	srv := liveServer(t, via.Handler(panicListener{bus: bus, got: &got, seen: &seen}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	awaitTabID(t, lines)

	for v := 1; v <= 5; v++ {
		bus.Publish(v)
	}

	// 0b111010 = values 1,3,4,5 completed; 2 panicked.
	awaitLine(t, lines, "seen=58")
	assert.EqualValues(t, 5, got.Load(), "every value must still reach the handler")
}
