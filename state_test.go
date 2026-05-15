package via_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/scope"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type statePage struct {
	Hits via.State[int]
}

func (p *statePage) Inc(ctx *via.Ctx) error {
	p.Hits.Set(ctx, p.Hits.Get(ctx)+1)
	return nil
}

func (p *statePage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Button(h.Text("+"), on.Click(p.Inc)),
		h.P(p.Hits.Text()),
	)
}

func TestState_initialZeroValueAppearsInRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[statePage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, "<p>0</p>",
		"State[int] zero value renders inside view fragment")
}

func TestState_actionMutatesStateForCurrentTab(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[statePage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")

	// Open SSE first so flushed patches land in the stream.
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Inc").Fire())
	require.Equal(t, 200, tc.Action("Inc").Fire())
	require.Equal(t, 200, tc.Action("Inc").Fire())

	// We expect at least one element patch with "<p>3</p>".
	viatest.AwaitFrame(t, frames, 2*time.Second, "<p>3</p>")
}

// Update: read-modify-write helper across all reactive shapes

type updatablePage struct {
	N      via.State[int]
	Step   via.Signal[int] `via:"step,init=1"`
	Theme  scope.User[string]
	Visits scope.App[int]
}

func (p *updatablePage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestUpdate_StateApplyFn(t *testing.T) {
	t.Parallel()

	c := &updatablePage{}
	ctx := viatest.NewCtx(t, c)

	c.N.Set(ctx, 5)
	c.N.Update(ctx, func(n int) int { return n * 2 })
	assert.Equal(t, 10, c.N.Get(ctx))
}

func TestUpdate_SignalApplyFn(t *testing.T) {
	t.Parallel()

	c := &updatablePage{}
	ctx := viatest.NewCtx(t, c)

	c.Step.Update(ctx, func(n int) int { return n + 4 })
	assert.Equal(t, 5, c.Step.Get(ctx),
		"init=1 plus +4 from Update = 5")
}

func TestUpdate_ScopeUserApplyFn(t *testing.T) {
	t.Parallel()

	c := &updatablePage{}
	ctx := viatest.NewCtx(t, c)

	c.Theme.Set(ctx, "blue")
	c.Theme.Update(ctx, func(s string) string { return s + "-dark" })
	assert.Equal(t, "blue-dark", c.Theme.Get(ctx))
}

func TestUpdate_NilFnIsNoOp(t *testing.T) {
	t.Parallel()

	c := &updatablePage{}
	ctx := viatest.NewCtx(t, c)
	c.N.Set(ctx, 7)
	c.N.Update(ctx, nil)
	assert.Equal(t, 7, c.N.Get(ctx))
}
