package via_test

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestList_appendAddsElementsInOrder(t *testing.T) {
	t.Parallel()
	var l via.List[string]
	l.Append("a")
	l.Append("b")
	assert.Equal(t, []string{"a", "b"}, l.Get())
}

// hookless holds server State and nothing else — no OnInit, no Tick, no
// Listen. Rendering the State is its only claim to a connection.
type hookless struct{ v via.State[int] }

func (s *hookless) View() h.H { return h.Div(h.Str("n="), s.v.Display()) }

func TestState_makesItsUnitLiveWithNoHook(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(hookless{}))

	status, body := app.Get("/")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `data-init="@post('/_via/sse')"`,
		"a page whose View renders State must bootstrap the stream")

	c := app.Connect()
	defer c.Close()
	assert.NotEmpty(t, c.TabID(), "the connect must hand back a tab id, not 404")
}

// stateEcho is a live child whose action writes a user-influenced value into
// server State, which is then re-rendered and pushed over the connection's SSE.
type stateEcho struct{ msg via.State[string] }

func (e *stateEcho) Set(ctx *via.Ctx) { e.msg.Set("<b>Ada</b>") }
func (e *stateEcho) View() h.H {
	return h.Div(
		h.P(h.Str("msg: "), e.msg.Display()),
		h.Button(via.On("click", e.Set), h.Str("set")),
	)
}

func TestState_rendersEscapedValueOnALiveChild(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(stateEcho{}))
	conn := app.Connect()

	status, _ := app.Action(0).Over(conn).Fire()
	assert.Equal(t, http.StatusNoContent, status, "a live action acks 204; the render ships over the SSE")

	frame := conn.Await("&lt;b&gt;Ada&lt;/b&gt;")
	assert.Contains(t, frame, "msg: ", "the frame must re-render the child's State")
	assert.NotContains(t, frame, "<b>Ada", "the raw value must not survive into markup")
}

func TestState_bareSetAndAppend(t *testing.T) {
	t.Parallel()
	var s via.State[int]
	s.Set(7)
	assert.Equal(t, 7, s.Get())

	var l via.List[string]
	l.Append("a")
	l.Append("b")
	assert.Equal(t, []string{"a", "b"}, l.Get())
}

func TestList_remove(t *testing.T) {
	t.Parallel()
	var l via.List[string]
	l.Append("a")
	l.Append("b")
	l.Append("c")

	l.Remove(1)
	assert.Equal(t, []string{"a", "c"}, l.Get(), "Remove shifts the rest left")
}

func TestList_outOfRangeIndexPanics(t *testing.T) {
	t.Parallel()
	newList := func() *via.List[string] {
		var l via.List[string]
		l.Append("a")
		return &l
	}
	assert.Panics(t, func() { newList().Remove(1) }, "Remove past the end")
}

func TestList_removeZeroesTheFreedSlot(t *testing.T) {
	t.Parallel()
	var l via.List[*string]
	a, b := "a", "b"
	l.Append(&a)
	l.Append(&b)
	full := l.Get()[:2]
	l.Remove(0)
	assert.Nil(t, full[1], "the vacated tail slot must be zeroed, not left aliasing the element")
}

// listChild is the canonical case item 04 claimed was impossible: a live list
// a user can delete a row from. Each row carries a stable id so the morph
// matches by identity rather than by position.
type listChild struct{ items via.List[string] }

func (t *listChild) DropFirst(ctx *via.Ctx) { t.items.Remove(0) }
func (t *listChild) View() h.H {
	return h.Div(
		t.items.Each(func(s string) h.H {
			return h.P(h.RawAttr("id", "row-"+s), h.Str(s))
		}),
		// A marker that changes with the length, so a test can await the
		// post-removal frame rather than matching the row it expects gone.
		h.P(h.RawAttr("id", "count-"+strconv.Itoa(len(t.items.Get())))),
		h.Button(via.On("click", t.DropFirst), h.Str("drop")),
	)
}

func TestList_removeReachesTheBrowser(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(listChild{items: newListItems("drop", "keep")}))
	status, first := app.Get("/")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, first, "count-2", "both rows render on the first paint")
	assert.Contains(t, first, "row-drop")

	c := app.Connect()
	defer c.Close()
	// A live child's action answers 204: the re-render travels on the
	// connection as a patch frame, not in the action's own response body.
	status, _ = app.Action(0).Over(c).Fire()
	assert.Equal(t, http.StatusNoContent, status)
	patch := c.Await("count-1")
	assert.NotContains(t, patch, "row-drop", "the dropped row must be gone from the pushed patch")
	assert.Contains(t, patch, "row-keep", "the surviving row must still render")
}

// eachListChild renders via l.Each(row) — the List method that sugars over
// via.Each(l.Get(), row) — rather than calling via.Each directly.
type eachListChild struct{ items via.List[string] }

func (e *eachListChild) View() h.H        { return h.Ul(e.items.Each(e.row)) }
func (e *eachListChild) row(s string) h.H { return h.Li(h.Str(s)) }

func TestList_eachRendersRowsInOrder(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(eachListChild{items: newListItems("one", "two", "three")}))
	status, body := app.Get("/")
	assert.Equal(t, http.StatusOK, status)
	assert.Regexp(t, "<li>one</li>.*<li>two</li>.*<li>three</li>", body,
		"List.Each must render rows in the list's order")
}

func newListItems(items ...string) via.List[string] {
	var l via.List[string]
	for _, s := range items {
		l.Append(s)
	}
	return l
}

// hooklessCounter is live purely because its View renders State — no OnInit,
// no Tick, no Listen — and its action must still route to this connection's
// own instance and push the re-render over the stream.
type hooklessCounter struct{ n via.State[int] }

func (c *hooklessCounter) Inc(ctx *via.Ctx) { c.n.Set(c.n.Get() + 1) }
func (c *hooklessCounter) View() h.H {
	return h.Div(h.Str("n="), c.n.Display(), h.Button(via.On("click", c.Inc)))
}

func TestState_liveActionOnAHooklessUnitPushesItsPatch(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(hooklessCounter{}))
	c := app.Connect()
	defer c.Close()

	status, _ := app.Action(0).Over(c).Fire()
	require.Equal(t, http.StatusNoContent, status,
		"a live action answers 204 — its re-render travels on the stream, not in the response")
	assert.Contains(t, c.Await("n="), "n=1", "the mutation must reach the browser as a patch frame")
}

type lateLive struct {
	open bool
	Msg  via.State[string]
}

func (p *lateLive) Open(ctx *via.Ctx) { p.open = true }

func (p *lateLive) msg() h.H { return p.Msg.Display() }

func (p *lateLive) View() h.H {
	return h.Div(
		h.Button(via.On("click", p.Open), h.Str("open")),
		via.When(p.open, p.msg),
	)
}

func TestState_actionThatTurnsThePageLiveFails(t *testing.T) {
	// Sequential: it captures the global log output.
	app := via.Handler(lateLive{})
	rec := httptest.NewRecorder()
	rec.Body = &bytes.Buffer{}
	get := httptest.NewRecorder()
	app.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/", nil))
	require.NotContains(t, get.Body.String(), "/_via/sse", "page must be served plain")
	m := regexp.MustCompile(`@post\('([^']+)'`).FindStringSubmatch(get.Body.String())
	require.Len(t, m, 2)

	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	req := httptest.NewRequest(http.MethodPost, m[1], strings.NewReader(`{}`))
	req.Header.Set("Datastar-Request", "true")
	app.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, logs.String(), "made a unit live")
}

// seededChild is embedded by value, so its starting State must come from a
// composite literal and not from a Set inside a callback.
type seededChild struct {
	Room  via.State[string]
	Lines via.List[string]
}

func (c *seededChild) View() h.H {
	return h.Div(h.P(c.Room.Display()), h.Ul(c.Lines.Each(liLine)))
}

func liLine(s string) h.H { return h.Li(h.Str(s)) }

type seededParent struct{ Child seededChild }

func (p *seededParent) View() h.H { return h.Div(via.Child(p.Child)) }

func TestStateOf_seedsAChildEmbeddedByValue(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(seededParent{
		Child: seededChild{Room: via.StateOf("lobby")},
	}))

	code, body := app.Get("/")
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "lobby")
}

func TestListOf_seedsEveryElementInOrder(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(seededParent{
		Child: seededChild{Lines: via.ListOf("first", "second")},
	}))

	_, body := app.Get("/")
	assert.Contains(t, body, "<li>first</li><li>second</li>")
}

func TestListOf_withNoElementsRendersAnEmptyList(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(seededParent{Child: seededChild{Lines: via.ListOf[string]()}}))

	_, body := app.Get("/")
	assert.Contains(t, body, "<ul></ul>")
}

type tagSeedState struct {
	Room via.State[string] `via:"init=\"lobby\""`
}

func (p *tagSeedState) View() h.H { return h.Div(h.P(p.Room.Display()), h.Str("get="+p.Room.Get())) }

func TestState_startsAtTheTagSeed(t *testing.T) {
	t.Parallel()
	code, body := vt.Serve(t, via.Handler(tagSeedState{})).Get("/")

	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "<p>lobby</p>")
	assert.Contains(t, body, "get=lobby")
}

type tagSeedChild struct {
	Room via.State[string] `via:"init=\"tag\""`
}

func (c *tagSeedChild) View() h.H { return h.P(c.Room.Display()) }

type tagSeedParent struct{ Child tagSeedChild }

func (p *tagSeedParent) View() h.H { return h.Div(via.Child(p.Child)) }

func TestState_literalSeedWinsOverTheTagSeed(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(tagSeedParent{
		Child: tagSeedChild{Room: via.StateOf("lobby")},
	}))

	_, body := app.Get("/")
	assert.Contains(t, body, "<p>lobby</p>")
	assert.NotContains(t, body, "tag")
}

type tagSeedInited struct {
	Room via.State[string] `via:"init=\"tag\""`
}

func (p *tagSeedInited) OnInit(ctx *via.Ctx) error { p.Room.Set("set"); return nil }
func (p *tagSeedInited) View() h.H                 { return h.P(p.Room.Display()) }

func TestState_setInOnInitOverridesTheTagSeed(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Handler(tagSeedInited{})).Get("/")

	assert.Contains(t, body, "<p>set</p>")
}

type tagSeedList struct {
	Lines via.List[string] `via:"init=[\"a\",\"b\"]"`
}

func (p *tagSeedList) View() h.H        { return h.Ul(p.Lines.Each(p.row)) }
func (p *tagSeedList) row(s string) h.H { return h.Li(h.Str(s)) }

func TestList_startsAtTheTagSeed(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Handler(tagSeedList{})).Get("/")

	assert.Contains(t, body, "<li>a</li><li>b</li>")
}

type tagSeedStateMalformed struct {
	Room via.State[string] `via:"init=lobby"`
}

func (p *tagSeedStateMalformed) View() h.H { return h.P(p.Room.Display()) }

func TestState_panicsAtMountOnAMalformedSeedTag(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, `tagSeedStateMalformed.Room has a via:"init=…" value that is not JSON for its `+
		`via.State[string] type`, func() { via.Handler(tagSeedStateMalformed{}) })
}

type trackFollow struct {
	n     *atomic.Int64
	room  *topic.Topic[int64]
	Count via.State[int64]
}

func (w *trackFollow) OnInit(ctx *via.Ctx) error { w.Count.Track(ctx, w.room, w.n.Load); return nil }
func (w *trackFollow) View() h.H                 { return h.P(h.Str("count: "), w.Count.Display()) }

func TestState_trackSeedsThePlainRender(t *testing.T) {
	t.Parallel()
	n := &atomic.Int64{}
	n.Store(7)
	app := vt.Serve(t, via.Handler(trackFollow{n: n, room: topic.New[int64]()}))

	status, body := app.Get("/")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "count: 7",
		"Track must seed the first paint from load, with no connection open")
}

func TestState_trackFollowsAPublishFromAnotherGoroutine(t *testing.T) {
	t.Parallel()
	n, room := &atomic.Int64{}, topic.New[int64]()
	app := vt.Serve(t, via.Handler(trackFollow{n: n, room: room}))
	conn := app.Connect()
	defer conn.Close()

	room.Publish(n.Add(3))
	assert.Contains(t, conn.Await("count: 3"), "count: 3",
		"a publish from outside the unit must reach the tracking connection")
}

type trackRaced struct {
	n     *atomic.Int64
	room  *topic.Topic[int64]
	Count via.State[int64]
}

func (w *trackRaced) OnInit(ctx *via.Ctx) error {
	w.Count.Track(ctx, w.room, w.n.Load)
	// Simulates a writer racing the connect: lands before the subscribe.
	w.room.Publish(w.n.Add(1))
	return nil
}
func (w *trackRaced) View() h.H { return h.P(h.Str("count: "), w.Count.Display()) }

func TestState_trackSeesAWriteThatBeatTheSubscribe(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(trackRaced{n: &atomic.Int64{}, room: topic.New[int64]()}))
	conn := app.Connect()
	defer conn.Close()

	assert.Contains(t, conn.Await("count: "), "count: 1",
		"the first frame after connect must show the write that landed before the subscribe")
}

// trackJoiner mirrors example/chat's presence: Track, then its OnConnect writes.
type trackJoiner struct {
	n     *atomic.Int64
	room  *topic.Topic[int64]
	Count via.State[int64]
}

func (w *trackJoiner) OnInit(ctx *via.Ctx) error {
	w.Count.Track(ctx, w.room, w.n.Load)
	ctx.OnConnect(w.join)
	return nil
}
func (w *trackJoiner) join()     { w.room.Publish(w.n.Add(1)) }
func (w *trackJoiner) View() h.H { return h.P(h.Str("count: "), w.Count.Display()) }

func TestState_trackSeesTheUnitsOwnOnConnectPublish(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(trackJoiner{n: &atomic.Int64{}, room: topic.New[int64]()}))
	conn := app.Connect()
	defer conn.Close()

	assert.Contains(t, conn.Await("count: 1"), "count: 1",
		"Track's own connect re-read must not shadow the unit's OnConnect publish")
}

// trackLater calls Track from a tick handler, long after OnInit returned.
type trackLater struct {
	n     *atomic.Int64
	room  *topic.Topic[int64]
	N     via.State[int64]
	Beats via.State[int]
}

func (w *trackLater) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, w.beat)
	return nil
}

func (w *trackLater) beat(ctx *via.Ctx) {
	w.Beats.Set(w.Beats.Get() + 1)
	w.N.Track(ctx, w.room, w.n.Load)
}

func (w *trackLater) View() h.H {
	return h.Div(h.Str("n="), w.N.Display(), h.Str(" beats="), w.Beats.Display())
}

func TestState_trackAfterOnInitIsIgnored(t *testing.T) {
	// Sequential: it captures the global log output.
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	n := &atomic.Int64{}
	n.Store(5)
	app := vt.Serve(t, via.Handler(trackLater{n: n, room: topic.New[int64]()}))
	conn := app.Connect()
	defer conn.Close()

	frame := conn.Await("beats=1")
	assert.Contains(t, frame, "n=0", "a late Track must not seed the State")
	assert.Contains(t, buf.String(), "Track called after OnInit returned",
		"a Track call after OnInit must log loudly instead of half-working")
}

// trackFromAction calls Track from an action handler, not OnInit.
type trackFromAction struct {
	feed  *topic.Topic[string]
	n     *atomic.Int64
	room  *topic.Topic[int64]
	N     via.State[int64]
	Bumps via.State[int]
}

func (w *trackFromAction) OnInit(ctx *via.Ctx) error {
	ctx.Listen(w.feed, w.recv)
	return nil
}

func (w *trackFromAction) recv(*via.Ctx, string) {}

func (w *trackFromAction) Bump(ctx *via.Ctx) {
	w.Bumps.Set(w.Bumps.Get() + 1)
	w.N.Track(ctx, w.room, w.n.Load)
}

func (w *trackFromAction) View() h.H {
	return h.Div(
		h.Str("n="), w.N.Display(),
		h.Str(" bumps="), w.Bumps.Display(),
		h.Button(via.On("click", w.Bump)),
	)
}

func TestState_trackFromAnActionHandlerIsIgnored(t *testing.T) {
	// Sequential: it captures the global log output.
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	n := &atomic.Int64{}
	n.Store(7)
	app := vt.Serve(t, via.Handler(trackFromAction{feed: topic.New[string](), n: n, room: topic.New[int64]()}))
	conn := app.Connect()
	defer conn.Close()

	status, _ := app.Action(0).Over(conn).Fire()
	require.Equal(t, http.StatusNoContent, status)

	frame := conn.Await("bumps=1")
	assert.Contains(t, frame, "n=0", "a Track from an action handler must not seed the State")
	assert.Contains(t, buf.String(), "Track called after OnInit returned",
		"a Track call from an action handler must log loudly instead of half-working")
}

type litTrack struct {
	Hits via.State[int64]
}

func (p *litTrack) View() h.H { return h.P(h.Str("count: "), p.Hits.Display()) }

func TestStateTrack_seedsThePlainRenderWithoutAnOnInit(t *testing.T) {
	t.Parallel()
	n := &atomic.Int64{}
	n.Store(7)
	app := vt.Serve(t, via.Handler(litTrack{Hits: via.StateTrack(topic.New[int64](), n.Load)}))

	status, body := app.Get("/")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "count: 7",
		"the literal must seed the first paint on a unit that has no OnInit at all")
}

func TestStateTrack_followsAPublish(t *testing.T) {
	t.Parallel()
	n, room := &atomic.Int64{}, topic.New[int64]()
	app := vt.Serve(t, via.Handler(litTrack{Hits: via.StateTrack(room, n.Load)}))
	conn := app.Connect()
	defer conn.Close()

	room.Publish(n.Add(3))
	assert.Contains(t, conn.Await("count: 3"), "count: 3",
		"a publish from outside the unit must reach a literal-tracked State")
}

// litTrackRacer publishes from OnInit, inside the window a connect re-read must cover.
type litTrackRacer struct {
	n    *atomic.Int64
	room *topic.Topic[int64]
	Hits via.State[int64]
}

func (p *litTrackRacer) OnInit(*via.Ctx) error { p.room.Publish(p.n.Add(1)); return nil }
func (p *litTrackRacer) View() h.H             { return h.P(h.Str("count: "), p.Hits.Display()) }

func TestStateTrack_seesAWriteThatBeatTheSubscribe(t *testing.T) {
	t.Parallel()
	n, room := &atomic.Int64{}, topic.New[int64]()
	app := vt.Serve(t, via.Handler(litTrackRacer{n: n, room: room, Hits: via.StateTrack(room, n.Load)}))
	conn := app.Connect()
	defer conn.Close()

	assert.Contains(t, conn.Await("count: "), "count: 1",
		"the first frame after connect must show the write that landed before the subscribe")
}

type litTrackReader struct {
	seen string
	Hits via.State[int64]
}

func (p *litTrackReader) OnInit(*via.Ctx) error {
	p.seen = "seen:" + strconv.FormatInt(p.Hits.Get(), 10)
	return nil
}
func (p *litTrackReader) View() h.H { return h.P(h.Str(p.seen)) }

func TestStateTrack_runsBeforeTheUnitsOwnOnInit(t *testing.T) {
	t.Parallel()
	n := &atomic.Int64{}
	n.Store(9)
	app := vt.Serve(t, via.Handler(litTrackReader{Hits: via.StateTrack(topic.New[int64](), n.Load)}))

	status, body := app.Get("/")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "seen:9",
		"the unit's own OnInit must read a State the literal already seeded")
}

type litTrackKid struct {
	Hits via.State[int64]
}

func (c litTrackKid) View() h.H { return h.P(h.Str("kid: "), c.Hits.Display()) }

type litTrackParent struct {
	Kid litTrackKid
}

func (p *litTrackParent) View() h.H { return h.Div(via.Child(p.Kid)) }

func TestStateTrack_worksOnAnEmbeddedChild(t *testing.T) {
	t.Parallel()
	n, room := &atomic.Int64{}, topic.New[int64]()
	n.Store(4)
	app := vt.Serve(t, via.Handler(litTrackParent{Kid: litTrackKid{Hits: via.StateTrack(room, n.Load)}}))

	status, body := app.Get("/")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "kid: 4", "an embedded child's literal must seed at its own init")

	conn := app.Connect()
	defer conn.Close()
	room.Publish(n.Add(1))
	assert.Contains(t, conn.Await("kid: 5"), "kid: 5",
		"an embedded child's literal must follow its topic")
}

// litTrackSilent never renders State; only the literal's Listen can make the page live.
type litTrackSilent struct {
	Hits via.State[int64]
}

func (p *litTrackSilent) View() h.H { return h.P(h.Str("silent")) }

func TestStateTrack_makesThePageLive(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(litTrackSilent{Hits: via.StateTrack(topic.New[int64](), new(atomic.Int64).Load)}))

	status, body := app.Get("/")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `@post('/_via/sse')`,
		"a literal-tracked State must earn the page a stream even when nothing renders it")
}
