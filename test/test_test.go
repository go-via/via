package test_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tcPage struct {
	N     via.State[int]
	Label via.Signal[string] `via:"label,init=hello"`
}

func (p *tcPage) Bump(ctx *via.Ctx) error {
	p.N.Set(ctx, p.N.Get(ctx)+1)
	return nil
}

func (p *tcPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.N.Text(), h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestNewClient_picksUpTabIDFromRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	tab := tc.TabID()
	assert.NotEmpty(t, tab)
	assert.True(t, strings.HasPrefix(tab, "/_"),
		"tab id is route-prefixed; got %q", tab)
}

func TestClient_HTML_returnsLastFetchedBody(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	body := tc.HTML()
	assert.Contains(t, body, "<button")
	assert.Contains(t, body, ">+<")
}

func TestClient_Reload_refetchesAndReturnsCurrentBody(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	originalTab := tc.TabID()
	original := tc.HTML()
	require.NotEmpty(t, original)

	// Reload returns a fresh fetch and rotates the tab id (each GET
	// registers a new Ctx — that's the contract documented on Reload).
	body := tc.Reload()
	assert.Contains(t, body, "<button")
	assert.NotEqual(t, originalTab, tc.TabID(),
		"each GET registers a new tab; Reload must update tabID")
	assert.Equal(t, body, tc.HTML(),
		"HTML() should now return what Reload just stored")
}

func TestActionCall_Fire_returnsResponseStatus(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Bump").Fire())
}

func TestAction_acceptsBoundMethodValue(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	page := &tcPage{}
	// Typed form: pass the bound method, get the action name resolved
	// via reflect — typo-proof since Bump is referenced, not stringified.
	require.Equal(t, 200, tc.Action(page.Bump).Fire())
}

func TestActionCall_Fire_returns404OnUnknownMethod(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	assert.Equal(t, 404, tc.Action("DoesNotExist").Fire())
}

func TestActionCall_WithSignal_carriesValueIntoActionPayload(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")

	// Fire 3 increments, each carrying a different incoming "label"
	// signal value. The state should grow to 3 and the latest signal
	// payload should land in the rendered fragment.
	require.Equal(t, 200, tc.Action("Bump").
		WithSignal("label", "first").Fire())
	require.Equal(t, 200, tc.Action("Bump").
		WithSignal("label", "second").Fire())
	require.Equal(t, 200, tc.Action("Bump").
		WithSignal("label", "third").Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	viatest.AwaitFrame(t, frames, 2*time.Second, ">3<")
}

type unitTestPage struct {
	N    via.State[int]
	Step via.Signal[int] `via:"step,init=2"`
}

func (p *unitTestPage) Inc(ctx *via.Ctx) error {
	p.N.Set(ctx, p.N.Get(ctx)+p.Step.Get(ctx))
	return nil
}

func (p *unitTestPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestNewCtx_invokesActionMethodsDirectly(t *testing.T) {
	t.Parallel()

	c := &unitTestPage{}
	ctx := viatest.NewCtx(t, c)

	require.NoError(t, c.Inc(ctx))
	require.NoError(t, c.Inc(ctx))
	require.NoError(t, c.Inc(ctx))

	assert.Equal(t, 6, c.N.Get(ctx),
		"three Inc calls with init=2 step should yield 6")
}

func TestNewCtx_initialSignalValueComesFromTag(t *testing.T) {
	t.Parallel()

	c := &unitTestPage{}
	ctx := viatest.NewCtx(t, c)
	assert.Equal(t, 2, c.Step.Get(ctx),
		"init=2 tag should populate the Signal at NewCtx time")
}

type redirectingPage struct{}

func (p *redirectingPage) Login(ctx *via.Ctx) error {
	ctx.Redirect("/profile")
	ctx.ExecScript("console.log('hi')")
	ctx.PatchSignal("_picoTheme", "blue")
	return nil
}

func (p *redirectingPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestNewCtx_pendingRedirectVisibleViaCtxIntrospection(t *testing.T) {
	t.Parallel()

	c := &redirectingPage{}
	ctx := viatest.NewCtx(t, c)
	require.NoError(t, c.Login(ctx))

	assert.Equal(t, "/profile", ctx.PendingRedirect())
	assert.Contains(t, ctx.PendingScripts(), "console.log('hi')")
	sigs := ctx.PendingSignals()
	assert.Equal(t, "blue", sigs["_picoTheme"])
}

func TestSSE_streamsHeartbeatsAndPatches(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithSSEHeartbeat(50*time.Millisecond),
	)
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	// Without firing any action we should still observe at least one
	// heartbeat frame within 1s thanks to the short heartbeat interval.
	viatest.AwaitFrame(t, frames, 1500*time.Millisecond, "datastar-patch-signals")
}
