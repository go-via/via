package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type signalCounter struct {
	Step via.SignalNum[int] `via:"step,init=1"`
	Name via.SignalStr
}

func (c *signalCounter) View(ctx *via.CtxR) h.H {
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
	Open via.SignalBool `via:"open"`
}

func (p *signalShowPage) View(ctx *via.CtxR) h.H {
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
	MyField via.SignalNum[int]
}

func (c *fieldNameKey) View(ctx *via.CtxR) h.H { return h.Div() }

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
	Disabled via.SignalBool `via:"disabled"`
	Hue      via.SignalStr  `via:"hue,init=blue"`
}

func (p *attrStylePage) View(ctx *via.CtxR) h.H {
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
	On via.SignalBool `via:"on,init=true"`
}

func (p *boolInitPage) View(ctx *via.CtxR) h.H { return h.Div() }

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

// Update-driven read-modify-write patterns observed through SSE-frame
// side effects: bool flip, numeric delta, slice append, bounded ring.

type signalHelpersPage struct {
	Open  via.SignalBool         `via:"open"`
	Count via.SignalNum[int]     `via:"count,init=10"`
	Bal   via.SignalNum[float64] `via:"bal"`
	Hits  via.StateTabNum[int]
	Vis   via.StateTabBool
	Items via.SignalSlice[int] `via:"items"`
}

func (p *signalHelpersPage) FlipOpen(ctx *via.Ctx) error {
	_ = p.Open.Update(ctx, func(b bool) (bool, error) { return !b, nil })
	return nil
}

func (p *signalHelpersPage) ToggleVis(ctx *via.Ctx) error {
	_ = p.Vis.Update(ctx, func(b bool) (bool, error) { return !b, nil })
	return nil
}

func (p *signalHelpersPage) AddCount(ctx *via.Ctx) error {
	_ = p.Count.Update(ctx, func(n int) (int, error) { return n + 3, nil })
	_ = p.Count.Update(ctx, func(n int) (int, error) { return n - 5, nil })
	return nil
}

func (p *signalHelpersPage) AddBal(ctx *via.Ctx) error {
	_ = p.Bal.Update(ctx, func(v float64) (float64, error) { return v + 0.5, nil })
	_ = p.Bal.Update(ctx, func(v float64) (float64, error) { return v + 0.25, nil })
	return nil
}

func (p *signalHelpersPage) AddHits(ctx *via.Ctx) error {
	_ = p.Hits.Update(ctx, func(n int) (int, error) { return n + 7, nil })
	_ = p.Hits.Update(ctx, func(n int) (int, error) { return n - 2, nil })
	return nil
}

func (p *signalHelpersPage) PushOne(ctx *via.Ctx) error {
	_ = p.Items.Update(ctx, func(s []int) ([]int, error) { return append(s, 1), nil })
	_ = p.Items.Update(ctx, func(s []int) ([]int, error) { return append(s, 2), nil })
	_ = p.Items.Update(ctx, func(s []int) ([]int, error) { return append(s, 3), nil })
	return nil
}

func (p *signalHelpersPage) PushFive(ctx *via.Ctx) error {
	const max = 3
	for i := 1; i <= 5; i++ {
		item := i
		_ = p.Items.Update(ctx, func(s []int) ([]int, error) {
			s = append(s, item)
			if len(s) > max {
				copy(s, s[len(s)-max:])
				s = s[:max]
			}
			return s, nil
		})
	}
	return nil
}

func (p *signalHelpersPage) View(ctx *via.CtxR) h.H {
	// StateTab[T] doesn't surface in signals JSON; rendered text is its
	// only externally observable trace, so views that drive State helper
	// tests must render the value somewhere assertable.
	return h.Div(
		h.Span(h.ID("hits"), p.Hits.Text(ctx)),
		h.Span(h.ID("vis"), h.Textf("%v", p.Vis.Read(ctx))),
	)
}

func TestUpdate_flipsBoolSignalSurfacingInSSE(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("FlipOpen").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"open":true`)

	require.Equal(t, http.StatusOK, tc.Action("FlipOpen").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"open":false`)
}

func TestUpdate_flipsBoolStateTabSurfacingInView(t *testing.T) {
	t.Parallel()
	// Pins that StateTab[bool].Update works the same as Signal[bool].Update —
	// via.StateTab stays a drop-in substitute for via.Signal in reactive
	// read-modify-write code.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("ToggleVis").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="vis">true</span>`)
}

func TestUpdate_intSignalAcceptsPositiveAndNegativeDeltas(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// init=10, then +3, -5 → 8
	require.Equal(t, http.StatusOK, tc.Action("AddCount").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"count":8`)
}

func TestUpdate_floatSignalRespectsType(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("AddBal").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"bal":0.75`)
}

func TestUpdate_numericStateTabRendersThroughView(t *testing.T) {
	t.Parallel()
	// Mirror of TestUpdate_flipsBoolStateTabSurfacingInView for numeric
	// state: StateTab[int].Update + Text() must produce the running total.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("AddHits").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="hits">5</span>`)
}

func TestUpdate_appendsItemsToSliceSignal(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("PushOne").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"items":[1,2,3]`)
}

func TestUpdate_boundedRingKeepsOnlyLatestMaxItems(t *testing.T) {
	t.Parallel()
	// Push five into a max=3 buffer: oldest two roll off, leaving [3,4,5].
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalHelpersPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("PushFive").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"items":[3,4,5]`)
}

// Inline "set if changed" guard: changed values reach the wire;
// unchanged values do not trigger a second patch.

type setIfChangedPage struct {
	Status via.SignalStr `via:"status,init=idle"`
}

func (p *setIfChangedPage) SetSame(ctx *via.Ctx) error {
	if p.Status.Read(ctx) != "idle" {
		p.Status.Op(ctx).To("idle")
	}
	return nil
}

func (p *setIfChangedPage) SetBusy(ctx *via.Ctx) error {
	if p.Status.Read(ctx) != "busy" {
		p.Status.Op(ctx).To("busy")
	}
	return nil
}

func (p *setIfChangedPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestUpdate_changedValueProducesSignalFrame(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[setIfChangedPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("SetBusy").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"status":"busy"`)
}

func TestUpdate_unchangedValueProducesNoFrame(t *testing.T) {
	t.Parallel()
	// The inline Get != v guard must short-circuit before the Update
	// call, so no datastar-patch-signals frame should appear for this
	// action. Wait briefly; absence is the assertion.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[setIfChangedPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("SetSame").Fire())
	select {
	case f := <-frames:
		assert.NotContains(t, f, `"status"`,
			"identical-value guard must not enqueue a status patch")
	case <-time.After(200 * time.Millisecond):
		// No frame at all is the success path.
	}
}
