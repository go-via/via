package via_test

import (
	"net/http"
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

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[statePage](app, "/")

	body := getBody(t, server, "/")
	assert.Contains(t, body, "<p>0</p>",
		"StateTab[int] zero value renders inside view fragment")
}

func TestState_actionMutatesStateForCurrentTab(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[statePage](app, "/")

	tc := vt.NewClient(t, server, "/")

	// Open SSE first so flushed patches land in the stream.
	frames, cancel := tc.SSEReady()
	defer cancel()

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
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[stateIntInitPage](app, "/")
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
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[stateStringInitPage](app, "/")
	body := getBody(t, server, "/")
	assert.Contains(t, body, "<div>--</div>",
		"StateTab[string] with init=-- must render the seeded value on first load")
}

type stateScalarTextPage struct {
	On    via.StateTabBool         `via:"on,init=true"`
	Off   via.StateTabBool         `via:"off"`
	Count via.StateTabNum[uint]    `via:"count,init=42"`
	Ratio via.StateTabNum[float64] `via:"ratio,init=2.5"`
	Tags  via.StateTab[[]string]   `via:"tags"`
}

func (p *stateScalarTextPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Span(h.ID("on"), p.On.Text(ctx)),
		h.Span(h.ID("off"), p.Off.Text(ctx)),
		h.Span(h.ID("count"), p.Count.Text(ctx)),
		h.Span(h.ID("ratio"), p.Ratio.Text(ctx)),
		h.Span(h.ID("tags"), p.Tags.Text(ctx)),
	)
}

func TestStateTabText_rendersBoolUintFloatAndCompositeKinds(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[stateScalarTextPage](app, "/")

	// StateTab[T].Text routes every value kind through scalarString; the
	// existing tests only exercised string + int.
	body := getBody(t, server, "/")
	assert.Contains(t, body, `<span id="on">true</span>`, "bool true")
	assert.Contains(t, body, `<span id="off">false</span>`, "bool false")
	assert.Contains(t, body, `<span id="count">42</span>`, "uint")
	assert.Contains(t, body, `<span id="ratio">2.5</span>`, "float64")
	assert.Contains(t, body, `<span id="tags">null</span>`,
		"a composite StateTab value falls back to JSON (nil slice → null)")
}

type stateFloat32TextPage struct {
	Rate via.StateTabNum[float32] `via:"rate,init=0.1"`
}

func (p *stateFloat32TextPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Span(h.ID("rate"), p.Rate.Text(ctx)))
}

func TestStateTabText_rendersFloat32WithoutFloat64Noise(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[stateFloat32TextPage](app, "/")

	body := getBody(t, server, "/")
	assert.Contains(t, body, `<span id="rate">0.1</span>`,
		"float32 must format at its own precision, not widen to float64")
	assert.NotContains(t, body, "0.10000000149011612",
		"reflect.Value.Float widens float32; bitSize 64 leaks the expansion")
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

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[updatePage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	// Set(5) then Update(*2) → 10.
	require.Equal(t, http.StatusOK, tc.Action("DoState").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="n">10</span>`)
}

func TestUpdate_appliesFnToSignal(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[updatePage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

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

func TestStateTab_panicsOnNilCtxUpdate(t *testing.T) {
	t.Parallel()
	var s via.StateTabNum[int]
	assert.PanicsWithValue(t,
		"via: StateTab.Update called with nil *Ctx",
		func() { _ = s.Update(nil, func(int) (int, error) { return 1, nil }) },
	)
}

func TestStateTab_panicsOnNilCtxWrite(t *testing.T) {
	t.Parallel()
	var s via.StateTabNum[int]
	assert.PanicsWithValue(t,
		"via: StateTab.Write called with nil *Ctx",
		func() { s.Write(nil, 1) },
	)
}
