package via_test

import (
	"context"
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

// beater is a live embeddable child: it ticks its own State, so each one pushes
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

// duo children two live beaters; it does not itself implement OnInit — it is a
// multiplex parent whose live children share one SSE stream.
type duo struct{ A, B beater }

func (d *duo) View() h.H { return h.Div(via.Child(d.A), via.Child(d.B)) }

func TestMux_eachLiveChildPushesItsOwnContainer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(duo{}))
		conn := app.Connect()

		conn.Await(`selector #via-i0`) // child 0 pushed its container
		conn.Await(`selector #via-i1`) // child 1 pushed its container, independently
	})
}

// liveProbe is an embedded live child (State alone makes it live) that
// registers an OnConnect/OnDispose pair, so a test can prove whether the pair ran
// at all. failerChild's OnInit errors; panickerChild's panics.
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

type failerChild struct{}

func (f *failerChild) OnInit(ctx *via.Ctx) error { return errors.New("init boom") }
func (f *failerChild) View() h.H                 { return h.Div(h.Str("x")) }

type failPair struct {
	A liveProbe
	B failerChild
}

func (p *failPair) View() h.H { return h.Div(via.Child(p.A), via.Child(p.B)) }

func TestMux_childInitErrorAbortsTheConnectAndAcquiresNothing(t *testing.T) {
	t.Parallel()
	acquired := make(chan struct{})
	resp, _ := do(t, serve(t, via.Handler(failPair{A: liveProbe{acquired: acquired}})),
		http.MethodPost, "/_via/sse", "")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"an embedded child's failed OnInit must abort the connect")
	select {
	case <-acquired:
		require.Fail(t, "a sibling's OnConnect ran even though the connect never opened")
	default:
	}
}

// panickerChild's OnInit panics instead of returning an error.
type panickerChild struct{}

func (p *panickerChild) OnInit(ctx *via.Ctx) error { panic("init boom") }
func (p *panickerChild) View() h.H                 { return h.Div(h.Str("x")) }

type panicPair struct {
	A liveProbe
	B panickerChild
}

func (p *panicPair) View() h.H { return h.Div(via.Child(p.A), via.Child(p.B)) }

func TestMux_childInitPanicAbortsTheConnectAndAcquiresNothing(t *testing.T) {
	t.Parallel()
	acquired := make(chan struct{})
	resp, _ := do(t, serve(t, via.Handler(panicPair{A: liveProbe{acquired: acquired}})),
		http.MethodPost, "/_via/sse", "")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"an embedded child's panicking OnInit must abort the connect")
	select {
	case <-acquired:
		require.Fail(t, "a sibling's OnConnect ran even though the connect never opened")
	default:
	}
}

// namer has a client Signal — two of them as sibling children must not collide on
// the same slot name.
type namer struct{ name via.Signal[string] }

func (n *namer) View() h.H { return h.Div(n.name.Bind(), h.Span(n.name.Display())) }

type pair struct{ X, Y namer }

func (p *pair) View() h.H { return h.Div(via.Child(p.X), via.Child(p.Y)) }

func TestChild_childSignalsAreScopedPerChild(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(pair{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "i0__name", "child 0's signal must carry a child-scoped slot")
	assert.Contains(t, body, "i1__name", "child 1's signal must carry a child-scoped slot")
	assert.Contains(t, body, `id="via-i0" data-signals=`, "child 0 must declare its own signals")
	assert.Contains(t, body, `id="via-i1" data-signals=`, "child 1 must declare its own signals")
}

// liveNamer is a live child carrying both a client Signal and ticking State —
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

func (s *solo) View() h.H { return h.Div(via.Child(s.X)) }

func TestMux_liveChildSignalSlotIsStableAndPushOmitsDeclaration(t *testing.T) {
	t.Parallel()
	srv := via.Handler(solo{})

	// The plain GET check runs on a real listener, outside any synctest
	// bubble — it needs no fake time and this keeps the two halves of the
	// test independent.
	_, body := do(t, serve(t, srv), http.MethodGet, "/", "")
	assert.Contains(t, body, `id="via-i0" data-ignore-morph data-signals=`, "GET must declare the child's signal")
	assert.Contains(t, body, `data-bind="x__draft"`, "the child signal uses a child-scoped slot")

	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, srv)
		conn := app.Connect()
		line := conn.Await(`data-bind="x__draft"`) // the push re-renders the child with the same slot
		assert.NotContains(t, line, "data-signals", "a live push must not re-declare child signals")
	})
}

// greeter renders a seeded field — the vehicle for proving a child can
// receive constructor data (a dep) rather than only its zero value.
type greeter struct{ who string }

func (g *greeter) View() h.H { return h.Div(h.P(h.Str("hi "), h.Str(g.who))) }

type greetPage struct{ G greeter }

func (p *greetPage) View() h.H { return h.Div(via.Child(p.G)) }

func TestChild_newChildSeedsTheChild(t *testing.T) {
	t.Parallel()
	app := via.Handler(greetPage{G: greeter{who: "alice"}})
	_, body := do(t, serve(t, app), http.MethodGet, "/", "")
	assert.Contains(t, body, "hi alice", "the parent literal must seed the embedded child")
}

// liveClicker is a live child with State + an action (dep-free, so a zero-value
// Child works) — the vehicle for routing a live action to its own child.
type liveClicker struct{ n via.State[int] }

func (c *liveClicker) Bump(ctx *via.Ctx) { c.n.Set(c.n.Get() + 1) }
func (c *liveClicker) View() h.H {
	return h.Div(h.P(h.Str("c="), c.n.Display()), h.Button(via.On("click", c.Bump), h.Str("+")))
}

// panel children two live clickers.
type panel struct{ A, B liveClicker }

func (p *panel) View() h.H { return h.Div(via.Child(p.A), via.Child(p.B)) }

func TestMux_liveChildActionRoutesToItsChildAndPushes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(panel{}))
		conn := app.Connect()

		// URL id 1 addresses this child; Live routes it to this connection's tab.
		status, _ := app.ChildAction("0", 0).Over(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status, "a live mux action acks 204; the result rides the SSE")

		conn.Await("selector #via-i0") // the action's push must target its own child container
		conn.Await("c=1")
	})
}

func TestMux_liveChildActionWithUnknownTabIsGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(panel{}))
		conn := app.Connect() // establish the app, but use a bogus tab below

		status, _ := app.ChildAction("0", 0).Over(conn).Tab("bogus-tab-id").Fire()
		assert.Equal(t, http.StatusGone, status)
	})
}

func TestMux_liveChildActionBindingCarriesChildID(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(panel{})), http.MethodGet, "/", "")
	assert.Regexp(t, `@post\('/_via/a/0/[A-Za-z0-9_-]+'\)`, body,
		"a live child action must carry its child id")
	assert.NotContains(t, body, "X-Via-Tab", "the tab id is a signal now, not a per-action header")
}

// hitsRoot is a plain root (not itself live) with its own plain action,
// embedding a live child (liveClicker) — the region-ownership case: the
// root's own action patch must not repaint the child from its seed value.
type hitsRoot struct {
	hits int
	Isl  liveClicker
}

func (r *hitsRoot) Hit(ctx *via.Ctx) { r.hits++ }
func (r *hitsRoot) View() h.H {
	return h.Div(
		h.Span(h.Str("hits:"), h.Str(r.hits)),
		via.Child(r.Isl),
		h.Button(via.On("click", r.Hit), h.Str("hit")),
	)
}

func TestChild_plainRootPatchLeavesLiveChildRegionAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(hitsRoot{}))

		status, body := app.Get("/")
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, `id="via-i0" data-ignore-morph`,
			"a live child's container must be marked so a root patch skips it")

		status, body = app.Action(0).Fire()
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, "hits:1", "the root's own action must take effect")
		assert.Contains(t, body, `id="via-i0" data-ignore-morph`,
			"the root patch must still mark the live child's container")
	})
}

func TestMux_parentWithLiveChildsEmitsTheBootstrap(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(duo{})), http.MethodGet, "/", "")
	assert.Contains(t, body, `data-init="@post('/_via/sse')"`,
		"a parent with live children must bootstrap the stream")
}

func TestChild_parentWithNoLiveChildsOmitsTheBootstrap(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(board{})), http.MethodGet, "/", "")
	assert.NotContains(t, body, "@post('/_via/sse')",
		"plain children must not trigger an SSE bootstrap")
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

// board children two kids as sibling children.
type board struct{ A, B kid }

func (b *board) View() h.H { return h.Div(via.Child(b.A), via.Child(b.B)) }

func TestChild_rendersEachChildInItsOwnContainerWithScopedActions(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(board{})), http.MethodGet, "/", "")

	for _, want := range []string{
		`id="via-i0"`, // first child's container
		`id="via-i1"`, // second child's container
	} {
		assert.Contains(t, body, want, "children missing container/scoped-action")
	}
	assert.Regexp(t, `@post\('/_via/a/0/[A-Za-z0-9_-]+'`, body)
	assert.Regexp(t, `@post\('/_via/a/1/[A-Za-z0-9_-]+'`, body)
}

func TestChild_actionRoutesToItsChildAndPatchesThatContainer(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(board{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "1", 0), "{}") // bump the second child (url id 2)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Contains(t, body, `id="via-i1"`, "patch must target the acted child's container")
	assert.Contains(t, body, "n=1", "the acted child must reflect its mutation")
	assert.NotContains(t, body, `id="via-i0"`, "patch must not include the sibling child")
	assert.NotContains(t, body, `id="root"`, "patch must be the child container, not #root")
}

func TestChild_actionWithNoVisibleChangeReturns204(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(board{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	resp, _ := do(t, srv, http.MethodPost, actionURL(t, page, "0", 1), "{}") // first child (url id 1), Noop
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestChild_unknownChildOrActionIsGone(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(board{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, "0", 0)

	for _, path := range []string{swapChildIndex(t, url, "10"), swapActionID(t, url, "zzzzzzzz")} {
		resp, _ := do(t, srv, http.MethodPost, path, "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode, "out-of-range %s must be 410", path)
	}
}

func TestChild_siblingChildsDoNotShareAnActionIndexSpace(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(board{})), http.MethodGet, "/", "")
	assert.True(t, strings.Contains(body, `/_via/a/0/`) && strings.Contains(body, `/_via/a/1/`),
		"each child must own a /{child}/{act} table, not a flat page index")
}

// banner is a plain composition embedded by a layout.
type banner struct{}

func (b *banner) View() h.H { return h.P(h.Str("BANNER")) }

// shell is a generic layout: it renders a frame and children whatever composition
// its Body field holds — plain struct-field composition, no wrapper type. This
// is the content projection via.Child exists for: one shell composes with any
// page, and the field type names the content statically.
type shell[C any] struct{ Body C }

func (s *shell[C]) View() h.H { return h.Div(h.H1(h.Str("SHELL")), via.Child(s.Body)) }

func TestChild_projectsChildInPlace(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(shell[banner]{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "SHELL", "the layout frame renders")
	assert.Contains(t, body, "BANNER", "the embedded content renders inside the frame")
	assert.Contains(t, body, `id="via-i0"`, "embedded content is wired into a child container keyed by its ordinal")
	assert.Less(t, strings.Index(body, "SHELL"), strings.Index(body, "BANNER"),
		"content is embedded in place, after the frame heading")
}

// liveShell is a plain layout (no OnInit) whose Body field holds a live
// child. The page must bootstrap its SSE stream and the embedded live child
// must push its own container and render its server State — proving plain
// struct-field composition rides the live multiplex machinery.
type liveShell struct{ Body beater }

func (s *liveShell) View() h.H { return h.Div(h.H1(h.Str("APP")), via.Child(s.Body)) }

func TestChild_projectsLiveChild(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(liveShell{Body: beater{label: "hb"}}))
	conn := app.Connect()

	conn.Await(`selector #via-i0`) // the embedded live child pushes its own container
	conn.Await("hb=")              // and renders its own server State through the field
}

func TestChild_panicsWithoutView(t *testing.T) {
	t.Parallel()
	require.PanicsWithValue(t,
		"via: via.Child(child) requires child to have a View() method",
		func() { via.Child(struct{ X int }{}) },
	)
}

// nestHost is a plain child (itself embedded by a page) whose own View
// embeds a live child — the two-deep composition B1 makes work.
type nestHost struct{ Inner beater }

func (n *nestHost) View() h.H { return h.Div(via.Child(n.Inner)) }

type nestPage struct{ Host nestHost }

func (p *nestPage) View() h.H { return h.Div(via.Child(p.Host)) }

func TestChild_liveChildInsideLiveChildAtDepthTwo(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(nestPage{Host: nestHost{Inner: beater{label: "hb"}}}))
	conn := app.Connect()

	conn.Await(`selector #via-i0-0`) // the depth-two live child's key composes onto its wrapper child's ("0"), so it can never alias it
	conn.Await("hb=")                // and streams its own server state independently
}

// The guard is about live children only: a plain child embedded
// inside a child still renders in place. Fails if the guard over-reaches to
// all nesting.
type plainNestHost struct{ Inner banner }

func (n *plainNestHost) View() h.H { return h.Div(h.H2(h.Str("HOST")), via.Child(n.Inner)) }

type plainNestPage struct{ Host plainNestHost }

func (p *plainNestPage) View() h.H { return h.Div(via.Child(p.Host)) }

func TestChild_allowsNestedPlainChild(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(plainNestPage{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "HOST", "the child renders")
	assert.Contains(t, body, "BANNER", "the nested plain child renders in place")
}

// livePage is a live root (implements OnInit) whose View embeds a live
// child — nested live composition, refused at render.
type livePage struct {
	Inner beater
	n     via.State[int]
}

func (p *livePage) View() h.H { return h.Div(p.n.Display(), via.Child(p.Inner)) }

func TestChild_liveChildInsideLivePageIsRefused(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(livePage{Inner: beater{label: "hb"}}))
	status, _ := app.Get("/")
	assert.Equal(t, http.StatusInternalServerError, status,
		"embedding a live child inside a live parent must abort the render with a 500")
}

// nestedLiveChild is itself a live child (embedded from a plain root) whose
// own View calls Child again — the other nested-composition shape A1 cuts:
// an embedded live unit's own independent re-render never re-walks a parent,
// so it has nothing to keep a further Child's addressing stable against.
type nestedLiveChild struct {
	Child banner
	n     via.State[int]
}

func (n *nestedLiveChild) View() h.H { return h.Div(n.n.Display(), via.Child(n.Child)) }

type nestedLiveHost struct{ Inner nestedLiveChild }

func (h2 *nestedLiveHost) View() h.H { return h.Div(via.Child(h2.Inner)) }

func TestChild_liveChildChilddingFurtherChildrenIsRefused(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(nestedLiveHost{}))
	status, _ := app.Get("/")
	assert.Equal(t, http.StatusInternalServerError, status,
		"a live child's own View calling Child must abort the render with a 500")
}

// The guard is about live children only: a streaming page may still embed a plain
// child — the whole-page push re-renders it in place, which is its
// normal semantics. Fails if the guard over-reaches to all children.
type livePlainPage struct {
	Inner banner
	n     via.State[int]
}

func (p *livePlainPage) View() h.H {
	return h.Div(h.H1(h.Str("LIVEPAGE")), p.n.Display(), via.Child(p.Inner))
}

func TestChild_allowsPlainChildInLivePage(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(livePlainPage{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "LIVEPAGE", "the streaming page renders")
	assert.Contains(t, body, "BANNER", "the plain child renders in place")
}

// flipChild's own action count depends on a pointer shared with its parent —
// a legitimate cross-instance dependency (Child's own godoc: "pointer deps
// are intentionally shared"), used here purely to flip the child's shape
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

// flipRoot has its own dispatchable action (child 0) and children flipChild —
// the vehicle for proving the root's own action URL survives a child-only
// shape change.
type flipRoot struct {
	Child flipChild
	hits  int
}

func (r *flipRoot) Act(*via.Ctx) { r.hits++ }
func (r *flipRoot) View() h.H    { return h.Div(h.Button(via.On("click", r.Act)), via.Child(r.Child)) }

func TestChild_childShapeFlipDoesNotStaleTheParentsOwnAction(t *testing.T) {
	t.Parallel()
	extra := false
	srv := serve(t, via.Handler(flipRoot{Child: flipChild{extra: &extra}}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	rootAction := actionURL(t, page, "r", 0)

	extra = true // the child's OWN shape now differs from what the GET rendered

	resp, _ := do(t, srv, http.MethodPost, rootAction, "{}")
	assert.NotEqual(t, http.StatusGone, resp.StatusCode,
		"a child-only shape change must not stale the parent's own already-rendered action")
}

// childRootChild is a plain (plain) child of a live root — enough to
// trigger the reuse lookup on the root's own first push.
type childRootChild struct{}

func (childRootChild) View() h.H { return h.Div(h.Str("child")) }

type liveRootWithChild struct {
	n     via.State[int]
	Child childRootChild
}

func (r *liveRootWithChild) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Millisecond, r.tick)
	return nil
}
func (r *liveRootWithChild) tick(ctx *via.Ctx) { r.n.Set(r.n.Get() + 1) }
func (r *liveRootWithChild) View() h.H {
	return h.Div(r.n.Display(), via.Child(r.Child))
}

func TestLive_rootChildSurvivesItsOwnFirstPush(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Handler(liveRootWithChild{}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	awaitTabID(t, lines)

	awaitLine(t, lines, `id="via-i0"`)
}

// twoSpeedChild is a live child whose Tick step is distinguishable from a
// sibling's, so which instance ended up under which container id is provable.
// It has the Noop action so a native form submit against it exists to
// trigger dispatchOverStream's full-page re-render.
type twoSpeedChild struct {
	step int
	n    via.State[int]
}

func (b *twoSpeedChild) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Millisecond, b.tick)
	return nil
}
func (b *twoSpeedChild) tick(ctx *via.Ctx) { b.n.Set(b.n.Get() + b.step) }
func (b *twoSpeedChild) Noop(ctx *via.Ctx) {}
func (b *twoSpeedChild) View() h.H {
	return h.Div(h.Str(strconv.Itoa(b.n.Get())), via.PostForm(b.Noop, h.Button(h.Str("go"))))
}

type twoSpeedChildNoAction struct {
	step int
	n    via.State[int]
}

func (b *twoSpeedChildNoAction) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Millisecond, b.tick)
	return nil
}
func (b *twoSpeedChildNoAction) tick(ctx *via.Ctx) { b.n.Set(b.n.Get() + b.step) }
func (b *twoSpeedChildNoAction) View() h.H         { return h.Div(h.Str(strconv.Itoa(b.n.Get()))) }

type twoLiveChildsRoot struct {
	A twoSpeedChild
	B twoSpeedChildNoAction
}

func (r *twoLiveChildsRoot) View() h.H { return h.Div(via.Child(r.A), via.Child(r.B)) }

func TestPostForm_liveSubmitRendersFreshPageWithDistinctChildIds(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Handler(twoLiveChildsRoot{A: twoSpeedChild{step: 1}, B: twoSpeedChildNoAction{step: 1000}}))

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	awaitLine(t, lines, `selector #via-i0`)
	awaitLine(t, lines, `selector #via-i1`)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, "0", 0) // "0" = A's key: the root's first Child
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

// sessionSettingChild is a live child whose OnInit establishes the
// session (so its cookie reaches the browser on the connect response,
// before any action fires) and whose Login action re-Puts a different value
// into that same session — an in-place mutation, not a fresh cookie.
type sessionSettingChild struct{ n via.State[int] }

func (s *sessionSettingChild) OnInit(ctx *via.Ctx) error {
	ctx.Session().Put(member{Name: "anon"})
	return nil
}
func (s *sessionSettingChild) Login(ctx *via.Ctx) { ctx.Session().Put(member{Name: "zed"}) }
func (s *sessionSettingChild) View() h.H {
	return h.Div(s.n.Display(), via.PostForm(s.Login, h.Button(h.Str("go"))))
}

// rootReadsSessionOnInit is a plain root whose OnInit loads the session
// value into a field — the fresh instance dispatchOverStream's native path now
// renders must see whatever the live child's action last Put, since it
// reads the same session object the action just mutated.
type rootReadsSessionOnInit struct {
	Isl  sessionSettingChild
	name string
}

func (r *rootReadsSessionOnInit) OnInit(ctx *via.Ctx) error {
	if m, ok := ctx.Session().Get[member](); ok {
		r.name = m.Name
	}
	return nil
}
func (r *rootReadsSessionOnInit) View() h.H {
	return h.Div(h.Str(r.name), via.Child(r.Isl))
}

func TestPostForm_liveSubmitRunsOnInitOnTheReturnedPage(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Handler(rootReadsSessionOnInit{}))

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
	url := actionURL(t, page, "0", 0) // child 1 = sessionSettingChild's Login action

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

func (p *initChildHost) View() h.H { return h.Div(h.H1(h.Str("HOST")), via.Child(p.Kid)) }

func TestChild_runsTheChildsOwnOnInit(t *testing.T) {
	t.Parallel()
	hits := 0
	_, body := do(t, serve(t, via.Handler(initChildHost{Kid: childIniter{hits: &hits}})), http.MethodGet, "/", "")

	assert.Contains(t, body, "from-child-init", "the child's OnInit must run before its View renders")
	assert.Equal(t, 1, hits, "the child's OnInit runs exactly once per request-scoped render")
}

// notFoundChild's OnInit reports its data is gone — the honest 404 a root's
// OnInit gets, from inside a render that is already several frames deep.
type notFoundChild struct{}

func (c *notFoundChild) OnInit(ctx *via.Ctx) error { return via.ErrNotFound }
func (c *notFoundChild) View() h.H                 { return h.Div(h.Str("never")) }

type notFoundHost struct{ Kid notFoundChild }

func (p *notFoundHost) View() h.H { return h.Div(via.Child(p.Kid)) }

func TestChild_childOnInitNotFoundAnswers404(t *testing.T) {
	t.Parallel()
	resp, body := do(t, serve(t, via.Handler(notFoundHost{})), http.MethodGet, "/", "")

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

func (p *redirectHost) View() h.H { return h.Div(via.Child(p.Kid)) }

func TestChild_childOnInitRedirectGatesThePage(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(redirectHost{}))
	resp, err := (&http.Client{CheckRedirect: noFollow}).Get(srv.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

// plainKid is a plain child of a live root: it holds no State and never
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

func (p *livePageWithPlainKid) View() h.H { return h.Div(p.n.Display(), via.Child(p.Kid)) }

func TestDispatch_plainChildOfALivePageStaysPlain(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(livePageWithPlainKid{}))
	conn := app.Connect()
	defer conn.Close()

	status, body := app.ChildAction("0", 0).Over(conn).Fire()

	assert.Equal(t, http.StatusOK, status,
		"a plain child on a streaming page must dispatch on the plain path, not 410 as a missing live unit")
	assert.Contains(t, body, "kid-hits=1", "its action answers with its own re-rendered container")
}

// deepCounter is a live leaf whose Signal sits at field offset 0 — the same
// offset deepPanel.Query occupies, so only the child key keeps the two slots
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

// deepPanel is a plain middle child: it has its own Signal and action, and its
// View embeds a live child — the shape Child's godoc once assumed impossible.
type deepPanel struct {
	Query via.Signal[string]
	Kid   deepCounter
	hits  *atomic.Int64
}

func (p *deepPanel) Search(*via.Ctx) { p.hits.Add(1) }
func (p *deepPanel) View() h.H {
	return h.Section(h.Input(p.Query.Bind()),
		h.Button(via.On("click", p.Search), h.Str("search")),
		h.Span(h.Str("hits="), h.Str(int(p.hits.Load()))), via.Child(p.Kid))
}

type deepRoot struct {
	Filter      via.Signal[string]
	Left, Right deepPanel
}

func (d *deepRoot) View() h.H {
	return h.Main(h.Input(d.Filter.Bind()), via.Child(d.Left), via.Child(d.Right))
}

func newDeepRoot(hits *atomic.Int64) deepRoot {
	return deepRoot{Left: deepPanel{hits: hits}, Right: deepPanel{hits: hits}}
}

func TestChild_siblingsOfTheSameTypeGetDistinctKeys(t *testing.T) {
	t.Parallel()
	_, page := do(t, serve(t, via.Handler(newDeepRoot(&atomic.Int64{}))), http.MethodGet, "/", "")

	for _, id := range []string{`id="via-i0"`, `id="via-i0-0"`, `id="via-i1"`, `id="via-i1-0"`} {
		assert.Contains(t, page, id, "every child in the tree needs its own container")
	}
	slots := regexp.MustCompile(`data-bind="([^"]+)"`).FindAllStringSubmatch(page, -1)
	seen := map[string]bool{}
	for _, m := range slots {
		assert.False(t, seen[m[1]], "signal slot %s is bound by two different signals", m[1])
		seen[m[1]] = true
	}
	assert.Len(t, seen, 5, "root filter + two panel queries + two counter steps")
}

func TestChild_plainChildActionKeepsItsNestedLiveChildAddressable(t *testing.T) {
	t.Parallel()
	hits := &atomic.Int64{}
	app := vt.Serve(t, via.Handler(newDeepRoot(hits)))
	conn := app.Connect()
	defer conn.Close()

	status, patch := app.ChildAction("0", 0).Over(conn).Fire()
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, int64(1), hits.Load(), "the left panel's own action ran")

	assert.Len(t, regexp.MustCompile(`id="via-i0"`).FindAllString(patch, -1), 1,
		"the patch must carry the acted child's container exactly once")
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

// threeDeepLeafHost/Mid nest a live leaf under two plain children, so its key is
// a three-segment path — the depth at which a single composition step is no
// longer enough to keep the numbering consistent.
type threeDeepMid struct{ Kid deepCounter }

func (m *threeDeepMid) View() h.H { return h.Div(via.Child(m.Kid)) }

type threeDeepOuter struct{ Mid threeDeepMid }

func (o *threeDeepOuter) View() h.H { return h.Div(via.Child(o.Mid)) }

type threeDeepPage struct{ Outer threeDeepOuter }

func (p *threeDeepPage) View() h.H { return h.Div(via.Child(p.Outer)) }

func TestChild_liveLeafUnderTwoPlainChildsKeepsItsPathKey(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(threeDeepPage{}))
	conn := app.Connect()
	defer conn.Close()

	_, page := app.Get("/")
	assert.Contains(t, page, `id="via-i0-0-0"`, "the leaf's key is the path of ordinals down to it")
	assert.Contains(t, page, `data-bind="outer__mid__kid__step"`)

	status, _ := app.ChildAction("0-0-0", 0).Over(conn).Body(`{"outer__mid__kid__step":3}`).Fire()
	assert.Equal(t, http.StatusNoContent, status, "the leaf dispatches at its own path address")
	assert.Contains(t, conn.Await("n="), "3")
}

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
		h.Button(via.On("click", p.Bump)), via.Child(p.Kid))
}

func TestChild_rootActionPatchInitsNestedChildren(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(initRoot{}))
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
		h.Button(via.On("click", m.Note)), via.Child(m.Kid))
}

type initHost struct{ Mid initMid }

func (h2 *initHost) View() h.H { return h.Div(via.Child(h2.Mid)) }

func TestChild_childActionPatchInitsNestedChildren(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(initHost{}))
	_, page := app.Get("/")
	require.Contains(t, page, "kid=7")

	status, patch := app.ChildAction("0", 0).Fire()
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, patch, "kid=7",
		"the child patch must re-init its own nested children")
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

func (p *twinPage) View() h.H  { return h.Div(via.Child(p.Top), via.Child(p.Foot)) }
func (s *twinShell) View() h.H { return h.Main(via.Child(s.Inner)) }

func TestChild_fallbackSlotNamesAreValidJSIdentifiers(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(twinShell{}))
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
	assert.Contains(t, page, `id="via-i0-0"`, "the child KEY keeps its '-' separator")
	assert.Contains(t, seen, "inner__i0_0__q", "the fallback spells the key with underscores")
}

type shiftFlag struct{ on bool }

type shiftBanner struct{}

func (b *shiftBanner) View() h.H { return h.P(h.ID("banner"), h.Str("banner")) }

type shiftForm struct {
	flag  *shiftFlag
	saved string
}

func (f *shiftForm) Save(ctx *via.Ctx) {
	f.flag.on = true
	f.saved = ctx.Request().FormValue("name")
}

func (f *shiftForm) View() h.H {
	return via.PostForm(f.Save, h.P(h.ID("saved"), h.Str(f.saved)), h.Input(h.Name("name")), h.Button(h.Str("go")))
}

// shiftRoot's Child order changes across the action: the form's own handler
// opens the branch that prepends the banner child, so the acted key ("0") is
// occupied by a shiftBanner on the response re-render while the acted instance
// is a shiftForm. Substituting by key alone splices the form in where the
// banner belongs — the banner vanishes, the form renders twice, and the second
// one carries the form's slot prefix for the wrong type.
type shiftRoot struct {
	flag   *shiftFlag
	Banner shiftBanner
	Form   shiftForm
}

func (r *shiftRoot) banner() h.H { return via.Child(r.Banner) }

func (r *shiftRoot) View() h.H {
	return h.Div(via.When(r.flag.on, r.banner), via.Child(r.Form))
}

func TestChild_actedInstanceIsNotSplicedIntoASlotOfAnotherType(t *testing.T) {
	t.Parallel()
	flag := &shiftFlag{}
	app := vt.Serve(t, via.Handler(shiftRoot{flag: flag, Form: shiftForm{flag: flag}}))
	_, page := app.Get("/")
	require.NotContains(t, page, `id="banner"`)

	status, body := nativeFormPost(t, app, actionURL(t, page, "0", 0), map[string]string{"name": "zed"})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `id="banner"`, "the type that belongs at the acted key must still render")
	assert.Equal(t, 1, strings.Count(body, "<form"), "the acted form must not be rendered in the banner's slot as well")
}

// oninitKid is a plain child whose only content comes from OnInit. Child copies
// it out of the parent's field on every render, so if a push render skips
// OnInit the child comes back zero-valued.
type oninitKid struct{ loaded string }

func (k *oninitKid) OnInit(ctx *via.Ctx) error { k.loaded = "FROM_ONINIT"; return nil }

func (k *oninitKid) View() h.H { return h.Span(h.Str("kid="), h.Str(k.loaded)) }

type tickRootWithKid struct {
	K oninitKid
	n via.State[int]
}

func (p *tickRootWithKid) OnInit(ctx *via.Ctx) error {
	ctx.Tick(2*time.Millisecond, p.tick)
	return nil
}

func (p *tickRootWithKid) tick(ctx *via.Ctx) { p.n.Set(p.n.Get() + 1) }

func (p *tickRootWithKid) View() h.H { return h.Div(p.n.Display(), via.Child(p.K)) }

func TestLive_plainChildUnderALiveRootKeepsItsOnInitState(t *testing.T) {
	t.Parallel()
	srv := liveServer(t, via.Handler(tickRootWithKid{}))

	_, page := do(t, srv, http.MethodGet, "/", "")
	require.Contains(t, page, "kid=FROM_ONINIT", "the GET must render the child's loaded state")

	lines, cancel := openStream(t, srv)
	defer cancel()
	assert.Contains(t, firstElementsFrame(t, lines), "kid=FROM_ONINIT",
		"a push must not serve the child zero-valued")
}

// flakyKid is a plain child of a live root whose OnInit starts failing after
// the connect render. A live root re-inits its plain children on every pushed
// frame, so from then on every frame panics initOutcome — and a push has no
// response to turn that into an HTTP answer.
type flakyKid struct{ inits *atomic.Int32 }

func (k *flakyKid) OnInit(ctx *via.Ctx) error {
	if k.inits.Add(1) > 1 {
		return errors.New("kid init boom")
	}
	return nil
}

func (k *flakyKid) View() h.H { return h.P(h.Str("kid")) }

type liveParentFlakyKid struct {
	Kid flakyKid
	n   via.State[int]
}

func (p *liveParentFlakyKid) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, p.beat)
	return nil
}

func (p *liveParentFlakyKid) beat(ctx *via.Ctx) { p.n.Set(p.n.Get() + 1) }

func (p *liveParentFlakyKid) View() h.H { return h.Div(p.n.Display(), via.Child(p.Kid)) }

func TestLive_childInitFailureOnThePushPathTearsTheStreamDown(t *testing.T) {
	t.Parallel()
	inits := &atomic.Int32{}
	srv := serve(t, via.Handler(liveParentFlakyKid{Kid: flakyKid{inits: inits}}))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	closed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		require.Fail(t, "the stream never closed: every frame is dropped and the tab has no signal at all")
	}
}
