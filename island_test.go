package via_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strconv"
	"strings"
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

func (b *beater) OnInit(ctx *via.Ctx) error { ctx.Tick(15*time.Millisecond, b.tick); return nil }
func (b *beater) tick(ctx *via.Ctx)         { b.n.Set(b.n.Get() + 1) }
func (b *beater) View() h.H {
	return h.Div(h.P(h.Str(b.label+"="), b.n.Display()))
}

// duo embeds two live beaters; it does NOT itself implement OnInit — it is a
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

		conn.Await(`selector #via-i0`) // island 0 pushed its container
		conn.Await(`selector #via-i1`) // island 1 pushed its container, independently
	})
}

// liveProbe is an embedded LIVE island (State alone makes it live) that
// registers an OnLive/OnDispose pair, so a test can prove whether the pair ran
// at all. failerIsland's OnInit errors; panickerIsland's panics.
type liveProbe struct {
	acquired chan struct{}
	n        via.State[int]
}

func (d *liveProbe) OnInit(ctx *via.Ctx) error {
	ctx.OnLive(d.markAcquired)
	return nil
}
func (d *liveProbe) markAcquired() { close(d.acquired) }
func (d *liveProbe) View() h.H     { return h.Div(h.Str("ok"), d.n.Display()) }

type failerIsland struct{}

func (f *failerIsland) OnInit(ctx *via.Ctx) error { return errors.New("init boom") }
func (f *failerIsland) View() h.H                 { return h.Div(h.Str("x")) }

type failPair struct {
	A liveProbe
	B failerIsland
}

func (p *failPair) View() h.H { return h.Div(via.Embed(p.A), via.Embed(p.B)) }

// An embedded child's OnInit runs inside the parent's render, before any unit
// has a stream: a failure there must abort the whole connect with a 500 and
// leave every sibling's OnLive unrun, so no acquire is left without its
// release.
func TestMux_childInitErrorAbortsTheConnectAndAcquiresNothing(t *testing.T) {
	t.Parallel()
	acquired := make(chan struct{})
	resp, _ := do(t, serve(t, via.Register(failPair{A: liveProbe{acquired: acquired}})),
		http.MethodPost, "/_via/sse", "")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"an embedded child's failed OnInit must abort the connect")
	select {
	case <-acquired:
		require.Fail(t, "a sibling's OnLive ran even though the connect never opened")
	default:
	}
}

// panickerIsland's OnInit panics instead of returning an error.
type panickerIsland struct{}

func (p *panickerIsland) OnInit(ctx *via.Ctx) error { panic("init boom") }
func (p *panickerIsland) View() h.H                 { return h.Div(h.Str("x")) }

type panicPair struct {
	A liveProbe
	B panickerIsland
}

func (p *panicPair) View() h.H { return h.Div(via.Embed(p.A), via.Embed(p.B)) }

func TestMux_childInitPanicAbortsTheConnectAndAcquiresNothing(t *testing.T) {
	t.Parallel()
	acquired := make(chan struct{})
	resp, _ := do(t, serve(t, via.Register(panicPair{A: liveProbe{acquired: acquired}})),
		http.MethodPost, "/_via/sse", "")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"an embedded child's panicking OnInit must abort the connect")
	select {
	case <-acquired:
		require.Fail(t, "a sibling's OnLive ran even though the connect never opened")
	default:
	}
}

// namer has a client Signal — two of them as sibling islands must not collide on
// the same slot name.
type namer struct{ name via.Signal[string] }

func (n *namer) View() h.H { return h.Div(n.name.Bind(), h.Span(n.name.Display())) }

type pair struct{ X, Y namer }

func (p *pair) View() h.H { return h.Div(via.Embed(p.X), via.Embed(p.Y)) }

// Sibling islands that each declare a Signal must get DISTINCT slot names —
// without a per-island prefix both would claim the same offset slot and clobber
// each other in
// the page's global Datastar store. Each island also declares its own signals on
// its container so they reach the store.
func TestEmbed_islandSignalsAreScopedPerIsland(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(pair{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "i0_f0", "island 0's signal must carry an island-scoped slot")
	assert.Contains(t, body, "i1_f0", "island 1's signal must carry an island-scoped slot")
	assert.Contains(t, body, `id="via-i0" data-signals=`, "island 0 must declare its own signals")
	assert.Contains(t, body, `id="via-i1" data-signals=`, "island 1 must declare its own signals")
}

// liveNamer is a LIVE island carrying BOTH a client Signal and ticking State —
// the chat-composer-in-multiplex case: the push must keep the Signal bound.
type liveNamer struct {
	draft via.Signal[string]
	beats via.State[int]
}

func (n *liveNamer) OnInit(ctx *via.Ctx) error { ctx.Tick(15*time.Millisecond, n.tick); return nil }
func (n *liveNamer) tick(ctx *via.Ctx)         { n.beats.Set(n.beats.Get() + 1) }
func (n *liveNamer) View() h.H {
	return h.Div(n.draft.Bind(), h.P(h.Str("beats="), n.beats.Display()))
}

type solo struct{ X liveNamer }

func (s *solo) View() h.H { return h.Div(via.Embed(s.X)) }

// A live island's signal must keep the SAME island-scoped slot across a push, or
// the client binding breaks; and the push must NOT re-declare data-signals, or a
// fan-out would clobber what the user is editing. The GET declares i0_f0 on the
// container; a tick-driven push re-binds i0_f0 with no data-signals.
func TestMux_liveIslandSignalSlotIsStableAndPushOmitsDeclaration(t *testing.T) {
	t.Parallel()
	srv := via.Register(solo{})

	// The plain GET check runs on a real listener, outside any synctest
	// bubble — it needs no fake time and this keeps the two halves of the
	// test independent.
	_, body := do(t, serve(t, srv), http.MethodGet, "/", "")
	assert.Contains(t, body, `id="via-i0" data-ignore-morph data-signals=`, "GET must declare the island's signal")
	assert.Contains(t, body, `data-bind="i0_f0"`, "the island signal uses an island-scoped slot")

	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, srv)
		conn := app.Connect()
		line := conn.Await(`data-bind="i0_f0"`) // the push re-renders the island with the same slot
		assert.NotContains(t, line, "data-signals", "a live push must not re-declare island signals")
	})
}

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

func (c *liveClicker) Bump(ctx *via.Ctx) { c.n.Set(c.n.Get() + 1) }
func (c *liveClicker) View() h.H {
	return h.Div(h.P(h.Str("c="), c.n.Display()), h.Button(via.On("click", c.Bump), h.Str("+")))
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

		// URL id 1 addresses this island; Live routes it to THIS connection's tab.
		status, _ := app.IslandAction(1, 0).Live(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status, "a live mux action acks 204; the result rides the SSE")

		conn.Await("selector #via-i0") // the action's push must target its own island container
		conn.Await("c=1")
	})
}

// A live mux action with an unknown tab must fail closed (410) so a stale client
// re-bootstraps rather than mutating a throwaway.
func TestMux_liveIslandActionWithUnknownTabIsGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(panel{}))
		conn := app.Connect() // establish the app, but use a bogus tab below

		status, _ := app.IslandAction(1, 0).Live(conn).Tab("bogus-tab-id").Fire()
		assert.Equal(t, http.StatusGone, status)
	})
}

// A live island's action binding must carry both the island id and the X-Via-Tab
// header, so the POST reaches the right island on the right connection.
func TestMux_liveIslandActionBindingCarriesTabHeader(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(panel{})), http.MethodGet, "/", "")
	assert.Regexp(t, `@post\('/_via/a/1/[A-Za-z0-9_-]+',\{headers:\{'X-Via-Tab':\$_viatab\}\}\)`, body,
		"a live island action must carry its island id and the tab header")
}

// hitsRoot is a PLAIN root (not itself live) with its own stateless action,
// embedding a LIVE island (liveClicker) — the region-ownership case: the
// root's own action patch must not repaint the island from its seed value.
type hitsRoot struct {
	hits int
	Isl  liveClicker
}

func (r *hitsRoot) Hit(ctx *via.Ctx) { r.hits++ }
func (r *hitsRoot) View() h.H {
	return h.Div(
		h.Span(h.Str("hits:"), h.Str(r.hits)),
		via.Embed(r.Isl),
		h.Button(via.On("click", r.Hit), h.Str("hit")),
	)
}

// A plain root's own action re-renders only itself (Datastar's default,
// whole-root morph) — its embedded live island's container must carry
// data-ignore-morph on every root-walk render, so that patch never repaints
// the island from its seed value.
func TestEmbed_statelessRootPatchLeavesLiveIslandRegionAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(hitsRoot{}))

		status, body := app.Get("/")
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, `id="via-i0" data-ignore-morph`,
			"a live island's container must be marked so a root patch skips it")

		status, body = app.Action(0).Fire()
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, "hits:1", "the root's own action must take effect")
		assert.Contains(t, body, `id="via-i0" data-ignore-morph`,
			"the root patch must still mark the live island's container")
	})
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
		h.Button(via.On("click", k.Bump), h.Str("+")),    // action 0
		h.Button(via.On("click", k.Noop), h.Str("noop")), // action 1
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
		`id="via-i0"`, // first island's container
		`id="via-i1"`, // second island's container
	} {
		assert.Contains(t, body, want, "embedded islands missing container/scoped-action")
	}
	// Each island's action is addressed under its OWN id, not a shared flat one.
	assert.Regexp(t, `@post\('/_via/a/1/[A-Za-z0-9_-]+'`, body)
	assert.Regexp(t, `@post\('/_via/a/2/[A-Za-z0-9_-]+'`, body)
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

// A non-existent island or action id must fail closed (410) so a stale client
// re-bootstraps rather than misrouting onto the wrong island. Only the island
// or action segment is forged; the rest of the URL is read off the rendered
// page (like TestLive_unknownActionAnswers410).
func TestEmbed_unknownIslandOrActionIsGone(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(board{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, 1, 0)

	for _, path := range []string{swapIslandIndex(t, url, "10"), swapActionID(t, url, "zzzzzzzz")} {
		resp, _ := do(t, srv, http.MethodPost, path, "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode, "out-of-range %s must be 410", path)
	}
}

// Verify the scoped action path is genuinely distinct per island (regression
// guard against a flat shared index leaking across islands).
func TestEmbed_siblingIslandsDoNotShareAnActionIndexSpace(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(board{})), http.MethodGet, "/", "")
	// Both islands address their action under their OWN island segment; neither
	// uses a page-global flat index.
	assert.True(t, strings.Contains(body, `/_via/a/1/`) && strings.Contains(body, `/_via/a/2/`),
		"each island must own a /{island}/{act} table, not a flat page index")
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

// liveShell is a NON-live layout (no OnInit) whose Body field holds a LIVE
// island. The page must bootstrap its SSE stream and the embedded live island
// must push its own container and render its server State — proving plain
// struct-field composition rides the live multiplex machinery.
type liveShell struct{ Body beater }

func (s *liveShell) View() h.H { return h.Div(h.H1(h.Str("APP")), via.Embed(s.Body)) }

func TestEmbed_projectsLiveIsland(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(liveShell{Body: beater{label: "hb"}}))
	conn := app.Connect()

	conn.Await(`selector #via-i0`) // the embedded live island pushes its own container
	conn.Await("hb=")              // and renders its own server State through the field
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

	conn.Await(`selector #via-i1`) // the depth-two live child gets its own container, distinct from its wrapper island's via-i0
	conn.Await("hb=")              // and streams its own server state independently
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

// livePage is a LIVE root (implements OnInit) whose View embeds a LIVE
// island — nested live composition, refused at render.
type livePage struct {
	Inner beater
	n     via.State[int]
}

func (p *livePage) View() h.H { return h.Div(p.n.Display(), via.Embed(p.Inner)) }

// A live root embedding a live child is nested live composition, cut from
// v0.8 (deferred alongside keyed live islands) — it must abort the render
// with a 500, not serve a page that silently misroutes an action, exactly
// like the State-off-a-live-page guard.
func TestEmbed_liveChildInsideLivePageIsRefused(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(livePage{Inner: beater{label: "hb"}}))
	status, _ := app.Get("/")
	assert.Equal(t, http.StatusInternalServerError, status,
		"embedding a live child inside a live parent must abort the render with a 500")
}

// nestedLiveIsland is itself a live island (embedded from a plain root) whose
// own View calls Embed again — the other nested-composition shape A1 cuts:
// an embedded live unit's own independent re-render never re-walks a parent,
// so it has nothing to keep a further Embed's addressing stable against.
type nestedLiveIsland struct {
	Child banner
	n     via.State[int]
}

func (n *nestedLiveIsland) View() h.H { return h.Div(n.n.Display(), via.Embed(n.Child)) }

type nestedLiveHost struct{ Inner nestedLiveIsland }

func (h2 *nestedLiveHost) View() h.H { return h.Div(via.Embed(h2.Inner)) }

// An embedded live island calling Embed inside its own View must also abort
// the render — regardless of whether the further-embedded child is itself
// live or plain.
func TestEmbed_liveIslandEmbeddingFurtherChildrenIsRefused(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(nestedLiveHost{}))
	status, _ := app.Get("/")
	assert.Equal(t, http.StatusInternalServerError, status,
		"a live island's own View calling Embed must abort the render with a 500")
}

// The guard is about LIVE children only: a live page may still embed a plain
// (stateless) child — the whole-page push re-renders it in place, which is its
// normal semantics. Fails if the guard over-reaches to all embeds.
type livePlainPage struct {
	Inner banner
	n     via.State[int]
}

func (p *livePlainPage) View() h.H {
	return h.Div(h.H1(h.Str("LIVEPAGE")), p.n.Display(), via.Embed(p.Inner))
}

func TestEmbed_allowsPlainChildInLivePage(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(livePlainPage{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "LIVEPAGE", "the live page renders")
	assert.Contains(t, body, "BANNER", "the plain child renders in place")
}

// flipChild's own action count depends on a pointer shared with its parent —
// a legitimate cross-instance dependency (Embed's own godoc: "pointer deps
// are intentionally shared"), used here purely to flip the CHILD's shape
// independently of the root between the root's GET and its later action.
type flipChild struct{ extra *bool }

func (c *flipChild) Bump(*via.Ctx)  {}
func (c *flipChild) Extra(*via.Ctx) {}
func (c *flipChild) View() h.H {
	if *c.extra {
		return h.Div(h.Button(via.On("click", c.Bump)), h.Button(via.On("click", c.Extra)))
	}
	return h.Div(h.Button(via.On("click", c.Bump)))
}

// flipRoot has its own dispatchable action (island 0) and embeds flipChild —
// the vehicle for proving the root's OWN action URL survives a child-only
// shape change.
type flipRoot struct {
	Child flipChild
	hits  int
}

func (r *flipRoot) Act(*via.Ctx) { r.hits++ }
func (r *flipRoot) View() h.H    { return h.Div(h.Button(via.On("click", r.Act)), via.Embed(r.Child)) }

// A child's shape changing between a page's GET and a later click on the
// PARENT's own button must not 410 that click. An action id addresses its
// handler, so nothing about a sibling's render can invalidate it — the A6 bug
// (a whole-page shape digest that only refreshed on the parent's own next
// push) is structurally gone.
func TestEmbed_childShapeFlipDoesNotStaleTheParentsOwnAction(t *testing.T) {
	t.Parallel()
	extra := false
	srv := serve(t, via.Register(flipRoot{Child: flipChild{extra: &extra}}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	rootAction := actionURL(t, page, 0, 0)

	extra = true // the child's OWN shape now differs from what the GET rendered

	resp, _ := do(t, srv, http.MethodPost, rootAction, "{}")
	assert.NotEqual(t, http.StatusGone, resp.StatusCode,
		"a child-only shape change must not stale the parent's own already-rendered action")
}

// embedRootChild is a plain (non-live) Embed child of a live root — enough to
// trigger the reuse lookup on the root's own first push.
type embedRootChild struct{}

func (embedRootChild) View() h.H { return h.Div(h.Str("child")) }

type liveRootWithEmbed struct {
	n     via.State[int]
	Child embedRootChild
}

func (r *liveRootWithEmbed) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Millisecond, r.tick)
	return nil
}
func (r *liveRootWithEmbed) tick(ctx *via.Ctx) { r.n.Set(r.n.Get() + 1) }
func (r *liveRootWithEmbed) View() h.H {
	return h.Div(r.n.Display(), via.Embed(r.Child))
}

// A live root's own first push must not treat itself as an already-connected
// Embed child: before the off-by-one fix, the reuse lookup used the raw
// island index (0) instead of the dispatch address (1), so a live root's
// first push resolved unit(0) to ITSELF, re-entered its own View through
// Embed, and panicked — killing the stream after the connect handshake.
func TestLive_rootEmbedSurvivesItsOwnFirstPush(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Register(liveRootWithEmbed{}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	awaitTabID(t, lines)

	awaitLine(t, lines, `id="via-i0"`)
}

// twoSpeedIsland is a live island whose Tick step is distinguishable from a
// sibling's, so which instance ended up under which container id is provable.
// It has the Noop action so a native form submit against it exists to
// trigger dispatchLive's full-page re-render.
type twoSpeedIsland struct {
	step int
	n    via.State[int]
}

func (b *twoSpeedIsland) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Millisecond, b.tick)
	return nil
}
func (b *twoSpeedIsland) tick(ctx *via.Ctx) { b.n.Set(b.n.Get() + b.step) }
func (b *twoSpeedIsland) Noop(ctx *via.Ctx) {}
func (b *twoSpeedIsland) View() h.H {
	return h.Div(h.Str(strconv.Itoa(b.n.Get())), via.PostForm(b.Noop, h.Button(h.Str("go"))))
}

type twoSpeedIslandNoAction struct {
	step int
	n    via.State[int]
}

func (b *twoSpeedIslandNoAction) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Millisecond, b.tick)
	return nil
}
func (b *twoSpeedIslandNoAction) tick(ctx *via.Ctx) { b.n.Set(b.n.Get() + b.step) }
func (b *twoSpeedIslandNoAction) View() h.H         { return h.Div(h.Str(strconv.Itoa(b.n.Get()))) }

type twoLiveIslandsRoot struct {
	A twoSpeedIsland
	B twoSpeedIslandNoAction
}

func (r *twoLiveIslandsRoot) View() h.H { return h.Div(via.Embed(r.A), via.Embed(r.B)) }

// A native <form> submit inside a live unit now answers with the page a
// fresh connection will hold (a fresh instance, seeded from the field
// literal, not the dying connection's ticked state) — the reuse path that
// used to patch the response together from the connection's own live tree
// is gone (see Embed's godoc). Both islands must still get their own
// distinct container, exactly once each, on that fresh render.
func TestPostForm_liveSubmitRendersFreshPageWithDistinctIslandIds(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Register(twoLiveIslandsRoot{A: twoSpeedIsland{step: 1}, B: twoSpeedIslandNoAction{step: 1000}}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	awaitLine(t, lines, `selector #via-i0`)
	awaitLine(t, lines, `selector #via-i1`)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, 1, 0) // island 1 = A's dispatch address (islandIdx 0 + 1)
	body, ctype := multipartForm(t, map[string]string{"_viatab": tab})
	req, err := http.NewRequest(http.MethodPost, srv.URL+url, body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	matchesA := regexp.MustCompile(`id="via-i0"`).FindAll(respBody, -1)
	matchesB := regexp.MustCompile(`id="via-i1"`).FindAll(respBody, -1)
	require.Len(t, matchesA, 1, "via-i0 must appear exactly once, not duplicated")
	require.Len(t, matchesB, 1, "via-i1 must appear exactly once, not duplicated")
}

// sessionSettingIsland is a live island whose OnInit establishes the
// session (so its cookie reaches the browser on the connect response,
// before any action fires) and whose Login action re-Puts a different value
// into that SAME session — an in-place mutation, not a fresh cookie.
type sessionSettingIsland struct{ n via.State[int] }

func (s *sessionSettingIsland) OnInit(ctx *via.Ctx) error {
	ctx.Session().Put(member{Name: "anon"})
	return nil
}
func (s *sessionSettingIsland) Login(ctx *via.Ctx) { ctx.Session().Put(member{Name: "zed"}) }
func (s *sessionSettingIsland) View() h.H {
	return h.Div(s.n.Display(), via.PostForm(s.Login, h.Button(h.Str("go"))))
}

// rootReadsSessionOnInit is a plain root whose OnInit loads the session
// value into a field — the fresh instance dispatchLive's native path now
// renders must see whatever the live island's action last Put, since it
// reads the SAME session object the action just mutated.
type rootReadsSessionOnInit struct {
	Isl  sessionSettingIsland
	name string
}

func (r *rootReadsSessionOnInit) OnInit(ctx *via.Ctx) error {
	if m, ok := ctx.Session().Get[member](); ok {
		r.name = m.Name
	}
	return nil
}
func (r *rootReadsSessionOnInit) View() h.H {
	return h.Div(h.Str(r.name), via.Embed(r.Isl))
}

// A native form submit's fresh-instance re-render runs OnInit on the POST
// goroutine, so a session write the SAME action just made (Login, above) is
// already visible to it — proving the returned page reflects post-action
// state via the session, not via the deleted live-tree reuse path.
func TestPostForm_liveSubmitRunsOnInitOnTheReturnedPage(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Register(rootReadsSessionOnInit{}))

	// liveServer's httptest.NewTestServer only routes through its own
	// srv.Client()'s transport (an in-memory network, needed for a live
	// unit's own goroutines to run inside synctest) — a plain &http.Client{}
	// would try to dial the real network instead.
	client := srv.Client()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client.Jar = jar

	lines, cancel := openStreamWithClient(t, srv, client, "/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, 1, 0) // island 1 = sessionSettingIsland's Login action

	body, ctype := multipartForm(t, map[string]string{"_viatab": tab})
	req, err := http.NewRequest(http.MethodPost, srv.URL+url, body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Contains(t, string(respBody), "zed",
		"the fresh page's OnInit must see the session value the live action just Put")
}

// initChild loads a field in its own OnInit — the hook an embedded child never
// used to get at all; only the root's ran.
type childIniter struct {
	label string
	hits  *int
}

func (c *childIniter) OnInit(ctx *via.Ctx) error {
	*c.hits++
	c.label = "from-child-init"
	return nil
}
func (c *childIniter) View() h.H { return h.Div(h.Str(c.label)) }

type initChildHost struct{ Kid childIniter }

func (p *initChildHost) View() h.H { return h.Div(h.H1(h.Str("HOST")), via.Embed(p.Kid)) }

// An embedded child is a unit like any other, so its OnInit runs before its
// own View — the data-loading hook that used to be the root's alone.
func TestEmbed_runsTheChildsOwnOnInit(t *testing.T) {
	t.Parallel()
	hits := 0
	_, body := do(t, serve(t, via.Register(initChildHost{Kid: childIniter{hits: &hits}})), http.MethodGet, "/", "")

	assert.Contains(t, body, "from-child-init", "the child's OnInit must run before its View renders")
	assert.Equal(t, 1, hits, "the child's OnInit runs exactly once per request-scoped render")
}

// notFoundChild's OnInit reports its data is gone — the honest 404 a root's
// OnInit gets, from inside a render that is already several frames deep.
type notFoundChild struct{}

func (c *notFoundChild) OnInit(ctx *via.Ctx) error { return via.ErrNotFound }
func (c *notFoundChild) View() h.H                 { return h.Div(h.Str("never")) }

type notFoundHost struct{ Kid notFoundChild }

func (p *notFoundHost) View() h.H { return h.Div(via.Embed(p.Kid)) }

func TestEmbed_childOnInitNotFoundAnswers404(t *testing.T) {
	t.Parallel()
	resp, body := do(t, serve(t, via.Register(notFoundHost{})), http.MethodGet, "/", "")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"ErrNotFound from an embedded child's OnInit is a 404, exactly as it is from the root's")
	assert.NotContains(t, body, "never", "the page must not serve once a child's OnInit failed")
}

// redirectChild's OnInit queues a Redirect — via's one per-request gate,
// usable from a child so an embedded region can guard the whole page.
type redirectChild struct{}

func (c *redirectChild) OnInit(ctx *via.Ctx) error { ctx.Redirect("/login"); return nil }
func (c *redirectChild) View() h.H                 { return h.Div(h.Str("never")) }

type redirectHost struct{ Kid redirectChild }

func (p *redirectHost) View() h.H { return h.Div(via.Embed(p.Kid)) }

func TestEmbed_childOnInitRedirectGatesThePage(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(redirectHost{}))
	resp, err := (&http.Client{CheckRedirect: noFollow}).Get(srv.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

// plainKid is a NON-live child of a LIVE root: it holds no State and never
// ticks, so its action is a stateless in-place re-render even though the page
// around it is streaming.
type plainKid struct{ hits int }

func (k *plainKid) Bump(ctx *via.Ctx) { k.hits++ }
func (k *plainKid) View() h.H {
	return h.Div(h.Str("kid-hits="), h.Str(strconv.Itoa(k.hits)), h.Button(via.On("click", k.Bump)))
}

type livePageWithPlainKid struct {
	n   via.State[int]
	Kid plainKid
}

func (p *livePageWithPlainKid) View() h.H { return h.Div(p.n.Display(), via.Embed(p.Kid)) }

// Every action now echoes the tab id, live or not — so a live page's PLAIN
// child posts one too, against an address the connection never registered.
// That must fall through to the stateless path and answer with the child's own
// patch, not 410 as an unknown live unit.
func TestDispatch_plainChildOfALivePageStaysStateless(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(livePageWithPlainKid{}))
	conn := app.Connect()
	defer conn.Close()

	status, body := app.IslandAction(1, 0).Live(conn).Fire()

	assert.Equal(t, http.StatusOK, status,
		"a plain child on a live page must dispatch statelessly, not 410 as a missing live unit")
	assert.Contains(t, body, "kid-hits=1", "its action answers with its own re-rendered container")
}
