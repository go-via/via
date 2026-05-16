package via_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bareEmbedPage struct {
	via.Page
	Hits via.State[int]
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

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	viatest.AwaitFrame(t, frames, 2*time.Second, "_pageConnected")
	assert.GreaterOrEqual(t, connectFiredCount.Load(), int32(1),
		"the overriding OnConnect must fire when SSE opens")
}
