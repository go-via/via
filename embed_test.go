package via_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
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

// beater is a LIVE embeddable embed: it ticks its own State, so each one pushes
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

// Each live embed must push its OWN container over the one shared stream: a tick
// in embed 0 patches #via-i0, a tick in embed 1 patches #via-i1 — independently,
// on a parent that is not itself a live embed.
func TestMux_eachLiveEmbedPushesItsOwnContainer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(duo{}))
		conn := app.Connect()

		conn.Await(`selector #via-i0`) // embed 0 pushed its container
		conn.Await(`selector #via-i1`) // embed 1 pushed its container, independently
	})
}

// liveProbe is an embedded LIVE embed (State alone makes it live) that
// registers an OnConnect/OnDispose pair, so a test can prove whether the pair ran
// at all. failerEmbed's OnInit errors; panickerEmbed's panics.
type liveProbe struct {
	acquired chan struct{}
	n        via.State[int]
}

func (d *liveProbe) OnInit(ctx *via.Ctx) error {
	ctx.OnConnect(d.markAcquired)
	return nil
}
func (d *liveProbe) markAcquired() { close(d.acquired) }
func (d *liveProbe) View() h.H     { return h.Div(h.Str("ok"), d.n.Display()) }

type failerEmbed struct{}

func (f *failerEmbed) OnInit(ctx *via.Ctx) error { return errors.New("init boom") }
func (f *failerEmbed) View() h.H                 { return h.Div(h.Str("x")) }

type failPair struct {
	A liveProbe
	B failerEmbed
}

func (p *failPair) View() h.H { return h.Div(via.Embed(p.A), via.Embed(p.B)) }

// An embedded child's OnInit runs inside the parent's render, before any unit
// has a stream: a failure there must abort the whole connect with a 500 and
// leave every sibling's OnConnect unrun, so no acquire is left without its
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
		require.Fail(t, "a sibling's OnConnect ran even though the connect never opened")
	default:
	}
}

// panickerEmbed's OnInit panics instead of returning an error.
type panickerEmbed struct{}

func (p *panickerEmbed) OnInit(ctx *via.Ctx) error { panic("init boom") }
func (p *panickerEmbed) View() h.H                 { return h.Div(h.Str("x")) }

type panicPair struct {
	A liveProbe
	B panickerEmbed
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
		require.Fail(t, "a sibling's OnConnect ran even though the connect never opened")
	default:
	}
}

// namer has a client Signal — two of them as sibling embeds must not collide on
// the same slot name.
type namer struct{ name via.Signal[string] }

func (n *namer) View() h.H { return h.Div(n.name.Bind(), h.Span(n.name.Display())) }

type pair struct{ X, Y namer }

func (p *pair) View() h.H { return h.Div(via.Embed(p.X), via.Embed(p.Y)) }

// Sibling embeds that each declare a Signal must get DISTINCT slot names —
// without a per-embed prefix both would claim the same offset slot and clobber
// each other in
// the page's global Datastar store. Each embed also declares its own signals on
// its container so they reach the store.
func TestEmbed_embedSignalsAreScopedPerEmbed(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(pair{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "i0__name", "embed 0's signal must carry an embed-scoped slot")
	assert.Contains(t, body, "i1__name", "embed 1's signal must carry an embed-scoped slot")
	assert.Contains(t, body, `id="via-i0" data-signals=`, "embed 0 must declare its own signals")
	assert.Contains(t, body, `id="via-i1" data-signals=`, "embed 1 must declare its own signals")
}

// liveNamer is a LIVE embed carrying BOTH a client Signal and ticking State —
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

// A live embed's signal must keep the SAME embed-scoped slot across a push, or
// the client binding breaks; and the push must NOT re-declare data-signals, or a
// fan-out would clobber what the user is editing. The GET declares x__draft on
// the container; a tick-driven push re-binds x__draft with no data-signals.
func TestMux_liveEmbedSignalSlotIsStableAndPushOmitsDeclaration(t *testing.T) {
	t.Parallel()
	srv := via.Register(solo{})

	// The plain GET check runs on a real listener, outside any synctest
	// bubble — it needs no fake time and this keeps the two halves of the
	// test independent.
	_, body := do(t, serve(t, srv), http.MethodGet, "/", "")
	assert.Contains(t, body, `id="via-i0" data-ignore-morph data-signals=`, "GET must declare the embed's signal")
	assert.Contains(t, body, `data-bind="x__draft"`, "the embed signal uses an embed-scoped slot")

	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, srv)
		conn := app.Connect()
		line := conn.Await(`data-bind="x__draft"`) // the push re-renders the embed with the same slot
		assert.NotContains(t, line, "data-signals", "a live push must not re-declare embed signals")
	})
}

// greeter renders a seeded field — the vehicle for proving an embed child can
// receive constructor data (a dep) rather than only its zero value.
type greeter struct{ who string }

func (g *greeter) View() h.H { return h.Div(h.P(h.Str("hi "), h.Str(g.who))) }

type greetPage struct{ G greeter }

func (p *greetPage) View() h.H { return h.Div(via.Embed(p.G)) }

// An embed child must be able to receive injected data beyond its zero
// value — the parent's literal seeds it by value (no '&'), and the per-connection copy
// keeps it isolated. Without seeding, a real embed (chat needing a *Room) is
// impossible.
func TestEmbed_newEmbedSeedsTheChild(t *testing.T) {
	t.Parallel()
	app := via.Register(greetPage{G: greeter{who: "alice"}})
	_, body := do(t, serve(t, app), http.MethodGet, "/", "")
	assert.Contains(t, body, "hi alice", "the parent literal must seed the embedded child")
}

// liveClicker is a LIVE embed with State + an action (dep-free, so a zero-value
// Embed works) — the vehicle for routing a live action to its own embed.
type liveClicker struct{ n via.State[int] }

func (c *liveClicker) Bump(ctx *via.Ctx) { c.n.Set(c.n.Get() + 1) }
func (c *liveClicker) View() h.H {
	return h.Div(h.P(h.Str("c="), c.n.Display()), h.Button(via.On("click", c.Bump), h.Str("+")))
}

// panel embeds two live clickers.
type panel struct{ A, B liveClicker }

func (p *panel) View() h.H { return h.Div(via.Embed(p.A), via.Embed(p.B)) }

// A live embed's action must route over the shared connection to THAT embed's
// goroutine (via the tab handshake), mutate its State, and push the result to
// its own #via-i{n} — not the sibling, not the whole page.
func TestMux_liveEmbedActionRoutesToItsEmbedAndPushes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(panel{}))
		conn := app.Connect()

		// URL id 1 addresses this embed; Live routes it to THIS connection's tab.
		status, _ := app.EmbedAction("0", 0).Over(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status, "a live mux action acks 204; the result rides the SSE")

		conn.Await("selector #via-i0") // the action's push must target its own embed container
		conn.Await("c=1")
	})
}

// A live mux action with an unknown tab must fail closed (410) so a stale client
// re-bootstraps rather than mutating a throwaway.
func TestMux_liveEmbedActionWithUnknownTabIsGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(panel{}))
		conn := app.Connect() // establish the app, but use a bogus tab below

		status, _ := app.EmbedAction("0", 0).Over(conn).Tab("bogus-tab-id").Fire()
		assert.Equal(t, http.StatusGone, status)
	})
}

// A live embed's action binding must carry the embed id. The tab id rides in
// the signal store Datastar already ships with every @post, so the binding
// itself stays bare — no per-action headers clause.
func TestMux_liveEmbedActionBindingCarriesEmbedID(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(panel{})), http.MethodGet, "/", "")
	assert.Regexp(t, `@post\('/_via/a/0/[A-Za-z0-9_-]+'\)`, body,
		"a live embed action must carry its embed id")
	assert.NotContains(t, body, "X-Via-Tab", "the tab id is a signal now, not a per-action header")
}

// hitsRoot is a PLAIN root (not itself live) with its own plain action,
// embedding a LIVE embed (liveClicker) — the region-ownership case: the
// root's own action patch must not repaint the embed from its seed value.
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
// whole-root morph) — its embedded live embed's container must carry
// data-ignore-morph on every root-walk render, so that patch never repaints
// the embed from its seed value.
func TestEmbed_plainRootPatchLeavesLiveEmbedRegionAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(hitsRoot{}))

		status, body := app.Get("/")
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, `id="via-i0" data-ignore-morph`,
			"a live embed's container must be marked so a root patch skips it")

		status, body = app.Action(0).Fire()
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, "hits:1", "the root's own action must take effect")
		assert.Contains(t, body, `id="via-i0" data-ignore-morph`,
			"the root patch must still mark the live embed's container")
	})
}

// A parent that embeds live embeds (but isn't itself a live composition) must
// bootstrap the SSE stream on its GET page, so its embeds can connect.
func TestMux_parentWithLiveEmbedsEmitsTheBootstrap(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(duo{})), http.MethodGet, "/", "")
	assert.Contains(t, body, `data-init="@post('/_via/sse')"`,
		"a parent with live embeds must bootstrap the stream")
}

// A parent whose embeds are NOT live must stay streamless — no
// bootstrap, so a purely interactive (plain-action) multi-embed page pays
// for no SSE connection.
func TestEmbed_parentWithNoLiveEmbedsOmitsTheBootstrap(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(board{})), http.MethodGet, "/", "")
	assert.NotContains(t, body, "@post('/_via/sse')",
		"plain embeds must not trigger an SSE bootstrap")
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

// board embeds two kids as sibling embeds.
type board struct{ A, B kid }

func (b *board) View() h.H { return h.Div(via.Embed(b.A), via.Embed(b.B)) }

// Each embed must render into its own positional container and wire
// its actions to an embed-scoped path, so sibling embeds stay independent and
// a patch can target exactly one of them.
func TestEmbed_rendersEachEmbedInItsOwnContainerWithScopedActions(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(board{})), http.MethodGet, "/", "")

	for _, want := range []string{
		`id="via-i0"`, // first embed's container
		`id="via-i1"`, // second embed's container
	} {
		assert.Contains(t, body, want, "embeds missing container/scoped-action")
	}
	// Each embed's action is addressed under its OWN id, not a shared flat one.
	assert.Regexp(t, `@post\('/_via/a/0/[A-Za-z0-9_-]+'`, body)
	assert.Regexp(t, `@post\('/_via/a/1/[A-Za-z0-9_-]+'`, body)
}

// An action must route to the embed named in its path, mutate that embed, and
// the response must patch that embed's container (not #root, not its sibling).
func TestEmbed_actionRoutesToItsEmbedAndPatchesThatContainer(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(board{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "1", 0), "{}") // bump the second embed (url id 2)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Contains(t, body, `id="via-i1"`, "patch must target the acted embed's container")
	assert.Contains(t, body, "n=1", "the acted embed must reflect its mutation")
	// The patch must be embed-scoped, not a whole-page re-render — otherwise
	// sibling embeds get clobbered and the multiplex point is lost.
	assert.NotContains(t, body, `id="via-i0"`, "patch must not include the sibling embed")
	assert.NotContains(t, body, `id="root"`, "patch must be the embed container, not #root")
}

// An action that changes nothing the embed's View reads returns 204, not a
// redundant patch the browser would morph onto itself — the per-embed analogue
// of the flat path's no-op contract.
func TestEmbed_actionWithNoVisibleChangeReturns204(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(board{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	resp, _ := do(t, srv, http.MethodPost, actionURL(t, page, "0", 1), "{}") // first embed (url id 1), Noop
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// A non-existent embed or action id must fail closed (410) so a stale client
// re-bootstraps rather than misrouting onto the wrong embed. Only the embed
// or action segment is forged; the rest of the URL is read off the rendered
// page (like TestLive_unknownActionAnswers410).
func TestEmbed_unknownEmbedOrActionIsGone(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(board{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, "0", 0)

	for _, path := range []string{swapEmbedIndex(t, url, "10"), swapActionID(t, url, "zzzzzzzz")} {
		resp, _ := do(t, srv, http.MethodPost, path, "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode, "out-of-range %s must be 410", path)
	}
}

// Verify the scoped action path is genuinely distinct per embed (regression
// guard against a flat shared index leaking across embeds).
func TestEmbed_siblingEmbedsDoNotShareAnActionIndexSpace(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(board{})), http.MethodGet, "/", "")
	// Both embeds address their action under their OWN embed segment; neither
	// uses a page-global flat index.
	assert.True(t, strings.Contains(body, `/_via/a/0/`) && strings.Contains(body, `/_via/a/1/`),
		"each embed must own a /{embed}/{act} table, not a flat page index")
}

// banner is a plain composition embedded by a layout.
type banner struct{}

func (b *banner) View() h.H { return h.P(h.Str("BANNER")) }

// shell is a generic layout: it renders a frame and embeds whatever composition
// its Body field holds — plain struct-field composition, no wrapper type. This
// is the content projection via.Embed exists for: one shell composes with any
// page, and the field type names the content statically.
type shell[C any] struct{ Body C }

func (s *shell[C]) View() h.H { return h.Div(h.H1(h.Str("SHELL")), via.Embed(s.Body)) }

// An embedded child renders in place, inside the layout's frame, wired as a
// embed container of its own. Fails if Embed stops wiring the container or
// renders the child outside the frame.
func TestEmbed_projectsChildInPlace(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(shell[banner]{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "SHELL", "the layout frame renders")
	assert.Contains(t, body, "BANNER", "the embedded content renders inside the frame")
	assert.Contains(t, body, `id="via-i0"`, "embedded content is wired into an embed container keyed by its ordinal")
	assert.Less(t, strings.Index(body, "SHELL"), strings.Index(body, "BANNER"),
		"content is embedded in place, after the frame heading")
}

// liveShell is a PLAIN layout (no OnInit) whose Body field holds a LIVE
// embed. The page must bootstrap its SSE stream and the embedded live embed
// must push its own container and render its server State — proving plain
// struct-field composition rides the live multiplex machinery.
type liveShell struct{ Body beater }

func (s *liveShell) View() h.H { return h.Div(h.H1(h.Str("APP")), via.Embed(s.Body)) }

func TestEmbed_projectsLiveEmbed(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(liveShell{Body: beater{label: "hb"}}))
	conn := app.Connect()

	conn.Await(`selector #via-i0`) // the embedded live embed pushes its own container
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

// nestHost is a PLAIN embed (itself embedded by a page) whose own View
// embeds a LIVE child — the two-deep composition B1 makes work.
type nestHost struct{ Inner beater }

func (n *nestHost) View() h.H { return h.Div(via.Embed(n.Inner)) }

type nestPage struct{ Host nestHost }

func (p *nestPage) View() h.H { return h.Div(via.Embed(p.Host)) }

// A live embed nested two levels deep (a plain page embeds a plain embed
// that embeds a live child) must still get discovered, wired, and streamed —
// embed discovery walks the embedded tree at any depth, not just the page's
// direct children.
func TestEmbed_liveChildInsideLiveEmbedAtDepthTwo(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(nestPage{Host: nestHost{Inner: beater{label: "hb"}}}))
	conn := app.Connect()

	conn.Await(`selector #via-i0-0`) // the depth-two live child's key composes onto its wrapper embed's ("0"), so it can never alias it
	conn.Await("hb=")                // and streams its own server state independently
}

// The guard is about LIVE children only: a plain child embedded
// inside an embed still renders in place. Fails if the guard over-reaches to
// all nesting.
type plainNestHost struct{ Inner banner }

func (n *plainNestHost) View() h.H { return h.Div(h.H2(h.Str("HOST")), via.Embed(n.Inner)) }

type plainNestPage struct{ Host plainNestHost }

func (p *plainNestPage) View() h.H { return h.Div(via.Embed(p.Host)) }

func TestEmbed_allowsNestedPlainChild(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(plainNestPage{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "HOST", "the embed renders")
	assert.Contains(t, body, "BANNER", "the nested plain child renders in place")
}

// livePage is a LIVE root (implements OnInit) whose View embeds a LIVE
// embed — nested live composition, refused at render.
type livePage struct {
	Inner beater
	n     via.State[int]
}

func (p *livePage) View() h.H { return h.Div(p.n.Display(), via.Embed(p.Inner)) }

// A live root embedding a live child is nested live composition, cut from
// v0.8 (deferred alongside keyed live embeds) — it must abort the render
// with a 500, not serve a page that silently misroutes an action, exactly
// like the State-off-a-live-page guard.
func TestEmbed_liveChildInsideLivePageIsRefused(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(livePage{Inner: beater{label: "hb"}}))
	status, _ := app.Get("/")
	assert.Equal(t, http.StatusInternalServerError, status,
		"embedding a live child inside a live parent must abort the render with a 500")
}

// nestedLiveEmbed is itself a live embed (embedded from a plain root) whose
// own View calls Embed again — the other nested-composition shape A1 cuts:
// an embedded live unit's own independent re-render never re-walks a parent,
// so it has nothing to keep a further Embed's addressing stable against.
type nestedLiveEmbed struct {
	Child banner
	n     via.State[int]
}

func (n *nestedLiveEmbed) View() h.H { return h.Div(n.n.Display(), via.Embed(n.Child)) }

type nestedLiveHost struct{ Inner nestedLiveEmbed }

func (h2 *nestedLiveHost) View() h.H { return h.Div(via.Embed(h2.Inner)) }

// An embedded live embed calling Embed inside its own View must also abort
// the render — regardless of whether the further-embedded child is itself
// live or plain.
func TestEmbed_liveEmbedEmbeddingFurtherChildrenIsRefused(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(nestedLiveHost{}))
	status, _ := app.Get("/")
	assert.Equal(t, http.StatusInternalServerError, status,
		"a live embed's own View calling Embed must abort the render with a 500")
}

// The guard is about LIVE children only: a streaming page may still embed a plain
// child — the whole-page push re-renders it in place, which is its
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

	assert.Contains(t, body, "LIVEPAGE", "the streaming page renders")
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

// flipRoot has its own dispatchable action (embed 0) and embeds flipChild —
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
	rootAction := actionURL(t, page, "r", 0)

	extra = true // the child's OWN shape now differs from what the GET rendered

	resp, _ := do(t, srv, http.MethodPost, rootAction, "{}")
	assert.NotEqual(t, http.StatusGone, resp.StatusCode,
		"a child-only shape change must not stale the parent's own already-rendered action")
}

// embedRootChild is a plain (plain) Embed child of a live root — enough to
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
// embed index (0) instead of the dispatch address (1), so a live root's
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

// twoSpeedEmbed is a live embed whose Tick step is distinguishable from a
// sibling's, so which instance ended up under which container id is provable.
// It has the Noop action so a native form submit against it exists to
// trigger dispatchOverStream's full-page re-render.
type twoSpeedEmbed struct {
	step int
	n    via.State[int]
}

func (b *twoSpeedEmbed) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Millisecond, b.tick)
	return nil
}
func (b *twoSpeedEmbed) tick(ctx *via.Ctx) { b.n.Set(b.n.Get() + b.step) }
func (b *twoSpeedEmbed) Noop(ctx *via.Ctx) {}
func (b *twoSpeedEmbed) View() h.H {
	return h.Div(h.Str(strconv.Itoa(b.n.Get())), via.PostForm(b.Noop, h.Button(h.Str("go"))))
}

type twoSpeedEmbedNoAction struct {
	step int
	n    via.State[int]
}

func (b *twoSpeedEmbedNoAction) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Millisecond, b.tick)
	return nil
}
func (b *twoSpeedEmbedNoAction) tick(ctx *via.Ctx) { b.n.Set(b.n.Get() + b.step) }
func (b *twoSpeedEmbedNoAction) View() h.H         { return h.Div(h.Str(strconv.Itoa(b.n.Get()))) }

type twoLiveEmbedsRoot struct {
	A twoSpeedEmbed
	B twoSpeedEmbedNoAction
}

func (r *twoLiveEmbedsRoot) View() h.H { return h.Div(via.Embed(r.A), via.Embed(r.B)) }

// A native <form> submit inside a live unit now answers with the page a
// fresh connection will hold (a fresh instance, seeded from the field
// literal, not the dying connection's ticked state) — the reuse path that
// used to patch the response together from the connection's own live tree
// is gone (see Embed's godoc). Both embeds must still get their own
// distinct container, exactly once each, on that fresh render.
func TestPostForm_liveSubmitRendersFreshPageWithDistinctEmbedIds(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Register(twoLiveEmbedsRoot{A: twoSpeedEmbed{step: 1}, B: twoSpeedEmbedNoAction{step: 1000}}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	awaitLine(t, lines, `selector #via-i0`)
	awaitLine(t, lines, `selector #via-i1`)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, "0", 0) // "0" = A's key: the root's first Embed
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

// sessionSettingEmbed is a live embed whose OnInit establishes the
// session (so its cookie reaches the browser on the connect response,
// before any action fires) and whose Login action re-Puts a different value
// into that SAME session — an in-place mutation, not a fresh cookie.
type sessionSettingEmbed struct{ n via.State[int] }

func (s *sessionSettingEmbed) OnInit(ctx *via.Ctx) error {
	ctx.Session().Put(member{Name: "anon"})
	return nil
}
func (s *sessionSettingEmbed) Login(ctx *via.Ctx) { ctx.Session().Put(member{Name: "zed"}) }
func (s *sessionSettingEmbed) View() h.H {
	return h.Div(s.n.Display(), via.PostForm(s.Login, h.Button(h.Str("go"))))
}

// rootReadsSessionOnInit is a plain root whose OnInit loads the session
// value into a field — the fresh instance dispatchOverStream's native path now
// renders must see whatever the live embed's action last Put, since it
// reads the SAME session object the action just mutated.
type rootReadsSessionOnInit struct {
	Isl  sessionSettingEmbed
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
	url := actionURL(t, page, "0", 0) // embed 1 = sessionSettingEmbed's Login action

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

// plainKid is a PLAIN child of a LIVE root: it holds no State and never
// ticks, so its action is a plain in-place re-render even though the page
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

// Every action now echoes the tab id, live or not — so a streaming page's PLAIN
// child posts one too, against an address the connection never registered.
// That must fall through to the plain path and answer with the child's own
// patch, not 410 as an unknown live unit.
func TestDispatch_plainChildOfALivePageStaysPlain(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(livePageWithPlainKid{}))
	conn := app.Connect()
	defer conn.Close()

	status, body := app.EmbedAction("0", 0).Over(conn).Fire()

	assert.Equal(t, http.StatusOK, status,
		"a plain child on a streaming page must dispatch on the plain path, not 410 as a missing live unit")
	assert.Contains(t, body, "kid-hits=1", "its action answers with its own re-rendered container")
}

// deepCounter is a LIVE leaf whose Signal sits at field offset 0 — the same
// offset deepPanel.Query occupies, so only the embed key keeps the two slots
// apart on the wire.
type deepCounter struct {
	Step via.Signal[int]
	n    via.State[int]
}

func (c *deepCounter) Inc(*via.Ctx) { c.n.Set(c.n.Get() + c.Step.Get()) }
func (c *deepCounter) View() h.H {
	return h.Div(h.Str("n="), c.n.Display(),
		h.Input(c.Step.Bind()), h.Button(via.On("click", c.Inc), h.Str("+step")))
}

// deepPanel is a PLAIN middle embed: it has its own Signal and action, and its
// View Embeds a live child — the shape Embed's godoc once assumed impossible.
type deepPanel struct {
	Query via.Signal[string]
	Kid   deepCounter
	hits  *atomic.Int64
}

func (p *deepPanel) Search(*via.Ctx) { p.hits.Add(1) }
func (p *deepPanel) View() h.H {
	return h.Section(h.Input(p.Query.Bind()),
		h.Button(via.On("click", p.Search), h.Str("search")),
		h.Span(h.Str("hits="), h.Str(int(p.hits.Load()))), via.Embed(p.Kid))
}

type deepRoot struct {
	Filter      via.Signal[string]
	Left, Right deepPanel
}

func (d *deepRoot) View() h.H {
	return h.Main(h.Input(d.Filter.Bind()), via.Embed(d.Left), via.Embed(d.Right))
}

func newDeepRoot(hits *atomic.Int64) deepRoot {
	return deepRoot{Left: deepPanel{hits: hits}, Right: deepPanel{hits: hits}}
}

// An embed's key composes onto its parent's, so two Embeds of the SAME type
// side by side — each holding a same-typed child of its own — get four
// distinct container ids and four distinct signal prefixes. Under the old
// page-wide counter this held only because one flat walk numbered everything;
// the key makes it a property of the tree instead.
func TestEmbed_siblingsOfTheSameTypeGetDistinctKeys(t *testing.T) {
	t.Parallel()
	_, page := do(t, serve(t, via.Register(newDeepRoot(&atomic.Int64{}))), http.MethodGet, "/", "")

	for _, id := range []string{`id="via-i0"`, `id="via-i0-0"`, `id="via-i1"`, `id="via-i1-0"`} {
		assert.Contains(t, page, id, "every embed in the tree needs its own container")
	}
	slots := regexp.MustCompile(`data-bind="([^"]+)"`).FindAllStringSubmatch(page, -1)
	seen := map[string]bool{}
	for _, m := range slots {
		assert.False(t, seen[m[1]], "signal slot %s is bound by two different signals", m[1])
		seen[m[1]] = true
	}
	assert.Len(t, seen, 5, "root filter + two panel queries + two counter steps")
}

// A PLAIN embed's action re-renders that embed alone — and its View Embeds a
// live child, so the re-render must number that child off the embed's own key
// rather than restarting at the root's. Before this, the nested child came back
// as a DUPLICATE of its parent's container id, with a signal prefix that
// aliased the parent's own slot and a dispatch address that 410'd on click.
func TestEmbed_plainEmbedActionKeepsItsNestedLiveChildAddressable(t *testing.T) {
	t.Parallel()
	hits := &atomic.Int64{}
	app := vt.Serve(t, via.Register(newDeepRoot(hits)))
	conn := app.Connect()
	defer conn.Close()

	status, patch := app.EmbedAction("0", 0).Over(conn).Fire()
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, int64(1), hits.Load(), "the left panel's own action ran")

	assert.Len(t, regexp.MustCompile(`id="via-i0"`).FindAllString(patch, -1), 1,
		"the patch must carry the acted embed's container exactly once")
	assert.Contains(t, patch, `id="via-i0-0"`,
		"the nested live child keeps its composed key in a partial re-render")
	assert.Contains(t, patch, `data-bind="i0__kid__step"`,
		"and its signal prefix stays distinct from its parent's i0__")
	assert.Len(t, regexp.MustCompile(`data-bind="i0__query"`).FindAllString(patch, -1), 1,
		"the parent's own Query slot must not be aliased by the nested child's Step")

	childURL := actionURL(t, patch, "0-0", 0)
	assert.Contains(t, childURL, "/_via/a/0-0/",
		"the child's button is addressed by its composed key")
	code, _ := app.Action(0).Raw(childURL).Over(conn).Body(`{"i0__kid__step":5}`).Fire()
	assert.Equal(t, http.StatusNoContent, code,
		"the URL the parent's patch handed the browser must still reach the live child")
	assert.Contains(t, conn.Await("n="), "5", "and drive it")
}

// threeDeepLeafHost/Mid nest a live leaf under TWO plain embeds, so its key is
// a three-segment path — the depth at which a single composition step is no
// longer enough to keep the numbering consistent.
type threeDeepMid struct{ Kid deepCounter }

func (m *threeDeepMid) View() h.H { return h.Div(via.Embed(m.Kid)) }

type threeDeepOuter struct{ Mid threeDeepMid }

func (o *threeDeepOuter) View() h.H { return h.Div(via.Embed(o.Mid)) }

type threeDeepPage struct{ Outer threeDeepOuter }

func (p *threeDeepPage) View() h.H { return h.Div(via.Embed(p.Outer)) }

func TestEmbed_liveLeafUnderTwoPlainEmbedsKeepsItsPathKey(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(threeDeepPage{}))
	conn := app.Connect()
	defer conn.Close()

	_, page := app.Get("/")
	assert.Contains(t, page, `id="via-i0-0-0"`, "the leaf's key is the path of ordinals down to it")
	assert.Contains(t, page, `data-bind="outer__mid__kid__step"`)

	status, _ := app.EmbedAction("0-0-0", 0).Over(conn).Body(`{"outer__mid__kid__step":3}`).Fire()
	assert.Equal(t, http.StatusNoContent, status, "the leaf dispatches at its own path address")
	assert.Contains(t, conn.Await("n="), "3")
}

// --- nested OnInit on an action re-render (change 2) ---

// initKid loads its own data in OnInit. A re-render that skips it renders the
// zero value, which is nothing like what the same subtree shows on a GET.
type initKid struct{ n int }

func (k *initKid) OnInit(ctx *via.Ctx) error { k.n = 7; return nil }
func (k *initKid) View() h.H                 { return h.P(h.Str("kid="), h.Str(strconv.Itoa(k.n))) }

type initRoot struct {
	Kid  initKid
	hits int
}

func (p *initRoot) Bump(ctx *via.Ctx) { p.hits++ }
func (p *initRoot) View() h.H {
	return h.Div(h.Str("hits="), h.Str(strconv.Itoa(p.hits)),
		h.Button(via.On("click", p.Bump)), via.Embed(p.Kid))
}

// A plain ROOT action re-renders the whole page for its patch. The root's own
// OnInit already ran for this request, but every embedded child in the patch is
// a fresh copy whose OnInit has not — so it must run here or the patch ships a
// blank child over a populated one.
func TestEmbed_rootActionPatchInitsNestedChildren(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(initRoot{}))
	_, page := app.Get("/")
	require.Contains(t, page, "kid=7", "the GET paints the inited child")

	status, patch := app.Action(0).Fire()
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, patch, "kid=7",
		"the action patch must re-init the nested child, not repaint it zero-valued")
}

type initMid struct {
	Kid  initKid
	hits int
}

func (m *initMid) Note(ctx *via.Ctx) { m.hits++ }
func (m *initMid) View() h.H {
	return h.Div(h.Str("hits="), h.Str(strconv.Itoa(m.hits)),
		h.Button(via.On("click", m.Note)), via.Embed(m.Kid))
}

type initHost struct{ Mid initMid }

func (h2 *initHost) View() h.H { return h.Div(via.Embed(h2.Mid)) }

// Same rule one level down: a PLAIN embed's own action re-renders just that
// embed, and its nested children are fresh copies that have never been inited.
func TestEmbed_embedActionPatchInitsNestedChildren(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(initHost{}))
	_, page := app.Get("/")
	require.Contains(t, page, "kid=7")

	status, patch := app.EmbedAction("0", 0).Fire()
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, patch, "kid=7",
		"the embed patch must re-init its own nested children")
}

// twinStats is embedded twice by the same parent, which is what forces the
// positional fallback slot prefix (two fields of one type: the field name
// would be a guess).
type twinStats struct{ Q via.Signal[string] }

func (s *twinStats) View() h.H {
	return h.Div(h.Input(s.Q.Bind()), h.Span(h.Data("show", s.Q.Ref()+" != ''")))
}

type twinShell struct{ Inner twinPage }
type twinPage struct{ Top, Foot twinStats }

func (p *twinPage) View() h.H  { return h.Div(via.Embed(p.Top), via.Embed(p.Foot)) }
func (s *twinShell) View() h.H { return h.Main(via.Embed(s.Inner)) }

// The fallback prefix is built from the embed KEY, whose depth separator is
// '-' — not a JS identifier character. Ref() hands these names straight into
// Datastar expressions ($body__i0-0__q), where the '-' reads as subtraction,
// so the slot name must spell the key with '_'. The key itself is unchanged:
// the container id and the dispatch address still use '-'.
func TestEmbed_fallbackSlotNamesAreValidJSIdentifiers(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(twinShell{}))
	_, page := app.Get("/")

	binds := regexp.MustCompile(`data-bind="([^"]+)"`).FindAllStringSubmatch(page, -1)
	require.Len(t, binds, 2, "both twins must bind their own slot")
	seen := map[string]bool{}
	for _, m := range binds {
		assert.NotContainsf(t, m[1], "-", "slot %q is not a valid JS identifier", m[1])
		assert.Falsef(t, seen[m[1]], "the two twins share slot %q", m[1])
		seen[m[1]] = true
		assert.Containsf(t, page, `data-show="$`+m[1]+` != `, "Ref() must name the same slot Bind() declared")
	}
	assert.Contains(t, page, `id="via-i0-0"`, "the embed KEY keeps its '-' separator")
	assert.Contains(t, seen, "inner__i0_0__q", "the fallback spells the key with underscores")
}
