package via_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bareEmbedPage struct {
	via.Page
	Hits via.StateTab[int]
}

func (p *bareEmbedPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.Hits.Text())
}

func TestPage_embeddedDefaultsDoNotInterfereWithRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[bareEmbedPage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"embedding via.Page with only View defined must render normally")
}

// initCounterEmbed counts OnInit invocations via a package-level atomic
// counter because Mount allocates a fresh *initCounterEmbed per page
// request, so per-struct fields wouldn't survive across renders.
var initFiredCount atomic.Int32

type initCounterEmbed struct {
	via.Page
}

func (p *initCounterEmbed) OnInit(ctx *via.Ctx) error {
	initFiredCount.Add(1)
	return nil
}

func (p *initCounterEmbed) View(ctx *via.Ctx) h.H { return h.Div() }

func TestPage_embeddedAllowsOverridingOnInit(t *testing.T) {
	initFiredCount.Store(0) // shared with other tests in this package; isolate
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[initCounterEmbed](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, int32(1), initFiredCount.Load(),
		"the overriding OnInit on the embedding composition must take precedence over via.Page's no-op default")
}

var connectFiredCount atomic.Int32

type connectCounterEmbed struct {
	via.Page
}

// PatchSignal queues a frame the SSE drain emits without needing a
// flush, giving us a deterministic frame to await for the assertion.
func (p *connectCounterEmbed) OnConnect(ctx *via.Ctx) error {
	connectFiredCount.Add(1)
	ctx.PatchSignal("_pageConnected", true)
	return nil
}

func (p *connectCounterEmbed) View(ctx *via.Ctx) h.H { return h.Div() }

func TestPage_embeddedAllowsOverridingOnConnect(t *testing.T) {
	connectFiredCount.Store(0)
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[connectCounterEmbed](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	vt.AwaitFrame(t, frames, 2*time.Second, "_pageConnected")
	assert.GreaterOrEqual(t, connectFiredCount.Load(), int32(1),
		"the overriding OnConnect must fire when SSE opens")
}

var disposeFiredCount atomic.Int32

type disposeCounterEmbed struct {
	via.Page
}

func (p *disposeCounterEmbed) OnDispose(ctx *via.Ctx) {
	disposeFiredCount.Add(1)
}

func (p *disposeCounterEmbed) View(ctx *via.Ctx) h.H { return h.Div() }

func TestPage_embeddedAllowsOverridingOnDispose(t *testing.T) {
	disposeFiredCount.Store(0)
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[disposeCounterEmbed](app, "/")
	defer server.Close()

	_ = vt.NewClient(t, server, "/")

	require.NoError(t, app.Shutdown(context.Background()))
	require.Eventually(t, func() bool { return disposeFiredCount.Load() == 1 },
		2*time.Second, 10*time.Millisecond,
		"the overriding OnDispose on the embedding composition must take precedence over via.Page's no-op default")
}

type userScopedPage struct {
	Theme via.StateSess[string]
}

func (p *userScopedPage) UseRed(ctx *via.Ctx) error {
	p.Theme.Update(ctx, func(string) string { return "red" })
	return nil
}

func (p *userScopedPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.P(h.Text("theme="), p.Theme.Text(ctx)), h.Button(h.Text("red"), on.Click(p.UseRed)))
}

func TestScopeUser_writeFromActionAppearsInRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userScopedPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("UseRed").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "theme=red")
}

type appScopedPage struct {
	Visits via.StateApp[int]
}

func (p *appScopedPage) Bump(ctx *via.Ctx) error {
	p.Visits.Update(ctx, func(n int) int { return n + 1 })
	return nil
}

func (p *appScopedPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.Visits.Text(ctx))
}

func TestScopeApp_sharedAcrossSessions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[appScopedPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	require.Equal(t, 200, a.Action("Bump").Fire())
	require.Equal(t, 200, a.Action("Bump").Fire())

	b := vt.NewClient(t, server, "/")
	body := b.HTML()
	assert.Contains(t, body, ">2<",
		"App-scoped Visits must be 2 even on a fresh session")
}
