//go:build browser

// Package vtbrowser_test exercises the vtbrowser harness in a real headless
// Chromium (run with -tags browser; VIA_CHROME overrides the binary path).
// Each test drives a harness method against a minimal via fixture, so the
// suite doubles as the browser tier: proving the harness works means proving
// Datastar's data-on:click / data-bind / SSE-morph behave under the strict
// CSP — the bug class no httptest can see.
package vtbrowser_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
	"github.com/go-via/via/vtbrowser"
)

// liveTicker pushes an incrementing count over SSE with no client interaction —
// the vehicle for testing that WaitFor observes a server-push morph.
type liveTicker struct{ n via.State[int] }

func (p *liveTicker) OnInit(ctx *via.Ctx) error {
	ctx.Tick(80*time.Millisecond, p.tick)
	return nil
}
func (p *liveTicker) tick(ctx *via.Ctx) { p.n.Set(p.n.Get() + 1) }
func (p *liveTicker) View() h.H         { return h.Div(h.P(h.Str("n: "), p.n.Display())) }

// clicker is a live child whose action mutates its own State — the vehicle for
// testing Click and the $viatab signal round-trip.
type clicker struct{ count via.State[int] }

func (c *clicker) Bump(ctx *via.Ctx) { c.count.Set(c.count.Get() + 1) }
func (c *clicker) View() h.H {
	return h.Div(h.P(h.Str("count: "), c.count.Display()), h.Button(on.Click(c.Bump), h.Str("+")))
}

// form is a plain page with one bound input — the vehicle for Type and Value.
type form struct{ name via.Signal[string] }

func (f *form) View() h.H { return h.Div(h.Input(f.name.Bind(), h.RawAttr("placeholder", "name"))) }

// room + chat are a Topic-backed multi-user fixture (messages + presence) — the
// vehicle for NewTab fan-out, WaitValue (composer clear), and the no-clobber
// guarantee.
type room struct {
	bus      *topic.Topic[string]
	presence *topic.Topic[int]
	online   atomic.Int64
}

func newRoom() *room  { return &room{bus: topic.New[string](), presence: topic.New[int]()} }
func (r *room) join() { r.presence.Publish(int(r.online.Add(1))) }
func (r *room) part() { r.presence.Publish(int(r.online.Add(-1))) }

type chat struct {
	room   *room
	Draft  via.Signal[string]
	Log    via.List[string]
	Online via.State[int]
}

func (c *chat) OnInit(ctx *via.Ctx) error {
	ctx.Listen(c.room.bus, c.onMsg)
	ctx.Listen(c.room.presence, c.onPres)
	// Registered, not performed: OnInit also runs on the plain GET and on every
	// action, so joining here would count one tab twice.
	ctx.OnConnect(c.room.join)
	ctx.OnDispose(c.room.part)
	return nil
}
func (c *chat) onMsg(ctx *via.Ctx, m string) { c.Log.Append(m) }
func (c *chat) onPres(ctx *via.Ctx, n int)   { c.Online.Set(n) }
func (c *chat) Send(ctx *via.Ctx) {
	if c.Draft.Get() == "" {
		return
	}
	c.room.bus.Publish(c.Draft.Get())
	c.Draft.Set("")
}
func (c *chat) line(m string) h.H { return h.Li(h.Str(m)) }
func (c *chat) View() h.H {
	return h.Div(
		h.H1(h.Str("online: "), c.Online.Display()),
		h.Ul(via.Each(c.Log.Get(), c.line)),
		h.Form(on.Submit(c.Send),
			h.Input(c.Draft.Bind(), h.RawAttr("placeholder", "msg")),
			h.Button(h.Str("send")),
		),
	)
}

// bClock + bCounter are two live children multiplexed on one stream: a ticking
// clock and a click-driven counter. bDash is the shell (not itself live).
type bClock struct{ secs via.State[int] }

func (c *bClock) OnInit(ctx *via.Ctx) error { ctx.Tick(80*time.Millisecond, c.beat); return nil }
func (c *bClock) beat(ctx *via.Ctx)         { c.secs.Set(c.secs.Get() + 1) }
func (c *bClock) View() h.H                 { return h.Div(h.P(h.Str("uptime "), c.secs.Display())) }

type bCounter struct{ n via.State[int] }

func (c *bCounter) Inc(ctx *via.Ctx) { c.n.Set(c.n.Get() + 1) }
func (c *bCounter) View() h.H {
	return h.Div(h.P(h.Str("clicks "), c.n.Display()), h.Button(on.Click(c.Inc), h.Str("+")))
}

type bDash struct {
	Clock   bClock
	Counter bCounter
}

func (d *bDash) View() h.H { return h.Div(via.Child(d.Clock), via.Child(d.Counter)) }

func TestChild_multiplexedChildsUpdateIndependently(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(bDash{}))

	// Clock child ticks on its own (no interaction) → server-push morphs #via-i0.
	s.WaitFor("#via-i0 p", func(text string) bool {
		var n int
		_, err := fmt.Sscanf(text, "uptime %d", &n)
		return err == nil && n >= 2
	}, "the clock child to tick past 2 (live push to #via-i0)")

	// Counter child: its action must route to #via-i1 via the viatab signal and morph
	// only that container, leaving the clock running.
	s.WaitLiveConnected()
	s.Click("#via-i1 button")
	s.WaitTextContains("#via-i1 p", "clicks 1")
	s.RequireCleanConsole()
}

// pRoot is a plain root (not itself live) with its own action, embedding a
// live bCounter — the region-ownership case: the root's own action patch
// must not repaint the live child from its seed value.
type pRoot struct {
	hits    int
	Counter bCounter
}

func (p *pRoot) Hit(ctx *via.Ctx) { p.hits++ }
func (p *pRoot) View() h.H {
	return h.Div(
		h.P(h.Str("hits "), h.Str(p.hits)),
		h.Button(on.Click(p.Hit), h.Str("hit")),
		via.Child(p.Counter),
	)
}

func TestChild_rootActionPatchLeavesLiveChildAlone(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(pRoot{}))

	s.WaitLiveConnected()
	s.Click("#via-i0 button")
	s.WaitTextContains("#via-i0 p", "clicks 1")

	s.Click("#root > div > button")
	s.WaitTextContains("#root > div > p", "hits 1")

	// Datastar applies one patch-elements frame as a single morph, so "hits 1"
	// being on screen means the whole root patch has already been applied — there
	// is no later moment for a stray repaint of the child to arrive from it. The
	// clicks-2 assertion below is the second line of defence: a repaint from the
	// seed would make the next click read 1, not 2.
	assert.Contains(t, s.Text("#via-i0 p"), "clicks 1",
		"the root's own action patch repainted the live child from its seed")

	s.Click("#via-i0 button")
	s.WaitTextContains("#via-i0 p", "clicks 2")
	s.RequireCleanConsole()
}

func TestOpen_servesSkeletonAndRunsDatastarCleanly(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(clicker{}))

	assert.Contains(t, s.Text("p"), "count: 0", "Open did not serve the rendered skeleton")
	var hasRoot bool
	s.Eval(`!!document.getElementById('root')`, &hasRoot)
	assert.True(t, hasRoot, "page is missing the #root morph target")
	s.RequireCleanConsole()
}

func TestWaitFor_observesServerPushMorph(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(liveTicker{}))

	s.WaitFor("p", func(text string) bool {
		var n int
		_, err := fmt.Sscanf(text, "n: %d", &n)
		return err == nil && n >= 2
	}, "the server-pushed count to reach 2 (live morph)")
	s.RequireCleanConsole()
}

func TestClick_roundTripsLiveActionThroughTabHeader(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(clicker{}))

	s.WaitTextContains("p", "count: 0")
	s.WaitLiveConnected()
	s.Click("button")
	s.WaitTextContains("p", "count: 1")
	s.RequireCleanConsole()
}

func TestTypeAndValue_driveABoundInput(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(form{}))

	s.Type("input", "alice")
	assert.Equal(t, "alice", s.Value("input"), "Type/Value round-trip failed")
	s.RequireCleanConsole()
}

func TestNewTab_fansOutAndClearsComposerAcrossTabs(t *testing.T) {
	r := newRoom()
	a := vtbrowser.Open(t, via.Handler(chat{room: r}))
	b := a.NewTab()

	a.WaitTextContains("h1", "online: 2") // both streams connected + presence settled

	a.Type("input", "hello")
	a.WaitBoundSignal("input", "hello")
	a.Click("button")

	b.WaitTextContains("ul", "hello") // fan-out: B received A's message
	a.WaitValue("input", "")          // sender's composer cleared (signal-patch)

	a.RequireCleanConsole()
	b.RequireCleanConsole()
}

// reconnecting is true while the manager shows its drop banner.
const reconnecting = `document.documentElement.getAttribute('data-via-connection')==='connecting' && ` +
	`(document.getElementById('via-reconnect-banner')||{textContent:''}).textContent.includes('Reconnecting')`

func TestReconnect_bannerSurfacesOnDropAndClearsOnResume(t *testing.T) {
	g := &streamGate{app: via.Handler(liveTicker{})}
	s := vtbrowser.Open(t, g)

	var booted bool
	s.Eval(`window.__viaRC===1 && document.documentElement.getAttribute('data-via-connection')==='online'`, &booted)
	require.True(t, booted, "reconnect manager did not boot online — the nonce'd IIFE was dropped by the CSP or failed to run")
	s.WaitLiveConnected()

	g.dropStreams(3 * time.Second)
	s.WaitEvalTrue(reconnecting, "a dropped stream flips the status to connecting and shows the banner")

	s.WaitEvalTrue(`document.documentElement.getAttribute('data-via-connection')==='online' && `+
		`document.getElementById('via-reconnect-banner')===null`,
		"the resumed stream's first patch clears the banner and restores online")
	assert.Equal(t, int32(1), g.pages.Load(), "a network drop resumes in place, without a reload")
	s.RequireCleanConsole()
}

func TestReconnect_bannerIsRestyledByAnAppRule(t *testing.T) {
	app := via.Handler(liveTicker{}, via.WithHead(via.Head{
		Assets: via.Assets{Styles: []via.Style{{Inline: "#via-reconnect-banner{background:rgb(1, 2, 3)}"}}},
	}))
	g := &streamGate{app: app}
	s := vtbrowser.Open(t, g)
	s.WaitLiveConnected()

	// Held well past the checks below, so the banner is still up when read.
	g.dropStreams(time.Minute)
	s.WaitEvalTrue(reconnecting, "a dropped stream flips the status to connecting and shows the banner")

	var bg string
	s.Eval(`getComputedStyle(document.getElementById('via-reconnect-banner')).backgroundColor`, &bg)
	assert.Equal(t, "rgb(1, 2, 3)", bg, "a plain app rule must beat via's zero-specificity banner styling")
	s.RequireCleanConsole()
}

func TestReconnect_bannerColorFollowsConnectionState(t *testing.T) {
	g := &streamGate{app: via.Handler(clicker{})}
	s := vtbrowser.Open(t, g)
	s.WaitLiveConnected()
	bg := func() string {
		var got string
		s.Eval(`getComputedStyle(document.getElementById('via-reconnect-banner')).backgroundColor`, &got)
		return got
	}

	g.dropStreams(2 * time.Second)
	s.WaitEvalTrue(reconnecting, "a dropped stream flips the status to connecting and shows the banner")
	assert.Equal(t, "rgb(245, 158, 11)", bg(), "reconnecting banner is not the amber state colour")

	// The retry is refused, which the manager treats as final: no probe, no reload.
	g.deny.Store(true)
	s.WaitEvalTrue(`document.documentElement.getAttribute('data-via-connection')==='offline'`,
		"a refused reconnect marks the connection offline")
	assert.Equal(t, "rgb(220, 38, 38)", bg(), "disconnected banner is not the red state colour")
	s.RequireCleanConsole()
}

func TestReconnect_giveUpGoesOfflineAndCapsTheReloadLoop(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(clicker{}))
	// The connect's first frame proves the stream alive and would clear the
	// give-up banner below before the click could reach it.
	s.WaitLiveConnected()

	// Pre-armed at the reload cap so the terminal branch runs without a real reload.
	var armed bool
	s.Eval(`sessionStorage.setItem('__via_rc_reloads','3'); true`, &armed)

	var status string
	s.Eval(`document.dispatchEvent(new CustomEvent('datastar-fetch',{detail:{type:'retries-failed',el:document.body}}));`+
		`document.documentElement.getAttribute('data-via-connection')`, &status)
	assert.Equal(t, "offline", status, "a give-up did not flip the status to offline")
	assert.Contains(t, s.Text("#via-reconnect-banner"), "Disconnected",
		"at the reload cap the manager must report the connection as disconnected")
	var label string
	s.Eval(`(document.querySelector('#via-reconnect-banner button')||{}).textContent||''`, &label)
	assert.Equal(t, "Reconnect", label, "give-up banner has no Reconnect button")
	s.RequireCleanConsole()

	// The server is up, so the retry's first probe reloads; the pre-armed cap
	// would have blocked an automatic one.
	s.Click("#via-reconnect-banner button")
	s.WaitEvalTrue(`document.documentElement.getAttribute('data-via-connection')==='online' && `+
		`document.getElementById('via-reconnect-banner')===null`,
		"clicking Reconnect must bring the page back online with no banner left")
}

func TestNewTab_fanOutDoesNotClobberInProgressTyping(t *testing.T) {
	r := newRoom()
	a := vtbrowser.Open(t, via.Handler(chat{room: r}))
	b := a.NewTab()

	a.WaitTextContains("h1", "online: 2")

	a.Type("input", "half-typed") // A is composing; has not sent
	a.WaitBoundSignal("input", "half-typed")

	b.Type("input", "from-b")
	b.WaitBoundSignal("input", "from-b")
	b.Click("button") // B sends; fans out and pushes to A

	a.WaitTextContains("ul", "from-b") // A received B's fan-out
	assert.Equal(t, "half-typed", a.Value("input"), "fan-out clobbered A's in-progress draft")

	a.RequireCleanConsole()
	b.RequireCleanConsole()
}

// redirectViaScript is a plain page whose @post action calls via.Redirect,
// answered as the hash-admitted one-line navigation script (see redirectInit).
type redirectViaScript struct{}

func (p *redirectViaScript) Go(ctx *via.Ctx) { ctx.Redirect("/done") }
func (p *redirectViaScript) View() h.H {
	return h.Div(h.Button(h.RawAttr("id", "go"), on.Click(p.Go), h.Str("go")))
}

func TestPostActionRedirect_navigatesViaScript(t *testing.T) {
	app := via.Handler(redirectViaScript{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	s := vtbrowser.Open(t, app)
	s.Click("#go")

	s.WaitEvalTrue(`location.pathname==='/done'`,
		"the @post Redirect's navigation script to run under the strict CSP")

	// redirectInit removes its own <script> before navigating, so a live document
	// never carries it — and the new document was never sent one.
	var inserted bool
	s.Eval(`[...document.querySelectorAll('script')].some(x => x.textContent.includes('location.assign'))`, &inserted)
	assert.False(t, inserted, "the redirect script must remove itself from the document")
}

// liveFormBrowser is a live root with a native PostForm — the vehicle for
// proving, in a real browser, that the hidden $viatab field is actually
// filled (see the L1 fix: `data-attr:value`, not `data-attr-value`, which
// Datastar's `t.split(/:(.+)/)` key parser silently drops).
type liveFormBrowser struct {
	calls *int
	n     via.State[int]
}

func (f *liveFormBrowser) Save(ctx *via.Ctx) { *f.calls++ }
func (f *liveFormBrowser) View() h.H {
	return h.Div(
		f.n.Display(),
		via.PostForm(f.Save,
			h.Input(h.Name("name"), h.RawAttr("id", "name")),
			h.Button(h.RawAttr("id", "save"), h.Str("save")),
		),
		h.Span(h.RawAttr("id", "calls"), h.Str(strconv.Itoa(*f.calls))),
	)
}

func TestPostForm_nativeSubmitFromLiveUnitReturns200(t *testing.T) {
	calls := 0
	s := vtbrowser.Open(t, via.Handler(liveFormBrowser{calls: &calls}))

	s.WaitLiveConnected()
	s.Type("#name", "alice")
	s.Click("#save")

	// The submit's response document is the first render with calls == 1, so this
	// is also the gate that the native navigation finished.
	s.WaitTextContains("#calls", "1")

	var status float64
	s.Eval(`performance.getEntriesByType('navigation')[0].responseStatus`, &status)
	require.Equal(t, float64(http.StatusOK), status, "native PostForm submit from a live unit did not return 200")
	assert.NotContains(t, s.Text("body"), "this tab has no stream", "submit fell through to the plain 410 fallback")
	s.RequireCleanConsole()
}

// styledPage is the vehicle for the head tests: one element whose colour comes
// only from a stylesheet, so "did the sheet load" is directly observable.
type styledPage struct{}

func (styledPage) View() h.H { return h.Div(h.RawAttr("id", "styled"), h.Str("styled")) }

// cdn serves one off-origin stylesheet. A second httptest server is a genuinely
// different origin (different port), so this exercises the real cross-origin
// path without reaching the network.
func cdn(t testing.TB, css string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Write([]byte(css))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

const styledIsRed = `getComputedStyle(document.querySelector("#styled")).color === "rgb(255, 0, 0)"`

func TestDocumentHead_declaredOffOriginStylesheetLoads(t *testing.T) {
	origin := cdn(t, "#styled{color:red}")
	app := via.Handler(styledPage{}, via.WithHead(via.Head{
		Assets: via.Assets{Styles: []via.Style{{Href: origin + "/app.css"}}},
	}))
	s := vtbrowser.Open(t, app)
	s.WaitEvalTrue(styledIsRed, "the declared off-origin stylesheet loaded and applied under the derived CSP")
	s.RequireCleanConsole() // a CSP-blocked sheet surfaces as a console error
}

func TestDocumentHead_undeclaredOriginStaysBlocked(t *testing.T) {
	origin := cdn(t, "#styled{color:red}")
	app := via.Handler(styledPage{}, via.WithHead(via.Head{
		Assets: via.Assets{Styles: []via.Style{{Inline: `@import url("` + origin + `/app.css");`}}},
	}))
	s := vtbrowser.Open(t, app)
	// An @import is a render-blocking subresource: had style-src admitted it, it
	// would have been fetched and applied before the load event fired.
	s.WaitLoaded()

	var red bool
	s.Eval(styledIsRed, &red)
	assert.False(t, red, "an origin the Head never declared must not be admitted by style-src")
	// No console assertion: a refused @import is reported as a browser issue,
	// not through the console API, so it never reaches ConsoleErrors. The style
	// not applying is the whole of the evidence — and RequireCleanConsole would
	// be wrong here either way, since a block is the expected outcome.
}

func TestReconnect_cleanStreamCloseIsReportedToTheUser(t *testing.T) {
	r := via.NewRouter()
	via.Mount(r, "/", liveTicker{})
	s := vtbrowser.Open(t, r)
	s.WaitLiveConnected()
	s.WaitTextContains("p", "n: 1")

	r.Close()

	s.WaitEvalTrue(`document.documentElement.getAttribute('data-via-connection')==='offline'`,
		"a clean stream close must mark the connection offline")
	s.WaitEvalTrue(`(function(){var b=document.getElementById('via-reconnect-banner');`+
		`return !!b&&b.isConnected&&b.textContent.length>0})()`,
		"a clean stream close must put a visible banner on screen")
}

// streamGate stands between the browser and the app's stream route. It stubs
// the network, not via: every request still reaches the real handler, so the
// statuses the browser sees are the ones via answers.
type streamGate struct {
	app     http.Handler
	delay   time.Duration // how long a connect is held before it reaches the app
	deny    atomic.Bool   // answer 403 to every connect
	hold    atomic.Int64  // replaces delay for connects after dropStreams, in nanoseconds
	pages   atomic.Int32  // document loads, so a reload is countable
	streams atomic.Int32  // connects that reached the app

	mu   sync.Mutex
	open []*gatedStream
}

type gatedStream struct {
	end  context.CancelFunc
	done chan struct{}
	cut  atomic.Bool // abort the browser's side too, as a network drop does
}

func (g *streamGate) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Header.Get("Sec-Fetch-Dest") == "document" {
		g.pages.Add(1)
	}
	if !strings.HasSuffix(req.URL.Path, "/_via/sse") {
		g.app.ServeHTTP(w, req)
		return
	}
	if g.deny.Load() {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	delay := g.delay
	if h := g.hold.Load(); h > 0 {
		delay = time.Duration(h)
	}
	select {
	case <-time.After(delay):
	case <-req.Context().Done():
		return
	}
	ctx, end := context.WithCancel(req.Context())
	gs := &gatedStream{end: end, done: make(chan struct{})}
	g.mu.Lock()
	g.open = append(g.open, gs)
	g.mu.Unlock()
	g.streams.Add(1)
	g.app.ServeHTTP(w, req.WithContext(ctx))
	close(gs.done)
	if gs.cut.Load() {
		// net/http closes the connection mid-body, so the browser's fetch
		// fails with a network error rather than seeing the response end.
		panic(http.ErrAbortHandler)
	}
	// The browser's side stays open after via's returns, so the page never
	// sees its stream finish: the tab is stale and nothing on it knows.
	<-req.Context().Done()
}

// endStreams ends via's side of every open stream and returns once via has
// dropped them, so the next action finds no stream for its tab.
func (g *streamGate) endStreams() { g.closeAll(false) }

// dropStreams cuts every open stream on both sides, as a network drop does,
// and holds each later connect for hold so the gap can be observed.
func (g *streamGate) dropStreams(hold time.Duration) {
	g.hold.Store(int64(hold))
	g.closeAll(true)
}

func (g *streamGate) closeAll(cut bool) {
	g.mu.Lock()
	open := g.open
	g.open = nil
	g.mu.Unlock()
	for _, s := range open {
		s.cut.Store(cut)
		s.end()
		<-s.done
	}
}

// connectionWatch records every state the reconnect manager publishes and
// every banner it inserts, so a flash inside one task still counts.
const connectionWatch = `(()=>{window.__states=[];window.__banner=0;` +
	`new MutationObserver(function(ms){for(const m of ms){` +
	`if(m.type==='attributes')__states.push(document.documentElement.getAttribute('data-via-connection'));` +
	`for(const n of m.addedNodes||[])if(n.id==='via-reconnect-banner')__banner++}})` +
	`.observe(document.documentElement,{attributes:true,attributeFilter:['data-via-connection'],childList:true,subtree:true});` +
	`return true})()`

func TestReconnect_clickOnAStaleTabReloadsOnce(t *testing.T) {
	// Short: a click on a gone tab waits this long for its stream to come back
	// before it answers 410.
	g := &streamGate{app: via.Handler(clicker{}, via.WithPinnedDeadline(time.Second))}
	s := vtbrowser.Open(t, g)
	s.WaitLiveConnected()
	var ok bool
	s.Eval(`(()=>{window.__stale=1;return true})()`, &ok)

	g.endStreams()
	s.Click("button")

	s.WaitEvalTrue(`window.__stale===undefined`, "a 410 on a real click must reload the stale tab")
	s.WaitLiveConnected()
	s.Sleep(3 * time.Second) // past the manager's probe backoff, so a second reload would have landed
	assert.Equal(t, int32(2), g.pages.Load(), "the stale tab must reload exactly once")

	s.Click("button")
	s.WaitTextContains("p", "count: 1")
	s.RequireCleanConsole()
}

func TestClick_beforeTheStreamConnectsIsApplied(t *testing.T) {
	g := &streamGate{app: via.Handler(clicker{}), delay: 1500 * time.Millisecond}
	s := vtbrowser.Open(t, g)
	// A data-json-signals element renders only once Datastar has bound the page.
	s.WaitEvalTrue(`(()=>{let p=document.getElementById('__bound');if(!p){p=document.createElement('pre');`+
		`p.id='__bound';p.setAttribute('data-json-signals','');document.body.appendChild(p)}`+
		`return p.textContent.includes('viatab')})()`, "Datastar to bind the page")
	require.Zero(t, g.streams.Load(), "precondition: the click must land before the stream connects")

	s.Click("button")
	s.WaitTextContains("p", "count: 1")
	assert.Equal(t, int32(1), g.pages.Load(), "the early click must be applied, not answered with a reload")
	s.RequireCleanConsole()
}

// evalProbe is a page script that compiles code from a string through eval
// and through the Function constructor, the calls a library that needs
// 'unsafe-eval' makes, next to a Datastar expression.
type evalProbe struct{}

func (evalProbe) PageMeta() via.Meta {
	return via.Meta{Assets: via.Assets{Scripts: []via.Script{{
		Inline: `try{eval('1');window.__eval='ran'}catch(_){window.__eval='blocked'}` +
			`try{Function('window.__fn="ran"')()}catch(_){window.__fn='blocked'}`,
	}}}}
}

func (evalProbe) View() h.H {
	return h.Div(h.Span(h.ID("out"), h.DataText(expr.Rawf(`'datastar ' + 'ran'`))))
}

func TestCSP_evalIsBlockedWhileDatastarExpressionsRun(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(evalProbe{}))
	s.WaitTextContains("#out", "datastar ran")
	var got string
	s.Eval(`window.__eval||''`, &got)
	assert.Equal(t, "blocked", got, "the policy must forbid eval to every script but Datastar's nonce'd compiles")
	s.RequireCleanConsole()
}

func TestWithUnsafeEval_letsAPageScriptCompileFromAString(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts []via.Option
		want string
	}{
		{"default", nil, "blocked"},
		{"WithUnsafeEval", []via.Option{via.WithUnsafeEval()}, "ran"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := vtbrowser.Open(t, via.Handler(evalProbe{}, tt.opts...))
			s.WaitTextContains("#out", "datastar ran")
			var got string
			s.Eval(`window.__fn||''`, &got)
			assert.Equal(t, tt.want, got)
			s.RequireCleanConsole()
		})
	}
}

// grower pushes a button per click. The first render has none, so the
// first push brings an expression Datastar has never compiled.
type grower struct {
	n    via.State[int]
	last via.State[int]
}

func (g *grower) Grow(ctx *via.Ctx)        { g.n.Set(g.n.Get() + 1) }
func (g *grower) Pick(ctx *via.Ctx, i int) { g.last.Set(i) }
func (g *grower) item(i int) h.H {
	return h.Button(h.ID("pick"+strconv.Itoa(i)), on.Click(on.WithArg(g.Pick, i)))
}
func (g *grower) upTo() []int {
	out := make([]int, g.n.Get())
	for i := range out {
		out[i] = i + 1
	}
	return out
}
func (g *grower) View() h.H {
	return h.Div(
		h.P(h.Str("last "), g.last.Display()),
		h.Button(h.ID("grow"), on.Click(g.Grow), h.Str("grow")),
		via.Each(g.upTo(), g.item),
	)
}

func TestCSP_expressionArrivingInAStreamPatchCompilesUnderTheDocumentNonce(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(grower{}))
	s.WaitLiveConnected()
	s.Click("#grow")
	s.Click("#grow")
	s.WaitEvalTrue(`!!document.getElementById('pick2')`, "the stream to push the second pick button")
	s.Click("#pick2")
	s.WaitTextContains("p", "last 2")
	s.RequireCleanConsole()
}

// picker binds one handler per row, and two events and a debounced click
// on single elements, each with its own arg.
type picker struct {
	last via.State[int]
	hits via.State[int]
}

func (p *picker) Pick(ctx *via.Ctx, i int) {
	p.last.Set(i)
	p.hits.Set(p.hits.Get() + 1)
}

func (p *picker) row(i int) h.H {
	return h.Button(h.ID("pick"+strconv.Itoa(i)), on.Click(on.WithArg(p.Pick, i)))
}

func (p *picker) View() h.H {
	return h.Div(
		h.P(h.ID("last"), h.Str("last "), p.last.Display()),
		h.P(h.ID("hits"), h.Str("hits "), p.hits.Display()),
		via.Each([]int{1, 2, 3}, p.row),
		h.Input(h.ID("both"), on.Click(on.WithArg(p.Pick, 10)), on.Change(on.WithArg(p.Pick, 20))),
		h.Button(h.ID("slow"), on.Click(on.WithArg(p.Pick, 30), on.Debounce(200*time.Millisecond))),
	)
}

func TestActionArg_eachElementDispatchesItsOwnArgFromOneSharedExpression(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(picker{}))
	var same bool
	s.Eval(`(()=>{const a=[1,2,3].map(i=>document.getElementById('pick'+i).getAttribute('data-on:click'));`+
		`return a[0]===a[1]&&a[1]===a[2]})()`, &same)
	require.True(t, same, "the rows must share one expression")

	s.Click("#pick2")
	s.WaitTextContains("#last", "last 2")
	s.Click("#pick3")
	s.WaitTextContains("#last", "last 3")
	s.Click("#pick1")
	s.WaitTextContains("#last", "last 1")

	s.Click("#both")
	s.WaitTextContains("#last", "last 10")
	var ok bool
	s.Eval(`document.getElementById('both').dispatchEvent(new Event('change')),true`, &ok)
	s.WaitTextContains("#last", "last 20")
	s.WaitTextContains("#hits", "hits 5")

	for range 3 {
		s.Click("#slow")
	}
	s.WaitTextContains("#last", "last 30")
	s.Sleep(400 * time.Millisecond)
	assert.Equal(t, "hits 6", s.Text("#hits"), "the debounce still folds three clicks into one POST")
	s.RequireCleanConsole()
}

type crasher struct{ count via.State[int] }

func (c *crasher) Bump(ctx *via.Ctx)  { c.count.Set(c.count.Get() + 1) }
func (c *crasher) Crash(ctx *via.Ctx) { panic("crasher: boom") }
func (c *crasher) View() h.H {
	return h.Div(
		h.P(h.Str("count: "), c.count.Display()),
		h.Button(h.ID("bump"), on.Click(c.Bump), h.Str("+")),
		h.Button(h.ID("crash"), on.Click(c.Crash), h.Str("crash")),
	)
}

func TestReconnect_failedActionLeavesTheConnectionOnline(t *testing.T) {
	g := &streamGate{app: via.Handler(crasher{})}
	s := vtbrowser.Open(t, g)
	s.WaitLiveConnected()
	var ok bool
	s.Eval(connectionWatch, &ok)
	s.Eval(`(()=>{document.addEventListener('datastar-fetch',function(e){`+
		`if(e.detail.type==='finished'&&e.detail.el.id==='crash')window.__crashed=1});return true})()`, &ok)

	s.Click("#crash")
	s.WaitEvalTrue(`window.__crashed===1`, "the panicking action's request to finish")
	s.Sleep(time.Second) // past the probe's first delay, so a reload would have begun

	var got struct {
		States []string
		Banner int
	}
	s.Eval(`({states:window.__states,banner:window.__banner})`, &got)
	assert.Empty(t, got.States, "a 500 from an action must not change the connection state")
	assert.Zero(t, got.Banner, "a 500 from an action must not put the banner up, even for a moment")
	assert.Equal(t, int32(1), g.pages.Load(), "a 500 from an action must not reload the page")

	s.Click("#bump")
	s.WaitTextContains("p", "count: 1")
	s.RequireCleanConsole()
}

func TestReconnect_forbiddenStreamStaysDisconnectedWithoutReloading(t *testing.T) {
	g := &streamGate{app: via.Handler(clicker{})}
	g.deny.Store(true)
	s := vtbrowser.Open(t, g)

	s.WaitEvalTrue(`document.documentElement.getAttribute('data-via-connection')==='offline'`,
		"a 403 on the stream must mark the connection offline")
	assert.Contains(t, s.Text("#via-reconnect-banner"), "Disconnected")
	s.Sleep(2500 * time.Millisecond) // past the manager's first probe and its jitter
	assert.Equal(t, int32(1), g.pages.Load(),
		"a 403 on the stream must not reload: the server would answer the same way again")
}

func TestReconnect_recoversAcrossADeployGap(t *testing.T) {
	r := via.NewRouter()
	via.Mount(r, "/", liveTicker{})
	s := vtbrowser.Open(t, r)
	s.WaitLiveConnected()
	s.WaitTextContains("p", "n: 1")

	r.Close() // the graceful shutdown: every stream ends with a clean close

	next := via.NewRouter()
	via.Mount(next, "/", liveTicker{})
	s.Restart(3*time.Second, next) // longer than the manager's old blind-reload window

	s.WaitEvalTrue(`document.documentElement.getAttribute('data-via-connection')==='online'`,
		"the tab must re-bootstrap onto the new server instead of reloading once into a dead one")
	s.WaitTextContains("p", "n: 1")
}

// jsCDN serves one off-origin script that stamps a global, so "did it execute"
// is directly observable.
func jsCDN(t testing.TB, js string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write([]byte(js))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// scriptPage declares its assets from a field fixed at Mount, which is what
// keeps PageMeta().Assets constant while still pointing at a test server's
// ephemeral port.
type scriptPage struct{ Src string }

func (p *scriptPage) PageMeta() via.Meta {
	return via.Meta{
		Title: "scripted",
		Assets: via.Assets{
			Scripts: []via.Script{
				{Src: p.Src + "/a.js", Defer: true},
				{Inline: `window.__inline = true`},
			},
			Styles: []via.Style{{Inline: "#styled{color:red}"}},
		},
	}
}

func (p *scriptPage) View() h.H { return h.Div(h.RawAttr("id", "styled"), h.Str("styled")) }

func TestPageMeta_declaredScriptsExecuteUnderThePerMountCSP(t *testing.T) {
	origin := jsCDN(t, `window.__external = true`)
	r := via.NewRouter()
	via.Mount(r, "/", scriptPage{Src: origin})
	s := vtbrowser.Open(t, r)

	s.WaitEvalTrue(`window.__external === true`, "the declared off-origin script executed")
	s.WaitEvalTrue(`window.__inline === true`, "the declared inline script was admitted by its hash")
	s.WaitEvalTrue(styledIsRed, "the declared inline style was admitted by its hash")
	s.RequireCleanConsole() // a CSP-blocked script surfaces as a console error
}

// bSeedChild's Signal is never Bind()ed or Display()ed: the seed declaration
// is all that puts its slot on the client, and a data-effect is its only reader.
type bSeedChild struct {
	Load via.Signal[[]int] `via:"init=[]"`
}

func (c *bSeedChild) Fill(ctx *via.Ctx) { c.Load.Set([]int{1, 2, 3}) }
func (c *bSeedChild) View() h.H {
	return h.Div(
		h.Span(h.ID("n"), h.Str("-")),
		h.Div(h.DataEffect(expr.Rawf(`document.getElementById('n').textContent = String(%s.length)`,
			c.Load.Ref()))),
		h.Button(h.ID("fill"), on.Click(c.Fill), h.Str("fill")),
	)
}

type bSeedRoot struct{ Uptime bSeedChild }

func (r *bSeedRoot) View() h.H { return h.Div(via.Child(r.Uptime)) }

func TestChild_seededChildSignalReachesTheIslandWithoutRootPhantom(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(bSeedRoot{}))

	s.WaitTextContains("#n", "0")

	var root, child string
	s.Eval(`document.getElementById('root').getAttribute('data-signals')||""`, &root)
	s.Eval(`document.getElementById('via-i0').getAttribute('data-signals')||""`, &child)
	assert.NotContains(t, root, "uptime_load", "the root minted a slot for its child's signal")
	assert.Contains(t, child, "uptime__load", "the child unit did not declare its own signal")

	s.Click("#fill")
	s.WaitTextContains("#n", "3")
	s.RequireCleanConsole()
}

// bToggleRoot gates a block on a client-only signal while a live child ticks
// below it: no server round-trip may reset the seeded toggle.
type bToggleRoot struct {
	Details via.SignalCS[bool] `via:"init=true"`
	Clock   bClock
}

func (r *bToggleRoot) View() h.H {
	return h.Div(
		h.Button(h.ID("toggle"), h.DataOn("click", r.Details.Ref().Toggle()), h.Str("toggle")),
		h.Div(h.ID("panel"), h.DataShow(r.Details.Ref()), h.Str("panel")),
		via.Child(r.Clock),
	)
}

const panelHidden = `(document.getElementById('panel')||{style:{}}).style.display==='none'`

func TestChild_clientOnlyToggleSeededOpenSurvivesChildPushes(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(bToggleRoot{}))

	s.WaitEvalTrue(`!(`+panelHidden+`)`, "the seeded client-only toggle to show the panel at first paint")

	s.Click("#toggle")
	s.WaitEvalTrue(panelHidden, "the click to hide the panel")

	s.WaitFor("#via-i0 p", func(text string) bool {
		var n int
		_, err := fmt.Sscanf(text, "uptime %d", &n)
		return err == nil && n >= 3
	}, "the live child to push several patches after the toggle")

	var hidden bool
	s.Eval(panelHidden, &hidden)
	assert.True(t, hidden, "a child push re-declared the root's client-only signal from its seed")
	s.RequireCleanConsole()
}

// bTickChild is a live child whose Signal is read only through Ref.
type bTickChild struct {
	N via.Signal[int] `via:"init=0"`
}

func (c *bTickChild) OnInit(ctx *via.Ctx) error { ctx.Tick(80*time.Millisecond, c.tick); return nil }
func (c *bTickChild) tick(ctx *via.Ctx)         { c.N.Set(c.N.Get() + 1) }
func (c *bTickChild) View() h.H {
	return h.Div(h.Span(h.ID("n2"), h.DataText(c.N.Ref())))
}

type bTickRoot struct {
	hits  int
	Ticks bTickChild
}

func (r *bTickRoot) Hit(ctx *via.Ctx) { r.hits++ }
func (r *bTickRoot) View() h.H {
	return h.Div(
		h.P(h.ID("hits"), h.Str("hits "), h.Str(r.hits)),
		h.Button(h.ID("hit"), on.Click(r.Hit), h.Str("hit")),
		via.Child(r.Ticks),
	)
}

func TestChild_childSetAfterRootRenderBindsToTheChildUnit(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(bTickRoot{}))

	s.WaitLiveConnected()
	s.WaitFor("#n2", func(text string) bool { n, err := strconv.Atoi(text); return err == nil && n >= 1 }, "the child to tick once")

	s.Click("#hit")
	s.WaitTextContains("#hits", "hits 1")

	after, err := strconv.Atoi(s.Text("#n2"))
	require.NoError(t, err, "the child's signal is unreadable after the root render: %q", s.Text("#n2"))
	s.WaitFor("#n2", func(text string) bool { n, err := strconv.Atoi(text); return err == nil && n > after },
		"the child's Set to keep arriving on the child's own slot after a root render")
	s.RequireCleanConsole()
}

// tracked mirrors internal/example/shared: an atomic kept in State by a StateTrack literal.
type tracked struct {
	n    *atomic.Int64
	room *topic.Topic[int64]
	Hits via.State[int64]
}

func (c *tracked) Inc(ctx *via.Ctx) { c.room.Publish(c.n.Add(1)) }
func (c *tracked) View() h.H {
	return h.Div(h.P(h.Str("hits: "), c.Hits.Display()), h.Button(on.Click(c.Inc), h.Str("+")))
}

func TestTrack_keepsEveryTabOnTheSharedValue(t *testing.T) {
	n, room := new(atomic.Int64), topic.New[int64]()
	a := vtbrowser.Open(t, via.Handler(tracked{n: n, room: room, Hits: via.StateTrack(room, n.Load)}))
	b := a.NewTab()
	a.WaitLiveConnected()
	b.WaitLiveConnected()

	a.Click("button")
	b.WaitTextContains("p", "hits: 1")
	a.WaitTextContains("p", "hits: 1")

	b.Click("button")
	a.WaitTextContains("p", "hits: 2")

	c := a.NewTab() // a late tab is seeded from the store, not from a publish
	c.WaitTextContains("p", "hits: 2")
	c.WaitLiveConnected()
	a.Click("button")
	c.WaitTextContains("p", "hits: 3")
	b.WaitTextContains("p", "hits: 3")

	a.RequireCleanConsole()
	b.RequireCleanConsole()
	c.RequireCleanConsole()
}

const (
	atLiteral  = "send @post('/x') now"
	getLiteral = "then @get('/y')"
)

// Datastar rewrites @name( into an action call even inside a string literal;
// Val must hand it the string so the handler still parses and yields the text.
type bAtLiteral struct{}

func (bAtLiteral) View() h.H {
	return h.Div(
		h.Span(h.ID("out"), h.Str("-")),
		h.Button(h.ID("send"), on.ClickCS(expr.Rawf(`document.getElementById('out').textContent = %s + "|" + %s`,
			expr.Val(atLiteral), expr.Val(getLiteral))), h.Str("send")),
	)
}

func TestVal_atSignInStringSurvivesDatastarRewrite(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(bAtLiteral{}))
	s.WaitLoaded()

	s.Click("#send")
	s.WaitTextContains("#out", "|")

	assert.Equal(t, atLiteral+"|"+getLiteral, s.Text("#out"))
	s.RequireCleanConsole()
}

// The #out spans carry no server-rendered text, so what they show is the
// client store's value and nothing else.
type bAtSeed struct {
	Msg via.SignalCS[string] `via:"init=\"send @post('/x') now\""`
}

func (p *bAtSeed) View() h.H { return h.Div(h.Span(h.ID("out"), h.DataText(p.Msg.Ref()))) }

func TestSignal_atSignInSeededValueReachesTheStoreIntact(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(bAtSeed{}))
	s.WaitLoaded()

	s.WaitTextContains("#out", "now")
	assert.Equal(t, atLiteral, s.Text("#out"))
	s.RequireCleanConsole()
}

type bAtPlain struct{ Msg via.Signal[string] }

func (p *bAtPlain) Send(ctx *via.Ctx) { p.Msg.Set(atLiteral) }
func (p *bAtPlain) View() h.H {
	return h.Div(h.Span(h.ID("out"), h.DataText(p.Msg.Ref())), h.Button(h.ID("send"), on.Click(p.Send), h.Str("send")))
}

func TestSignal_atSignInPlainActionPatchReachesTheStoreIntact(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(bAtPlain{}))
	s.WaitLoaded()

	s.Click("#send")
	s.WaitTextContains("#out", "now")
	assert.Equal(t, atLiteral, s.Text("#out"))
	s.RequireCleanConsole()
}

type bAtLive struct{ Msg via.Signal[string] }

func (p *bAtLive) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Hour, func(*via.Ctx) {})
	return nil
}
func (p *bAtLive) Send(ctx *via.Ctx) { p.Msg.Set(atLiteral) }
func (p *bAtLive) View() h.H {
	return h.Div(h.Span(h.ID("out"), h.DataText(p.Msg.Ref())), h.Button(h.ID("send"), on.Click(p.Send), h.Str("send")))
}

func TestSignal_atSignInLivePatchSignalsReachesTheStoreIntact(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(bAtLive{}))
	s.WaitLiveConnected()

	s.Click("#send")
	s.WaitTextContains("#out", "now")
	assert.Equal(t, atLiteral, s.Text("#out"))
	s.RequireCleanConsole()
}
