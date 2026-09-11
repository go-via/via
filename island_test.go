package via_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// beater is a LIVE embeddable island: it ticks its own State, so each one pushes
// independently over the shared connection.
type beater struct {
	label string
	n     via.State[int]
}

func (b *beater) OnConnect(ctx *via.Ctx) error { ctx.Tick(15*time.Millisecond, b.tick); return nil }
func (b *beater) tick(ctx *via.Ctx)            { b.n.Set(b.n.Get() + 1) }
func (b *beater) View() h.H {
	return h.Div(h.P(h.Str(b.label+"="), b.n.Display()))
}

// duo embeds two live beaters; it does NOT itself implement OnConnect — it is a
// multiplex parent whose live children share one SSE stream.
type duo struct{ A, B beater }

func (d *duo) View() h.H { return h.Div(via.Embed(d.A), via.Embed(d.B)) }

// Each live island must push its OWN container over the one shared stream: a tick
// in island 0 patches #via-i0, a tick in island 1 patches #via-i1 — independently,
// on a parent that is not itself a live island.
func TestMux_eachLiveIslandPushesItsOwnContainer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(duo{}))
		conn := app.Connect()

		conn.Await(`id="via-i0"`) // island 0 pushed its container
		conn.Await(`id="via-i1"`) // island 1 pushed its container, independently
	})
}

// disposerIsland registers a teardown on connect; failerIsland's OnConnect
// errors. Embedded as siblings (disposer first), a failed connect must run the
// already-connected sibling's disposer so its subscriptions don't leak.
type disposerIsland struct{ disposed chan struct{} }

func (d *disposerIsland) OnConnect(ctx *via.Ctx) error {
	ctx.OnDispose(func() { close(d.disposed) })
	return nil
}
func (d *disposerIsland) View() h.H { return h.Div(h.Str("ok")) }

type failerIsland struct{}

func (f *failerIsland) OnConnect(ctx *via.Ctx) error { return errors.New("connect boom") }
func (f *failerIsland) View() h.H                    { return h.Div(h.Str("x")) }

type failPair struct {
	A disposerIsland
	B failerIsland
}

func (p *failPair) View() h.H { return h.Div(via.Embed(p.A), via.Embed(p.B)) }

// If one island's OnConnect fails, the islands connected before it must have
// their disposers run — otherwise a multiplex page leaks the subscriptions of
// the siblings that already connected.
func TestMux_onConnectFailureDisposesConnectedSiblings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		handler := via.Register(failPair{A: disposerIsland{disposed: done}})
		req := httptest.NewRequest(http.MethodPost, "/_via/sse", nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")

		go handler.ServeHTTP(&halfOpenFlusher{}, req)
		synctest.Wait()

		select {
		case <-done:
		default:
			require.Fail(t, "a failed island connect did not dispose the already-connected sibling")
		}
	})
}

// panickerIsland's OnConnect panics instead of returning an error.
type panickerIsland struct{}

func (p *panickerIsland) OnConnect(ctx *via.Ctx) error { panic("connect boom") }
func (p *panickerIsland) View() h.H                    { return h.Div(h.Str("x")) }

type panicPair struct {
	A disposerIsland
	B panickerIsland
}

func (p *panicPair) View() h.H { return h.Div(via.Embed(p.A), via.Embed(p.B)) }

// If one island's OnConnect panics, the islands connected before it must still
// have their disposers run — otherwise a multiplex page leaks the subscriptions
// of the siblings that already connected.
func TestMux_onConnectPanicDisposesConnectedSiblings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		handler := via.Register(panicPair{A: disposerIsland{disposed: done}})
		req := httptest.NewRequest(http.MethodPost, "/_via/sse", nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")

		go handler.ServeHTTP(&halfOpenFlusher{}, req)
		synctest.Wait()

		select {
		case <-done:
		default:
			require.Fail(t, "a panicked island connect did not dispose the already-connected sibling")
		}
	})
}

// namer has a client Signal — two of them as sibling islands must not collide on
// the same slot name.
type namer struct{ name via.Signal[string] }

func (n *namer) View() h.H { return h.Div(n.name.Bind(), h.Span(n.name.Display())) }

type pair struct{ X, Y namer }

func (p *pair) View() h.H { return h.Div(via.Embed(p.X), via.Embed(p.Y)) }

// Sibling islands that each declare a Signal must get DISTINCT slot names —
// without a per-island prefix both would claim "s0" and clobber each other in
// the page's global Datastar store. Each island also declares its own signals on
// its container so they reach the store.
func TestEmbed_islandSignalsAreScopedPerIsland(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(pair{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "i0_s0", "island 0's signal must carry an island-scoped slot")
	assert.Contains(t, body, "i1_s0", "island 1's signal must carry an island-scoped slot")
	assert.Contains(t, body, `id="via-i0" data-signals=`, "island 0 must declare its own signals")
	assert.Contains(t, body, `id="via-i1" data-signals=`, "island 1 must declare its own signals")
}

// liveNamer is a LIVE island carrying BOTH a client Signal and ticking State —
// the chat-composer-in-multiplex case: the push must keep the Signal bound.
type liveNamer struct {
	draft via.Signal[string]
	beats via.State[int]
}

func (n *liveNamer) OnConnect(ctx *via.Ctx) error { ctx.Tick(15*time.Millisecond, n.tick); return nil }
func (n *liveNamer) tick(ctx *via.Ctx)            { n.beats.Set(n.beats.Get() + 1) }
func (n *liveNamer) View() h.H {
	return h.Div(n.draft.Bind(), h.P(h.Str("beats="), n.beats.Display()))
}

type solo struct{ X liveNamer }

func (s *solo) View() h.H { return h.Div(via.Embed(s.X)) }

// A live island's signal must keep the SAME island-scoped slot across a push, or
// the client binding breaks; and the push must NOT re-declare data-signals, or a
// fan-out would clobber what the user is editing. The GET declares i0_s0 on the
// container; a tick-driven push re-binds i0_s0 with no data-signals.
func TestMux_liveIslandSignalSlotIsStableAndPushOmitsDeclaration(t *testing.T) {
	t.Parallel()
	srv := via.Register(solo{})

	// The plain GET check runs on a real listener, outside any synctest
	// bubble — it needs no fake time and this keeps the two halves of the
	// test independent.
	_, body := do(t, serve(t, srv), http.MethodGet, "/", "")
	assert.Contains(t, body, `id="via-i0" data-signals=`, "GET must declare the island's signal")
	assert.Contains(t, body, `data-bind="i0_s0"`, "the island signal uses an island-scoped slot")

	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, srv)
		conn := app.Connect()
		line := conn.Await(`data-bind="i0_s0"`) // the push re-renders the island with the same slot
		assert.NotContains(t, line, "data-signals", "a live push must not re-declare island signals")
	})
}

// A SignalClientOnly[T] inside an island keeps its leading-underscore (client-only) marker
// in front of the island prefix — `_i0_s0` — so Datastar still never POSTs it.
func TestEmbed_localSignalInIslandStaysClientOnly(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(togglePage{})), http.MethodGet, "/", "")
	assert.Contains(t, body, `data-bind="_i0_s0"`,
		"a Local in an island keeps its leading underscore before the island prefix")
}

// togglePage embeds an island whose only signal is a client-only Local.
type toggleIsland struct{ open via.SignalClientOnly[bool] }

func (i *toggleIsland) View() h.H { return h.Div(i.open.Bind()) }

type togglePage struct{ T toggleIsland }

func (p *togglePage) View() h.H { return h.Div(via.Embed(p.T)) }

// greeter renders a seeded field — the vehicle for proving an island child can
// receive constructor data (a dep) rather than only its zero value.
type greeter struct{ who string }

func (g *greeter) View() h.H { return h.Div(h.P(h.Str("hi "), h.Str(g.who))) }

type greetPage struct{ G greeter }

func (p *greetPage) View() h.H { return h.Div(via.Embed(p.G)) }

// An island child must be able to receive injected data beyond its zero
// value — the parent's literal seeds it by value (no '&'), and the per-connection copy
// keeps it isolated. Without seeding, a real island (chat needing a *Room) is
// impossible.
func TestEmbed_newIslandSeedsTheChild(t *testing.T) {
	t.Parallel()
	app := via.Register(greetPage{G: greeter{who: "alice"}})
	_, body := do(t, serve(t, app), http.MethodGet, "/", "")
	assert.Contains(t, body, "hi alice", "the parent literal must seed the embedded child")
}

// liveClicker is a LIVE island with State + an action (dep-free, so a zero-value
// Island works) — the vehicle for routing a live action to its own island.
type liveClicker struct{ n via.State[int] }

func (c *liveClicker) OnConnect(ctx *via.Ctx) error { return nil }
func (c *liveClicker) Bump(ctx *via.Ctx)            { c.n.Set(c.n.Get() + 1) }
func (c *liveClicker) View() h.H {
	return h.Div(h.P(h.Str("c="), c.n.Display()), h.Button(via.OnClick(c.Bump), h.Str("+")))
}

// panel embeds two live clickers.
type panel struct{ A, B liveClicker }

func (p *panel) View() h.H { return h.Div(via.Embed(p.A), via.Embed(p.B)) }

// A live island's action must route over the shared connection to THAT island's
// goroutine (via the tab handshake), mutate its State, and push the result to
// its own #via-i{n} — not the sibling, not the whole page.
func TestMux_liveIslandActionRoutesToItsIslandAndPushes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(panel{}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")

		req, err := http.NewRequest(http.MethodPost, app.URL()+actionURL(t, page, 1, 0), strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Datastar-Request", "true")
		req.Header.Set("X-Via-Tab", conn.TabID()) // route to THIS connection's island (url id 1)
		resp, err := app.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode, "a live mux action acks 204; the result rides the SSE")

		line := conn.Await("c=1")
		assert.Contains(t, line, "via-i0", "the action's push must target its own island container")
	})
}

// A live mux action with an unknown tab must fail closed (410) so a stale client
// re-bootstraps rather than mutating a throwaway.
func TestMux_liveIslandActionWithUnknownTabIsGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(panel{}))
		app.Connect() // establish the app, but use a bogus tab below
		page := fetchPage(t, app, "/")

		req, err := http.NewRequest(http.MethodPost, app.URL()+actionURL(t, page, 1, 0), strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Datastar-Request", "true")
		req.Header.Set("X-Via-Tab", "bogus-tab-id")
		resp, err := app.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusGone, resp.StatusCode)
	})
}

// A live island's action binding must carry both the island id and the X-Via-Tab
// header, so the POST reaches the right island on the right connection.
func TestMux_liveIslandActionBindingCarriesTabHeader(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(panel{})), http.MethodGet, "/", "")
	assert.Regexp(t, `@post\('/_via/a/1/0\?v=[^']+',\{headers:\{'X-Via-Tab':\$_viatab\}\}\)`, body,
		"a live island action must carry its island id and the tab header")
}

// A parent that embeds live islands (but isn't itself a live composition) must
// bootstrap the SSE stream on its GET page, so its islands can connect.
func TestMux_parentWithLiveIslandsEmitsTheBootstrap(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(duo{})), http.MethodGet, "/", "")
	assert.Contains(t, body, `data-init="@post('/_via/sse')"`,
		"a parent with live islands must bootstrap the stream")
}

// A parent whose embedded islands are NOT live must stay streamless — no
// bootstrap, so a purely interactive (stateless-action) multi-island page pays
// for no SSE connection.
func TestEmbed_parentWithNoLiveIslandsOmitsTheBootstrap(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(board{})), http.MethodGet, "/", "")
	assert.NotContains(t, body, "@post('/_via/sse')",
		"non-live islands must not trigger an SSE bootstrap")
}

// kid is an embeddable child composition with its own action and state.
type kid struct{ n int }

func (k *kid) Bump(ctx *via.Ctx) { k.n++ }
func (k *kid) Noop(ctx *via.Ctx) {} // changes nothing the View reads
func (k *kid) View() h.H {
	return h.Div(
		h.P(h.Str("n="), h.Str(k.n)),
		h.Button(via.OnClick(k.Bump), h.Str("+")),    // action 0
		h.Button(via.OnClick(k.Noop), h.Str("noop")), // action 1
	)
}

// board embeds two kids as sibling islands.
type board struct{ A, B kid }

func (b *board) View() h.H { return h.Div(via.Embed(b.A), via.Embed(b.B)) }

// Each embedded island must render into its own positional container and wire
// its actions to an island-scoped path, so sibling islands stay independent and
// a patch can target exactly one of them.
func TestEmbed_rendersEachIslandInItsOwnContainerWithScopedActions(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(board{})), http.MethodGet, "/", "")

	for _, want := range []string{
		`id="via-i0"`,           // first island's container
		`id="via-i1"`,           // second island's container
		`@post('/_via/a/1/0?v=`, // first island (url id 1), action 0
		`@post('/_via/a/2/0?v=`, // second island (url id 2), action 0 — scoped, not a shared flat index
	} {
		assert.Contains(t, body, want, "embedded islands missing container/scoped-action")
	}
}

// An action must route to the island named in its path, mutate that island, and
// the response must patch that island's container (not #root, not its sibling).
func TestEmbed_actionRoutesToItsIslandAndPatchesThatContainer(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(board{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, 2, 0), "{}") // bump the second island (url id 2)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Contains(t, body, `id="via-i1"`, "patch must target the acted island's container")
	assert.Contains(t, body, "n=1", "the acted island must reflect its mutation")
	// The patch must be island-scoped, not a whole-page re-render — otherwise
	// sibling islands get clobbered and the multiplex point is lost.
	assert.NotContains(t, body, `id="via-i0"`, "patch must not include the sibling island")
	assert.NotContains(t, body, `id="root"`, "patch must be the island container, not #root")
}

// An action that changes nothing the island's View reads returns 204, not a
// redundant patch the browser would morph onto itself — the per-island analogue
// of the flat path's no-op contract.
func TestEmbed_actionWithNoVisibleChangeReturns204(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(board{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	resp, _ := do(t, srv, http.MethodPost, actionURL(t, page, 1, 1), "{}") // first island (url id 1), Noop
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// A non-existent island or action index must fail closed (410) so a stale client
// re-bootstraps rather than misrouting onto the wrong island. This carries a
// genuinely valid shape digest (read off the rendered page, like
// TestLive_outOfRangeActionAnswers410) with only the island or action segment
// forged — a bare "no ?v=" URL would already 410 as "stale page" before ever
// reaching the range check this guards.
func TestEmbed_unknownIslandOrActionIsGone(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(board{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, 1, 0)

	for _, path := range []string{swapIslandIndex(t, url, "10"), swapActionIndex(t, url, "9")} {
		resp, _ := do(t, srv, http.MethodPost, path, "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode, "out-of-range %s must be 410", path)
	}
}

// reuseA is a live island whose Act bumps a shared, test-visible counter — the
// vehicle for proving an action's URL still names ITS OWN island after its
// live parent re-renders.
type reuseA struct {
	n    via.State[int]
	hits *atomic.Int64
}

func (a *reuseA) OnConnect(*via.Ctx) error { return nil }
func (a *reuseA) Act(*via.Ctx)             { a.hits.Add(1); a.n.Set(a.n.Get() + 1) }
func (a *reuseA) View() h.H {
	return h.Div(h.P(h.Str("A")), h.Button(via.OnClick(a.Act), h.Str("a")))
}

// reuseP is a PLAIN (non-live) island embedded after a live sibling.
type reuseP struct{ hits *atomic.Int64 }

func (p *reuseP) Act(*via.Ctx) { p.hits.Add(1) }
func (p *reuseP) View() h.H {
	return h.Div(h.P(h.Str("P")), h.Button(via.OnClick(p.Act), h.Str("p")))
}

// reuseRoot is itself live (it ticks), so every beat re-renders it and calls
// Embed(A) then Embed(P) again — A is a live descendant the reuse path finds
// and re-registers; P is a fresh by-value re-seed every time.
type reuseRoot struct {
	A     reuseA
	P     reuseP
	beats via.State[int]
}

func (r *reuseRoot) OnConnect(ctx *via.Ctx) error { ctx.Tick(15*time.Millisecond, r.beat); return nil }
func (r *reuseRoot) beat(*via.Ctx)                { r.beats.Set(r.beats.Get() + 1) }
func (r *reuseRoot) View() h.H {
	return h.Div(h.P(h.Str("beats="), r.beats.Display()), via.Embed(r.A), via.Embed(r.P))
}

// A live parent's own re-render must still advance its page-wide island index
// once per Embed call on the REUSE path, exactly as the fresh-seed path does —
// otherwise a later sibling (P) is renumbered onto an EARLIER live
// descendant's already-claimed index: both containers end up "via-i0", and
// P's own rendered button carries A's OWN dispatch address. An attacker who
// reads the page-wide $_viatab signal (visible to any script on a live page)
// can then attach it to that address and run A's action under P's label —
// exactly the misroute this guards, so the check fires the URL WITH the tab
// header, the attack's own shape, not a plain click.
func TestMux_liveDescendantReuseAdvancesLaterSiblingsIndex(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var aHits, pHits atomic.Int64
		app := vt.Serve(t, via.Register(reuseRoot{A: reuseA{hits: &aHits}, P: reuseP{hits: &pHits}}))
		conn := app.Connect()

		line := conn.Await("beats=1") // the root's first tick-driven re-render
		assert.NotContains(t, line, `id="via-i0"><p>P`, "P must not collide onto A's container id")

		pURL := actionURLRe(t, line, 2, 0) // P's own address, distinct from A's (url id 1)
		status, _ := app.Action(0).Raw(pURL).Tab(conn.TabID()).Fire()
		assert.Equal(t, http.StatusGone, status, "P is not a live unit; its address must not resolve to A's")
		assert.Zero(t, aHits.Load(), "P's URL must never run A's action")
	})
}

// actionURLRe extracts the currently-live action URL for {island}/{n} out of
// an SSE push line (vt.Action.Fire only reads off a GET page, not a push).
func actionURLRe(t *testing.T, line string, island, n int) string {
	t.Helper()
	pat := `_via/a/` + strconv.Itoa(island) + `/` + strconv.Itoa(n) + `\?v=[^'"\\]+`
	m := regexp.MustCompile(pat).FindString(line)
	require.NotEmptyf(t, m, "action %d/%d not found in push line:\n%s", island, n, line)
	return "/" + m
}

// nestQ is a plain island embedded before a live sibling (occupies url id 1).
type nestQ struct{ hits *atomic.Int64 }

func (q *nestQ) Act(*via.Ctx) { q.hits.Add(1) }
func (q *nestQ) View() h.H    { return h.Div(h.P(h.Str("Q")), h.Button(via.OnClick(q.Act), h.Str("q"))) }

// nestC is a plain island nested INSIDE a live island (nestL below).
type nestC struct{ hits *atomic.Int64 }

func (c *nestC) Act(*via.Ctx) { c.hits.Add(1) }
func (c *nestC) View() h.H    { return h.Div(h.P(h.Str("C")), h.Button(via.OnClick(c.Act), h.Str("c"))) }

// nestL is live and embeds nestC — so C's page-wide index (assigned during
// first paint by the ONE shared pass) is L's own index + 1, not 0.
type nestL struct {
	C     nestC
	beats via.State[int]
}

func (l *nestL) OnConnect(ctx *via.Ctx) error { ctx.Tick(15*time.Millisecond, l.beat); return nil }
func (l *nestL) beat(*via.Ctx)                { l.beats.Set(l.beats.Get() + 1) }
func (l *nestL) View() h.H {
	return h.Div(h.P(h.Str("lb="), l.beats.Display()), via.Embed(l.C))
}

type nestRoot struct {
	Q nestQ
	L nestL
}

func (r *nestRoot) View() h.H { return h.Div(via.Embed(r.Q), via.Embed(r.L)) }

// L's own push re-renders ONLY L (a standalone renderIslandBind, not the whole
// page), so its local index allocator must pick up numbering where the
// page-wide first-paint pass left off (L's own index + 1) — not restart at 0,
// which would renumber C onto Q's already-claimed url id (both containers end
// up "via-i0") and misroute a click on C's button into Q's handler. C itself
// is a plain island nested two levels deep and stays unreachable by stateless
// dispatch either way (dispatchStateless's unit lookup is one level only) —
// the fix's job is failing safe (410, Q's action never runs), not making a
// nested plain child independently addressable.
func TestMux_nestedPlainChildOfLiveIslandKeepsFirstPaintIndex(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var qHits, cHits atomic.Int64
		app := vt.Serve(t, via.Register(nestRoot{Q: nestQ{hits: &qHits}, L: nestL{C: nestC{hits: &cHits}}}))
		conn := app.Connect()

		line := conn.Await("lb=1") // L's first tick-driven push
		assert.NotContains(t, line, `id="via-i0"`, "C must not collide onto Q's container id")
		cURL := actionURLRe(t, line, 3, 0) // C's own address, distinct from Q's (url id 1)

		status, _ := app.Action(0).Raw(cURL).Fire() // C is plain: no live connection, dispatches stateless
		assert.Equal(t, http.StatusGone, status)
		assert.Zero(t, qHits.Load(), "C's URL must never run Q's action")
		assert.Zero(t, cHits.Load())
	})
}

// Verify the scoped action path is genuinely distinct per island (regression
// guard against a flat shared index leaking across islands).
func TestEmbed_siblingIslandsDoNotShareAnActionIndexSpace(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(board{})), http.MethodGet, "/", "")
	// Both islands declare action 0 within their OWN namespace; neither uses a
	// page-global flat index.
	assert.True(t, strings.Contains(body, `/_via/a/1/0`) && strings.Contains(body, `/_via/a/2/0`),
		"each island must own a /{island}/{n} table, not a flat page index")
}

// swapA and swapB are two DIFFERENTLY TYPED live islands with distinguishable
// output — the vehicle for proving childSlots reuse checks identity, not just
// ordinal position.
type swapA struct{}

func (a *swapA) OnConnect(*via.Ctx) error { return nil }
func (a *swapA) View() h.H                { return h.Div(h.Str("AAA")) }

type swapB struct{}

func (b *swapB) OnConnect(*via.Ctx) error { return nil }
func (b *swapB) View() h.H                { return h.Div(h.Str("BBB")) }

// swapRoot is a live root whose Flip toggles Hide, which via.When guards
// swapA behind (zero-value Hide=false, so swapA shows on the very first
// render — GET and connect alike, with no OnConnect needed to reach it);
// swapB is unconditional. Flipping Hide to true drops swapA's Embed call
// entirely, shifting swapB down onto swapA's OLD childSlots ordinal (0) —
// the exact case Embed's own godoc recommends via.When for.
type swapRoot struct {
	Hide via.State[bool]
	A    swapA
	B    swapB
}

func (r *swapRoot) OnConnect(ctx *via.Ctx) error { return nil }
func (r *swapRoot) Flip(ctx *via.Ctx)            { r.Hide.Set(!r.Hide.Get()) }
func (r *swapRoot) embedA() h.H                  { return via.Embed(r.A) }
func (r *swapRoot) View() h.H {
	return h.Div(
		via.When(!r.Hide.Get(), r.embedA),
		via.Embed(r.B),
		h.Button(via.OnClick(r.Flip)),
	)
}

// A When-guarded Embed disappearing must never render its sibling's container
// with the vanished occupant's stale content — childSlots is keyed by
// (parent, ordinal), and swapA's disappearance shifts swapB down onto swapA's
// old ordinal, so an islandIdx match alone (both land on idx 0) can't tell
// the two apart. The reuse check must also confirm the occupant's concrete
// type still matches what's being embedded.
func TestEmbed_whenFlipDoesNotSwapSiblingIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(swapRoot{}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")
		assert.Contains(t, page, `id="via-i0"><div>AAA</div>`)
		assert.Contains(t, page, `id="via-i1"><div>BBB</div>`)

		status, _ := app.Action(0).Tab(conn.TabID()).Fire() // Flip: Hide -> true
		require.Equal(t, http.StatusNoContent, status)

		line := conn.Await(`id="via-i0"><div>BBB</div>`)
		assert.NotContains(t, line, `id="via-i0"><div>AAA</div>`,
			"swapB's container must show swapB's own content, not swapA's stale reused instance")
	})
}

// banner is a plain (stateless) composition embedded by a layout.
type banner struct{}

func (b *banner) View() h.H { return h.P(h.Str("BANNER")) }

// shell is a generic layout: it renders a frame and embeds whatever composition
// its Body field holds — plain struct-field composition, no wrapper type. This
// is the content projection via.Embed exists for: one shell composes with any
// page, and the field type names the content statically.
type shell[C any] struct{ Body C }

func (s *shell[C]) View() h.H { return h.Div(h.H1(h.Str("SHELL")), via.Embed(s.Body)) }

// An embedded child renders in place, inside the layout's frame, wired as a
// positional island container. Fails if Embed stops wiring the container or
// renders the child outside the frame.
func TestEmbed_projectsChildInPlace(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(shell[banner]{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "SHELL", "the layout frame renders")
	assert.Contains(t, body, "BANNER", "the embedded content renders inside the frame")
	assert.Contains(t, body, `id="via-i0"`, "embedded content is wired as a positional island container")
	assert.Less(t, strings.Index(body, "SHELL"), strings.Index(body, "BANNER"),
		"content is embedded in place, after the frame heading")
}

// liveShell is a NON-live layout (no OnConnect) whose Body field holds a LIVE
// island. The page must bootstrap its SSE stream and the embedded live island
// must push its own container and render its server State — proving plain
// struct-field composition rides the live multiplex machinery.
type liveShell struct{ Body beater }

func (s *liveShell) View() h.H { return h.Div(h.H1(h.Str("APP")), via.Embed(s.Body)) }

func TestEmbed_projectsLiveIsland(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(liveShell{Body: beater{label: "hb"}}))
	conn := app.Connect()

	conn.Await(`id="via-i0"`) // the embedded live island pushes its own container
	conn.Await("hb=")         // and renders its own server State through the field
}

// Embed panics when the child lacks a View() — a wrote-it-wrong error surfaces
// loudly at the first render, never as a silent blank region.
func TestEmbed_panicsWithoutView(t *testing.T) {
	t.Parallel()
	require.PanicsWithValue(t,
		"via: via.Embed(child) requires child to have a View() method",
		func() { via.Embed(struct{ X int }{}) },
	)
}

// nestHost is a PLAIN island (itself embedded by a page) whose own View
// embeds a LIVE child — the two-deep composition B1 makes work.
type nestHost struct{ Inner beater }

func (n *nestHost) View() h.H { return h.Div(via.Embed(n.Inner)) }

type nestPage struct{ Host nestHost }

func (p *nestPage) View() h.H { return h.Div(via.Embed(p.Host)) }

// A live island nested two levels deep (a plain page embeds a plain island
// that embeds a live child) must still get discovered, wired, and streamed —
// island discovery walks the embedded tree at any depth, not just the page's
// direct children.
func TestEmbed_liveChildInsideLiveIslandAtDepthTwo(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(nestPage{Host: nestHost{Inner: beater{label: "hb"}}}))
	conn := app.Connect()

	conn.Await(`id="via-i1"`) // the depth-two live child gets its own container, distinct from its wrapper island's via-i0
	conn.Await("hb=")         // and streams its own server state independently
}

// The guard is about LIVE children only: a plain (stateless) child embedded
// inside an island still renders in place. Fails if the guard over-reaches to
// all nesting.
type plainNestHost struct{ Inner banner }

func (n *plainNestHost) View() h.H { return h.Div(h.H2(h.Str("HOST")), via.Embed(n.Inner)) }

type plainNestPage struct{ Host plainNestHost }

func (p *plainNestPage) View() h.H { return h.Div(via.Embed(p.Host)) }

func TestEmbed_allowsNestedPlainChild(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(plainNestPage{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "HOST", "the island renders")
	assert.Contains(t, body, "BANNER", "the nested plain child renders in place")
}

// livePage is a LIVE root (implements OnConnect) whose View embeds a LIVE
// island — both stream over the page's one connection.
type livePage struct{ Inner beater }

func (p *livePage) View() h.H                { return h.Div(via.Embed(p.Inner)) }
func (p *livePage) OnConnect(*via.Ctx) error { return nil }

// A live page embedding a live island: the child gets its own container and
// pushes independently of the page over the shared connection.
func TestEmbed_liveChildInsideLivePageStreamsIndependently(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(livePage{Inner: beater{label: "hb"}}))
	conn := app.Connect()

	conn.Await(`id="via-i0"`) // the embedded live child pushes its own container
	conn.Await("hb=")         // and renders its own server state independently of the page
}

// tickingParentWithLiveChild is a LIVE root that ticks its OWN state — so it
// re-renders (and re-embeds its child) on every beat — and embeds a live
// child whose own state is independent.
type tickingParentWithLiveChild struct {
	Child liveClicker
	beats via.State[int]
}

func (p *tickingParentWithLiveChild) OnConnect(ctx *via.Ctx) error {
	ctx.Tick(15*time.Millisecond, p.beat)
	return nil
}
func (p *tickingParentWithLiveChild) beat(ctx *via.Ctx) { p.beats.Set(p.beats.Get() + 1) }
func (p *tickingParentWithLiveChild) View() h.H {
	return h.Div(h.P(h.Str("beats="), p.beats.Display()), via.Embed(p.Child))
}

// Embed takes the child by value: without reusing the connected child's own
// instance, every one of the parent's own re-renders would re-seed a fresh
// (zeroed) copy from the parent's field, silently resetting the child's
// state. This is the by-value staleness hazard B1 must close.
func TestEmbed_liveParentPushKeepsLiveChildState(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(tickingParentWithLiveChild{}))
	conn := app.Connect()
	conn.Await(`id="via-i0"`)

	code, _ := app.IslandAction(1, 0).Tab(conn.TabID()).Fire() // bump the child once
	require.Equal(t, http.StatusNoContent, code)
	conn.Await("c=1") // the child's own push already reflects the bump

	// The parent's own tick re-renders (and re-embeds) the child on every
	// beat; the child's state must still read 1 in that frame.
	line := conn.Await("beats=")
	assert.Contains(t, line, "c=1", "the parent's own push must not clobber the child's server state")
}

// The guard is about LIVE children only: a live page may still embed a plain
// (stateless) child — the whole-page push re-renders it in place, which is its
// normal semantics. Fails if the guard over-reaches to all embeds.
type livePlainPage struct{ Inner banner }

func (p *livePlainPage) View() h.H                { return h.Div(h.H1(h.Str("LIVEPAGE")), via.Embed(p.Inner)) }
func (p *livePlainPage) OnConnect(*via.Ctx) error { return nil }

func TestEmbed_allowsPlainChildInLivePage(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(livePlainPage{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "LIVEPAGE", "the live page renders")
	assert.Contains(t, body, "BANNER", "the plain child renders in place")
}
