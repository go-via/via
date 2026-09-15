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
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
	"github.com/go-via/via/vtbrowser"
)

// --- fixtures ---

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

// bClock + bCounter are two LIVE children multiplexed on one stream: a ticking
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

// Two live children on one page must update INDEPENDENTLY in a real browser: the
// clock's server-push morphs only #via-i0, and a click on the counter routes
// (via the tab handshake) to #via-i1 and morphs only that — proving Datastar
// patches each child's container separately over the one shared SSE stream.
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

// pRoot is a PLAIN root (not itself live) with its own action, embedding a
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

// A plain root's own action re-renders #root (Datastar's default whole-root
// morph); the embedded live child's container carries data-ignore-morph, so
// that patch must leave the child's DOM untouched, and the child's own push
// (Datastar inner mode) must still land afterward.
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

// --- harness tests ---

// Open must serve the server-rendered skeleton (including the #root morph
// target) and Datastar must run under the strict nonce'd CSP without a single
// console error. Eval is the escape hatch for DOM facts the named helpers omit.
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

// WaitFor polls the DOM, so it observes a value the server pushes over SSE with
// no client interaction — proving data-init opens the stream and each
// datastar-patch-elements frame morphs #root in a real browser.
func TestWaitFor_observesServerPushMorph(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(liveTicker{}))

	s.WaitFor("p", func(text string) bool {
		var n int
		_, err := fmt.Sscanf(text, "n: %d", &n)
		return err == nil && n >= 2
	}, "the server-pushed count to reach 2 (live morph)")
	s.RequireCleanConsole()
}

// Click drives a live-child action: the count changes only if the $viatab the
// SSE set is sent in the action's signal body, reaching this connection's child
// and pushing the result back over its stream. WaitTextContains absorbs the
// round-trip latency.
func TestClick_roundTripsLiveActionThroughTabHeader(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(clicker{}))

	s.WaitTextContains("p", "count: 0")
	s.WaitLiveConnected()
	s.Click("button")
	s.WaitTextContains("p", "count: 1")
	s.RequireCleanConsole()
}

// Type sends real key events (so Datastar's data-bind fires as for a human) and
// Value reads the bound input's resulting value.
func TestTypeAndValue_driveABoundInput(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(form{}))

	s.Type("input", "alice")
	if got := s.Value("input"); got != "alice" {
		t.Fatalf("Type/Value round-trip failed: got %q, want %q", got, "alice")
	}
	s.RequireCleanConsole()
}

// NewTab opens a second tab in the same browser. A message sent in one tab must
// fan out (Topic → SSE → morph) to the other, presence must reflect both
// connections, and the sender's composer must clear — a deliberate signal-patch
// that WaitValue observes.
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

// The reconnect manager is a nonce'd inline IIFE — wire-shape tests prove it
// ships, but only a real browser proves it PARSES and RUNS under the strict CSP,
// attaches its datastar-fetch listener, and drives the banner + status DOM. A
// dropped stream must surface the "Reconnecting…" banner and flip the status
// attribute; the resume must clear both. The drop is faked with a synthetic
// 'retrying' datastar-fetch event (it carries no patch payload, so Datastar's
// own document listener ignores it) and asserted synchronously. The resume is
// NOT faked — a synthetic patch event would make Datastar try to apply a
// payload-less patch and throw — so it rides a REAL server push: the ticker's
// next element-patch, which is exactly the signal Datastar emits on a live
// stream resume (no started/finished, only an incoming patch).
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

	s.WaitEvalTrue(`document.documentElement.getAttribute('data-via-connection')==='online' && `+
		`(document.getElementById('via-reconnect-banner')||{style:{}}).style.display==='none'`,
		"a real server-push patch cleared the banner and restored online")
	s.RequireCleanConsole()
}

// When Datastar gives up (retries-failed), the manager goes offline and, past
// its reload-loop cap, shows a terminal "please refresh" notice instead of
// scheduling another reload — the guard that stops a permanently-down server
// from pinning a tab in a reload loop. Pre-arming the counter at the cap keeps
// the test deterministic (no real page reload) while exercising that branch.
func TestReconnect_giveUpGoesOfflineAndCapsTheReloadLoop(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(clicker{}))

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

// A fan-out push must NOT clobber what another user is typing: while A composes
// (not yet sent), B sends; A must receive B's line AND keep its own draft. The
// element push omits data-signals precisely so a morph never overwrites a
// client signal a user is editing.
func TestNewTab_fanOutDoesNotClobberInProgressTyping(t *testing.T) {
	r := newRoom()
	a := vtbrowser.Open(t, via.Handler(chat{room: r}))
	b := a.NewTab()

	a.WaitTextContains("h1", "online: 2")

	a.Type("input", "half-typed") // A is composing; has NOT sent
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

// A via.Redirect from a Datastar @post action navigates the browser, and only a
// real browser proves it: the response is a text/javascript body Datastar
// appends to <head>, so it lands only if the strict CSP admits it by SHA-256 and
// Datastar's script branch honours Datastar-Script-Attributes.
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

// A native PostForm submit from inside a live unit must reach the handler and
// come back as a fresh 200 page, not fall through to the plain path's
// fail-closed 410 "this tab has no stream" document. Only a real
// browser can see this: an unfilled `data-attr-value` is not an error, it is
// silently ignored, so no Go-level assertion catches it (see L1).
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

// --- Declared assets under the derived CSP ---

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

// The payoff of declaring a Style: an off-origin stylesheet must actually load
// and apply. No httptest can see this — the server ships the same bytes whether
// or not the browser fetches them.
func TestDocumentHead_declaredOffOriginStylesheetLoads(t *testing.T) {
	origin := cdn(t, "#styled{color:red}")
	app := via.Handler(styledPage{}, via.WithHead(via.Head{
		Assets: via.Assets{Styles: []via.Style{{Href: origin + "/app.css"}}},
	}))
	s := vtbrowser.Open(t, app)
	s.WaitEvalTrue(styledIsRed, "the declared off-origin stylesheet loaded and applied under the derived CSP")
	s.RequireCleanConsole() // a CSP-blocked sheet surfaces as a console error
}

// The other half of the same claim: the policy is exactly as wide as what was
// declared. An @import inside an inline Style points at an origin nothing ever
// declared, so style-src does not cover it and the browser blocks it. This is
// the documented limitation, pinned so it cannot silently become a hole: if a
// declaration ever widened to a wildcard, this test would go green for the
// wrong reason and the assertion below would start failing.
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
	// No console assertion: a refused @import is reported as a browser ISSUE,
	// not through the console API, so it never reaches ConsoleErrors. The style
	// simply not applying is the whole of the evidence — and RequireCleanConsole
	// would be wrong here either way, since a block is the expected outcome.
}

// The 3am case a headless assertion cannot see: a graceful shutdown ends every
// stream by RETURNING, i.e. a clean 200 close. The bundled datastar.js defaults
// each @post to retry:"auto", and under "auto" a body that ends falls through
// to its resolve path — it fires `finished` and never `retrying` or
// `retries-failed`. The manager used to read `finished` as "all good" and hide
// the banner, so the tab looked alive while every click 410'd, with nothing on
// screen and nothing in the log.
//
// Router.Close is that exact close, from inside the library. After it the tab
// must say offline and show the banner.
func TestReconnect_cleanStreamCloseIsReportedToTheUser(t *testing.T) {
	r := via.NewRouter()
	r.Mount("/", liveTicker{})
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

// Datastar's fetch driver reports a >= 400 response through a `datastar-fetch`
// event of type "error" carrying the code as a string in detail.argsRaw.status
// (its onopen hook, in the bundled datastar.js). A 410 means this tab is stale
// — its stream is gone, or the current render no longer binds the action — and
// only a reload fixes it. A 403/5xx is the server's condition, and reloading
// into it would be a loop, so that one is a banner and nothing more.
//
// Driven by dispatching the event directly: what is under test is the
// manager's branch table, and provoking a real 410 and a real 403 on the same
// page would confound them with the stream close.
func TestReconnect_staleTabReloadsOn410ButNotOn403(t *testing.T) {
	s := vtbrowser.Open(t, via.Handler(clicker{}))
	s.WaitLiveConnected()

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

// The deploy case, and the one no httptest can see: SIGTERM ends every stream
// cleanly and the server is then GONE for several seconds while the new
// process starts. The manager used to answer that clean close with a single
// blind location.reload() on a 500-2000ms timer, so the reload landed on
// chrome-error://chromewebdata with no script left alive to try again — a
// permanently dead tab on every rolling restart.
//
// The gap here is deliberately longer than that old give-up window. The page
// must still be alive at the end of it: probing, then reloading once the new
// server answers, then streaming again.
func TestReconnect_recoversAcrossADeployGap(t *testing.T) {
	r := via.NewRouter()
	r.Mount("/", liveTicker{})
	s := vtbrowser.Open(t, r)
	s.WaitLiveConnected()
	s.WaitTextContains("p", "n: 1")

	r.Close() // the graceful shutdown: every stream ends with a clean close

	next := via.NewRouter()
	next.Mount("/", liveTicker{})
	s.Restart(3*time.Second, next)

	s.WaitEvalTrue(`document.documentElement.getAttribute('data-via-connection')==='online'`,
		"the tab must re-bootstrap onto the new server instead of reloading once into a dead one")
	s.WaitTextContains("p", "n: 1")
}

// --- Per-mount assets, in a real browser ---

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

// The only real proof of the feature: a script a page declared in PageMeta
// must EXECUTE under that mount's own CSP — an off-origin one by origin, an
// inline one by hash — and its inline style must apply. Every server-side test
// passes identically whether the browser ran the script or refused it.
func TestPageMeta_declaredScriptsExecuteUnderThePerMountCSP(t *testing.T) {
	origin := jsCDN(t, `window.__external = true`)
	r := via.NewRouter()
	r.Mount("/", scriptPage{Src: origin})
	s := vtbrowser.Open(t, r)

	s.WaitEvalTrue(`window.__external === true`, "the declared off-origin script executed")
	s.WaitEvalTrue(`window.__inline === true`, "the declared inline script was admitted by its hash")
	s.WaitEvalTrue(styledIsRed, "the declared inline style was admitted by its hash")
	s.RequireCleanConsole() // a CSP-blocked script surfaces as a console error
}
