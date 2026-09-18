//go:build browser

// Package vtbrowser_test exercises the vtbrowser harness in a real headless
// Chromium (run with -tags browser; VIA_CHROME overrides the binary path).
// Each test drives a harness method against a minimal via fixture, so the
// suite doubles as the browser tier: proving the harness works means proving
// Datastar's data-on:click / data-bind / SSE-morph behave under the strict
// CSP — the bug class no httptest can see.
package vtbrowser_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
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
	return h.Div(h.P(h.Str("count: "), c.count.Display()), h.Button(via.On("click", c.Bump), h.Str("+")))
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
		h.Form(via.On("submit", c.Send),
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
	return h.Div(h.P(h.Str("clicks "), c.n.Display()), h.Button(via.On("click", c.Inc), h.Str("+")))
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
		h.Button(via.On("click", p.Hit), h.Str("hit")),
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
	if got := s.Text("#via-i0 p"); !strings.Contains(got, "clicks 1") {
		t.Fatalf("the root's own action patch repainted the live child from its seed: %q", got)
	}

	s.Click("#via-i0 button")
	s.WaitTextContains("#via-i0 p", "clicks 2")
	s.RequireCleanConsole()
}

func TestOpen_servesSkeletonAndRunsDatastarCleanly(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(clicker{}))

	if got := s.Text("p"); !strings.Contains(got, "count: 0") {
		t.Fatalf("Open did not serve the rendered skeleton: %q", got)
	}
	var hasRoot bool
	s.Eval(`!!document.getElementById('root')`, &hasRoot)
	if !hasRoot {
		t.Fatal("page is missing the #root morph target")
	}
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
	if got := s.Value("input"); got != "alice" {
		t.Fatalf("Type/Value round-trip failed: got %q, want %q", got, "alice")
	}
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

func TestReconnect_bannerSurfacesOnDropAndClearsOnResume(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(liveTicker{}))

	var booted bool
	s.Eval(`window.__viaRC===1 && document.documentElement.getAttribute('data-via-connection')==='online'`, &booted)
	if !booted {
		t.Fatal("reconnect manager did not boot online — the nonce'd IIFE was dropped by the CSP or failed to run")
	}

	var status string
	s.Eval(`document.dispatchEvent(new CustomEvent('datastar-fetch',{detail:{type:'retrying'}}));`+
		`document.documentElement.getAttribute('data-via-connection')`, &status)
	if status != "connecting" {
		t.Fatalf("a dropped stream did not flip the status to connecting: %q", status)
	}
	if got := s.Text("#via-reconnect-banner"); !strings.Contains(got, "Reconnecting") {
		t.Fatalf("a dropped stream did not surface the reconnect banner: %q", got)
	}

	// The resume is not faked like the drop above: a synthetic patch event makes
	// Datastar apply a payload-less patch and throw, so this rides the ticker's
	// next real server push.
	s.WaitEvalTrue(`document.documentElement.getAttribute('data-via-connection')==='online' && `+
		`(document.getElementById('via-reconnect-banner')||{style:{}}).style.display==='none'`,
		"a real server-push patch cleared the banner and restored online")
	s.RequireCleanConsole()
}

func TestReconnect_giveUpGoesOfflineAndCapsTheReloadLoop(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(clicker{}))

	// Pre-armed at the reload cap so the terminal branch runs without a real reload.
	var armed bool
	s.Eval(`sessionStorage.setItem('__via_rc_reloads','3'); true`, &armed)

	var status string
	s.Eval(`document.dispatchEvent(new CustomEvent('datastar-fetch',{detail:{type:'retries-failed'}}));`+
		`document.documentElement.getAttribute('data-via-connection')`, &status)
	if status != "offline" {
		t.Fatalf("a give-up did not flip the status to offline: %q", status)
	}
	if got := s.Text("#via-reconnect-banner"); !strings.Contains(got, "refresh") {
		t.Fatalf("at the reload cap the manager must advise a manual refresh: %q", got)
	}
	s.RequireCleanConsole()
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
	if got := a.Value("input"); got != "half-typed" {
		t.Fatalf("fan-out clobbered A's in-progress draft: got %q, want %q", got, "half-typed")
	}

	a.RequireCleanConsole()
	b.RequireCleanConsole()
}

// redirectViaScript is a plain page whose @post action calls via.Redirect,
// answered as the hash-admitted one-line navigation script (see redirectInit).
type redirectViaScript struct{}

func (p *redirectViaScript) Go(ctx *via.Ctx) { ctx.Redirect("/done") }
func (p *redirectViaScript) View() h.H {
	return h.Div(h.Button(h.RawAttr("id", "go"), via.On("click", p.Go), h.Str("go")))
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
	if inserted {
		t.Fatal("the redirect script must remove itself from the document")
	}
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
	if status != http.StatusOK {
		t.Fatalf("native PostForm submit from a live unit did not return 200: got %v", status)
	}
	if got := s.Text("body"); strings.Contains(got, "this tab has no stream") {
		t.Fatalf("submit fell through to the plain 410 fallback: %q", got)
	}
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
	if red {
		t.Fatal("an origin the Head never declared must not be admitted by style-src")
	}
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
		`return !!b&&b.style.display!=='none'&&b.textContent.length>0})()`,
		"a clean stream close must put a visible banner on screen")
}

func TestReconnect_staleTabReloadsOn410ButNotOn403(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(clicker{}))
	s.WaitLiveConnected()

	// Dispatched directly: a real 410 and a real 403 on one page would confound
	// the manager's branch table with the stream close.
	const fire = `(function(s){document.dispatchEvent(new CustomEvent('datastar-fetch',` +
		`{detail:{type:'error',el:document.body,argsRaw:{status:s}}}));return true})`

	var ok bool
	s.Eval(`(function(){window.__probe=1;return true})()`, &ok)
	s.Eval(fire+`('403')`, &ok)
	s.WaitEvalTrue(`document.documentElement.getAttribute('data-via-connection')==='offline'`,
		"a 403 must be surfaced as a lost connection")
	s.Sleep(2500 * time.Millisecond) // longer than the manager's 500-2000ms reload jitter
	var probe int
	s.Eval(`window.__probe||0`, &probe)
	if probe != 1 {
		t.Fatal("a 403 must not reload: the server would answer the same way again")
	}

	s.Eval(fire+`('410')`, &ok)
	s.WaitEvalTrue(`window.__probe===undefined`, "a 410 means the page is stale and must reload")
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
		h.Button(h.ID("fill"), via.On("click", c.Fill), h.Str("fill")),
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
	if strings.Contains(root, "uptime_load") {
		t.Fatalf("the root minted a slot for its child's signal: %q", root)
	}
	if !strings.Contains(child, "uptime__load") {
		t.Fatalf("the child unit did not declare its own signal: %q", child)
	}

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
	if !hidden {
		t.Fatal("a child push re-declared the root's client-only signal from its seed")
	}
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
		h.Button(h.ID("hit"), via.On("click", r.Hit), h.Str("hit")),
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
	if err != nil {
		t.Fatalf("the child's signal is unreadable after the root render: %q", s.Text("#n2"))
	}
	s.WaitFor("#n2", func(text string) bool { n, err := strconv.Atoi(text); return err == nil && n > after },
		"the child's Set to keep arriving on the child's own slot after a root render")
	s.RequireCleanConsole()
}
