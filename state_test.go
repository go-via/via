package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type statePage struct {
	Hits via.StateTabNum[int]
}

func (p *statePage) Inc(ctx *via.Ctx) error {
	p.Hits.Write(ctx, p.Hits.Read(ctx)+1)
	return nil
}

func (p *statePage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Button(h.Text("+"), on.Click(p.Inc)),
		h.P(p.Hits.Text(ctx)),
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
		"StateTab[int] zero value renders inside view fragment")
}

func TestState_actionMutatesStateForCurrentTab(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[statePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")

	// Open SSE first so flushed patches land in the stream.
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Inc").Fire())
	require.Equal(t, 200, tc.Action("Inc").Fire())
	require.Equal(t, 200, tc.Action("Inc").Fire())

	// We expect at least one element patch with "<p>3</p>".
	vt.AwaitFrame(t, frames, 2*time.Second, "<p>3</p>")
}

type stateIntInitPage struct {
	N via.StateTabNum[int] `via:",init=3"`
}

func (p *stateIntInitPage) View(ctx *via.CtxR) h.H { return h.Div(p.N.Text(ctx)) }

func TestState_initTagSeedsNumericValueFromStructTag(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[stateIntInitPage](app, "/")
	defer server.Close()
	body := getBody(t, server, "/")
	assert.Contains(t, body, "<div>3</div>",
		"StateTab[int] with init=3 must render the seeded value on first load")
}

type stateStringInitPage struct {
	Label via.StateTabStr `via:",init=--"`
}

func (p *stateStringInitPage) View(ctx *via.CtxR) h.H {
	return h.Div(p.Label.Text(ctx))
}

func TestState_initTagSeedsStringValueFromStructTag(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[stateStringInitPage](app, "/")
	defer server.Close()
	body := getBody(t, server, "/")
	assert.Contains(t, body, "<div>--</div>",
		"StateTab[string] with init=-- must render the seeded value on first load")
}

// Update — read-modify-write on StateTab[T] and Signal[T]

type updatePage struct {
	N    via.StateTabNum[int]
	Step via.SignalNum[int] `via:"step,init=1"`
}

func (p *updatePage) DoState(ctx *via.Ctx) error {
	p.N.Write(ctx, 5)
	_ = p.N.Update(ctx, func(n int) (int, error) { return n * 2, nil })
	return nil
}

func (p *updatePage) DoSignal(ctx *via.Ctx) error {
	_ = p.Step.Update(ctx, func(n int) (int, error) { return n + 4, nil })
	return nil
}

func (p *updatePage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Span(h.ID("n"), p.N.Text(ctx)),
		h.Span(h.ID("step"), p.Step.Text()),
	)
}

func TestUpdate_appliesFnToState(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[updatePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// Set(5) then Update(*2) → 10.
	require.Equal(t, http.StatusOK, tc.Action("DoState").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="n">10</span>`)
}

func TestUpdate_appliesFnToSignal(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[updatePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// init=1, Update(+4) → 5.
	require.Equal(t, http.StatusOK, tc.Action("DoSignal").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"step":5`)
}

// State.Key isn't externally observable: StateTab[T] is server-rendered, so
// the wire key never appears in the client-visible payload (unlike
// Signal.Key, which surfaces via data-text="$<key>" and data-bind="<key>").
// Tag-driven key resolution for State is exercised end-to-end by the
// init-tag tests above, where mis-resolving the key would render the
// wrong seeded value.
