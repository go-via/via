package via_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	t.Parallel()
	app := vt.Serve(t, via.Register(duo{}, via.WithSSEHeartbeat(50*time.Millisecond)))
	conn := app.Connect()

	conn.Await(`id="via-i0"`) // island 0 pushed its container
	conn.Await(`id="via-i1"`) // island 1 pushed its container, independently
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
	t.Parallel()
	done := make(chan struct{})
	handler := via.Register(failPair{A: disposerIsland{disposed: done}})
	req := httptest.NewRequest(http.MethodPost, "/_via/sse", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	go handler.ServeHTTP(&halfOpenFlusher{}, req)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a failed island connect did not dispose the already-connected sibling")
	}
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
	srv := via.Register(solo{}, via.WithSSEHeartbeat(50*time.Millisecond))

	_, body := do(t, serve(t, srv), http.MethodGet, "/", "")
	assert.Contains(t, body, `id="via-i0" data-signals=`, "GET must declare the island's signal")
	assert.Contains(t, body, `data-bind="i0_s0"`, "the island signal uses an island-scoped slot")

	app := vt.Serve(t, srv)
	conn := app.Connect()
	line := conn.Await(`data-bind="i0_s0"`) // the push re-renders the island with the same slot
	assert.NotContains(t, line, "data-signals", "a live push must not re-declare island signals")
}

// A Local[T] inside an island keeps its leading-underscore (client-only) marker
// in front of the island prefix — `_i0_s0` — so Datastar still never POSTs it.
func TestEmbed_localSignalInIslandStaysClientOnly(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(togglePage{})), http.MethodGet, "/", "")
	assert.Contains(t, body, `data-bind="_i0_s0"`,
		"a Local in an island keeps its leading underscore before the island prefix")
}

// togglePage embeds an island whose only signal is a client-only Local.
type toggleIsland struct{ open via.Local[bool] }

func (i *toggleIsland) View() h.H { return h.Div(i.open.Bind()) }

type togglePage struct{ T toggleIsland }

func (p *togglePage) View() h.H { return h.Div(via.Embed(p.T)) }

// greeter renders a seeded field — the vehicle for proving an island child can
// receive constructor data (a dep) rather than only its zero value.
type greeter struct{ who string }

func (g *greeter) View() h.H { return h.Div(h.P(h.Str("hi "), h.Str(g.who))) }

type greetPage struct{ G greeter }

func (p *greetPage) View() h.H { return h.Div(via.Embed(p.G)) }

// An island child must be able to receive injected data, not only its zero
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
	t.Parallel()
	app := vt.Serve(t, via.Register(panel{}, via.WithSSEHeartbeat(50*time.Millisecond)))
	conn := app.Connect()

	req, err := http.NewRequest(http.MethodPost, app.URL()+"/_via/a/0/0", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Via-Tab", conn.TabID()) // route to THIS connection's island 0
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode, "a live mux action acks 204; the result rides the SSE")

	line := conn.Await("c=1")
	assert.Contains(t, line, "via-i0", "the action's push must target its own island container")
}

// A live mux action with an unknown tab must fail closed (410) so a stale client
// re-bootstraps rather than mutating a throwaway.
func TestMux_liveIslandActionWithUnknownTabIsGone(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(panel{}))
	app.Connect() // establish the app, but use a bogus tab below

	req, err := http.NewRequest(http.MethodPost, app.URL()+"/_via/a/0/0", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Via-Tab", "bogus-tab-id")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusGone, resp.StatusCode)
}

// A live island's action binding must carry both the island id and the X-Via-Tab
// header, so the POST reaches the right island on the right connection.
func TestMux_liveIslandActionBindingCarriesTabHeader(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(panel{})), http.MethodGet, "/", "")
	assert.Contains(t, body, `@post('/_via/a/0/0',{headers:{'X-Via-Tab':$_viatab}})`,
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
		`id="via-i0"`,          // first island's container
		`id="via-i1"`,          // second island's container
		`@post('/_via/a/0/0')`, // island 0, action 0
		`@post('/_via/a/1/0')`, // island 1, action 0 — scoped, not a shared flat index
	} {
		assert.Contains(t, body, want, "embedded islands missing container/scoped-action")
	}
}

// An action must route to the island named in its path, mutate that island, and
// the response must patch that island's container (not #root, not its sibling).
func TestEmbed_actionRoutesToItsIslandAndPatchesThatContainer(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(board{}))

	resp, body := do(t, srv, http.MethodPost, "/_via/a/1/0", "{}") // bump island 1
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

	resp, _ := do(t, srv, http.MethodPost, "/_via/a/0/1", "{}") // island 0, Noop
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// A non-existent island or action index must fail closed (410) so a stale client
// re-bootstraps rather than misrouting onto the wrong island.
func TestEmbed_unknownIslandOrActionIsGone(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(board{}))

	for _, path := range []string{"/_via/a/9/0", "/_via/a/0/9"} {
		resp, _ := do(t, srv, http.MethodPost, path, "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode, "out-of-range %s must be 410", path)
	}
}

// Verify the scoped action path is genuinely distinct per island (regression
// guard against a flat shared index leaking across islands).
func TestEmbed_siblingIslandsDoNotShareAnActionIndexSpace(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(board{})), http.MethodGet, "/", "")
	// Both islands declare action 0 within their OWN namespace; neither uses a
	// page-global "/_via/a/1" flat index.
	assert.True(t, strings.Contains(body, `/_via/a/0/0`) && strings.Contains(body, `/_via/a/1/0`),
		"each island must own a /{island}/{n} table, not a flat page index")
}
