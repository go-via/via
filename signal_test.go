package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type signalCounter struct {
	Step via.Signal[int] `via:"step,init=1"`
	Name via.Signal[string]
}

func (c *signalCounter) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Input(h.Type("number"), c.Step.Bind()),
		h.P(c.Step.Text()),
		h.Span(c.Name.Text()),
	)
}

func TestSignal_renderingProducesExpectedAttributes(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalCounter](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	cases := []struct {
		name, needle, why string
	}{
		{"init from tag", `&#34;step&#34;:1`, "init=1 must appear in data-signals meta"},
		{"Bind() renders data-bind", `data-bind="step"`, "Bind() must render data-bind with wire key"},
		{"Text() renders data-text span", `data-text="$step"`, "Text() must render data-text=$<key>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, body, c.needle, c.why)
		})
	}
}

type signalShowPage struct {
	Open via.Signal[bool] `via:"open"`
}

func (p *signalShowPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.Open.Show(), h.Text("hello"))
}

func TestSignal_showRendersDataShowExpression(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalShowPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-show="$open"`,
		"Show should produce data-show=$<key>")
}

type fieldNameKey struct {
	MyField via.Signal[int]
}

func (c *fieldNameKey) View(ctx *via.Ctx) h.H { return h.Div() }

func TestSignal_keyDefaultsToLowercasedFieldName(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[fieldNameKey](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `&#34;myField&#34;:0`)
}

// helpers

func getBody(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	resp, err := http.Get(server.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return string(buf)
}

type attrStylePage struct {
	Disabled via.Signal[bool]   `via:"disabled"`
	Hue      via.Signal[string] `via:"hue,init=blue"`
}

func (p *attrStylePage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Button(p.Disabled.Attr("disabled"), h.Text("Save")),
		h.Span(p.Hue.Style("color"), h.Text("hi")),
	)
}

func TestSignal_Attr_rendersDataAttrSyntax(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[attrStylePage](app, "/")
	defer server.Close()
	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-attr-disabled="$disabled"`,
		"Signal.Attr(name) should emit Datastar's data-attr-<name>=\"$key\"")
}

func TestSignal_Style_rendersDataStyleSyntax(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[attrStylePage](app, "/")
	defer server.Close()
	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-style-color="$hue"`,
		"Signal.Style(prop) should emit Datastar's data-style-<prop>=\"$key\"")
}

type boolInitPage struct {
	On via.Signal[bool] `via:"on,init=true"`
}

func (p *boolInitPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestSignal_initTagParsesBoolFromStructTag(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[boolInitPage](app, "/")
	defer server.Close()
	body := getBody(t, server, "/")
	assert.Contains(t, body, `&#34;on&#34;:true`,
		"Signal[bool] with init=true must initialise to true (struct tags arrive as strings)")
}

// Toggle / Add / Push / PushBounded / SetIfChanged — typed helpers
// observed through SSE-frame side effects.

type signalHelpersPage struct {
	Open  via.Signal[bool]    `via:"open"`
	Count via.Signal[int]     `via:"count,init=10"`
	Bal   via.Signal[float64] `via:"bal"`
	Hits  via.State[int]
	Vis   via.State[bool]
	Items via.Signal[[]int] `via:"items"`
}

func (p *signalHelpersPage) FlipOpen(ctx *via.Ctx) error {
	via.Toggle(ctx, &p.Open)
	return nil
}

func (p *signalHelpersPage) ToggleVis(ctx *via.Ctx) error {
	via.Toggle(ctx, &p.Vis)
	return nil
}

func (p *signalHelpersPage) AddCount(ctx *via.Ctx) error {
	via.Add(ctx, &p.Count, 3)
	via.Add(ctx, &p.Count, -5)
	return nil
}

func (p *signalHelpersPage) AddBal(ctx *via.Ctx) error {
	via.Add(ctx, &p.Bal, 0.5)
	via.Add(ctx, &p.Bal, 0.25)
	return nil
}

func (p *signalHelpersPage) AddHits(ctx *via.Ctx) error {
	via.Add(ctx, &p.Hits, 7)
	via.Add(ctx, &p.Hits, -2)
	return nil
}

func (p *signalHelpersPage) PushOne(ctx *via.Ctx) error {
	via.Push(ctx, &p.Items, 1)
	via.Push(ctx, &p.Items, 2)
	via.Push(ctx, &p.Items, 3)
	return nil
}

func (p *signalHelpersPage) PushFive(ctx *via.Ctx) error {
	for i := 1; i <= 5; i++ {
		via.PushBounded(ctx, &p.Items, i, 3)
	}
	return nil
}

func (p *signalHelpersPage) View(ctx *via.Ctx) h.H {
	// State[T] doesn't surface in signals JSON; rendered text is its
	// only externally observable trace, so views that drive State helper
	// tests must render the value somewhere assertable.
	return h.Div(
		h.Span(h.ID("hits"), p.Hits.Text()),
		h.Span(h.ID("vis"), h.Textf("%v", p.Vis.Get(ctx))),
	)
}

func TestToggle_flipsBoolSignalSurfacingInSSE(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("FlipOpen").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `"open":true`)

	require.Equal(t, http.StatusOK, tc.Action("FlipOpen").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `"open":false`)
}

func TestToggle_acceptsStateAsWellAsSignal(t *testing.T) {
	t.Parallel()
	// Pins the Mutable[bool] polymorphism: Toggle must accept State[bool]
	// as well as Signal[bool], otherwise via.State stops being a drop-in
	// substitute for via.Signal in reactive helpers.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("ToggleVis").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `<span id="vis">true</span>`)
}

func TestAdd_intSignalAcceptsPositiveAndNegativeDeltas(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// init=10, then +3, -5 → 8
	require.Equal(t, http.StatusOK, tc.Action("AddCount").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `"count":8`)
}

func TestAdd_floatSignalRespectsType(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("AddBal").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `"bal":0.75`)
}

func TestAdd_acceptsStateAsWellAsSignal(t *testing.T) {
	t.Parallel()
	// Mirror of TestToggle_acceptsStateAsWellAsSignal but for the numeric
	// helper. Pins Mutable[int] conformance of via.State[int].
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("AddHits").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `<span id="hits">5</span>`)
}

func TestPush_appendsItemsToSignalSlice(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("PushOne").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `"items":[1,2,3]`)
}

func TestPushBounded_keepsOnlyLatestMaxItems(t *testing.T) {
	t.Parallel()
	// Push five into a max=3 buffer: oldest two roll off, leaving [3,4,5].
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("PushFive").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `"items":[3,4,5]`)
}

// SetIfChanged: changed values reach the wire; unchanged values do not
// trigger a second patch.

type setIfChangedPage struct {
	Status via.Signal[string] `via:"status,init=idle"`
}

func (p *setIfChangedPage) SetSame(ctx *via.Ctx) error {
	via.SetIfChanged(ctx, &p.Status, "idle")
	return nil
}

func (p *setIfChangedPage) SetBusy(ctx *via.Ctx) error {
	via.SetIfChanged(ctx, &p.Status, "busy")
	return nil
}

func (p *setIfChangedPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestSetIfChanged_changedValueProducesSignalFrame(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[setIfChangedPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("SetBusy").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `"status":"busy"`)
}

func TestSetIfChanged_unchangedValueProducesNoFrame(t *testing.T) {
	t.Parallel()
	// Writing the existing value must short-circuit before reaching the
	// patch queue — no datastar-patch-signals frame should appear for
	// this action. Wait briefly; absence is the assertion.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[setIfChangedPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("SetSame").Fire())
	select {
	case f := <-frames:
		assert.NotContains(t, f, `"status"`,
			"SetIfChanged with identical value must not enqueue a status patch")
	case <-time.After(200 * time.Millisecond):
		// No frame at all is the success path.
	}
}
