package echarts_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type chartActionPage struct {
	Chart *echarts.Chart
}

func (p *chartActionPage) SetOpt(ctx *via.Ctx) error {
	return p.Chart.SetOption(ctx, map[string]any{"backgroundColor": "#fff"})
}

func (p *chartActionPage) SetSer(ctx *via.Ctx) error {
	return p.Chart.SetSeries(ctx, echarts.Line("L", [][]any{{0, 1}, {1, 2}}))
}

func (p *chartActionPage) SetTwoSer(ctx *via.Ctx) error {
	return p.Chart.SetSeries(ctx,
		echarts.Line("Read", [][]any{{0, 1}}),
		echarts.Line("Write", [][]any{{0, 2}}),
	)
}

func (p *chartActionPage) AppendPoints(ctx *via.Ctx) error {
	return p.Chart.AppendData(ctx, 0, [][]any{{42, 17}})
}

func (p *chartActionPage) AppendOne(ctx *via.Ctx) error {
	return p.Chart.AppendPoint(ctx, 0, []any{99, 7})
}

func (p *chartActionPage) AppendXYTyped(ctx *via.Ctx) error {
	return p.Chart.AppendXY(ctx, 0, int64(1700000000000), 42.5)
}

func (p *chartActionPage) RangeY(ctx *via.Ctx) error {
	return p.Chart.SetYAxisRange(ctx, 0, 200)
}

func (p *chartActionPage) RangeX(ctx *via.Ctx) error {
	return p.Chart.SetXAxisRange(ctx, int64(1700000000000), int64(1700000300000))
}

func (p *chartActionPage) AppendXYAtMulti(ctx *via.Ctx) error {
	return p.Chart.AppendXYAt(ctx, int64(1700000000000), 42.0, 17.5, 88.1)
}

func (p *chartActionPage) AppendNothing(ctx *via.Ctx) error {
	return p.Chart.AppendData(ctx, 0, nil)
}

func (p *chartActionPage) AppendOneAlt(ctx *via.Ctx) error {
	return p.Chart.AppendPoint(ctx, 1, []any{12, 34})
}

func (p *chartActionPage) ClearSer(ctx *via.Ctx) error {
	return p.Chart.SetSeries(ctx)
}

func (p *chartActionPage) Tear(ctx *via.Ctx) error {
	p.Chart.Dispose(ctx)
	return nil
}

func (p *chartActionPage) Retitle(ctx *via.Ctx) error {
	return p.Chart.SetTitle(ctx, "Latency (p99)")
}

func (p *chartActionPage) Resubtitle(ctx *via.Ctx) error {
	return p.Chart.SetSubtitle(ctx, "updated 12:04:33")
}

func (p *chartActionPage) Darken(ctx *via.Ctx) error {
	p.Chart.SetTheme(ctx, echarts.ThemeDark)
	return nil
}

func (p *chartActionPage) Reflow(ctx *via.Ctx) error {
	p.Chart.Resize(ctx)
	return nil
}

func (p *chartActionPage) ShowLegend(ctx *via.Ctx) error {
	return p.Chart.SetLegend(ctx, true)
}

func (p *chartActionPage) HideLegend(ctx *via.Ctx) error {
	return p.Chart.SetLegend(ctx, false)
}

func (p *chartActionPage) Wipe(ctx *via.Ctx) error {
	p.Chart.Clear(ctx)
	return nil
}

func (p *chartActionPage) Spin(ctx *via.Ctx) error {
	p.Chart.SetLoading(ctx, true)
	return nil
}

func (p *chartActionPage) StopSpin(ctx *via.Ctx) error {
	p.Chart.SetLoading(ctx, false)
	return nil
}

func (p *chartActionPage) OnInit(ctx *via.Ctx) error {
	// Bind a fresh Chart per ctx so each test gets a distinct element ID.
	if p.Chart == nil {
		p.Chart = echarts.NewChart(echarts.WithElementID("ch"))
	}
	return nil
}

func (p *chartActionPage) View(ctx *via.CtxR) h.H {
	if p.Chart == nil {
		return h.Div()
	}
	return p.Chart.Mount()
}

// fireChartAction spins a one-page app backed by chartActionPage, opens
// an SSE stream, fires the named action, and waits for an SSE frame
// containing all the given needles. Used to cut the per-test setup
// boilerplate (server, client, SSE channel) that's identical across
// most runtime tests.
func fireChartAction(t *testing.T, action string, needles ...string) {
	t.Helper()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[chartActionPage](app, "/")
	t.Cleanup(server.Close)

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	t.Cleanup(cancel)

	require.Equal(t, 200, tc.Action(action).Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, needles...)
}

func TestChartAPI_SetOption_emitsSetOptionScript(t *testing.T) {
	t.Parallel()
	fireChartAction(t, "SetOpt", "setOption", "backgroundColor")
}

func TestChartAPI_SetSeries_emitsSeriesUpdate(t *testing.T) {
	t.Parallel()
	fireChartAction(t, "SetSer", `"series"`, `"line"`)
}

func TestChartAPI_SetSeries_carriesAllVariadicSeriesIntoTheFrame(t *testing.T) {
	t.Parallel()
	// Distinct names rule out a bug that takes only series[0] or
	// duplicates one entry.
	fireChartAction(t, "SetTwoSer", "setOption", `"Read"`, `"Write"`)
}

func TestChartAPI_AppendData_emitsAppendDataCall(t *testing.T) {
	t.Parallel()
	fireChartAction(t, "AppendPoints", "appendData", "seriesIndex:0")
}

func TestChartAPI_AppendData_emptyDataIsNoop(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[chartActionPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("AppendNothing").Fire())
	// Streaming pipelines often produce an empty batch on a tick with
	// no new samples; the server should swallow that rather than push
	// a noisy `appendData({data:[]})` frame across the wire.
	select {
	case f := <-frames:
		assert.NotContains(t, f, "appendData",
			"empty data must not emit an appendData frame")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestChartAPI_AppendXYAt_emitsOnePointPerSeriesSharingX(t *testing.T) {
	t.Parallel()
	// Three values + one shared x → three appendData calls in one
	// SSE frame, each routed to its positional seriesIdx.
	fireChartAction(t, "AppendXYAtMulti",
		"seriesIndex:0", "[[1700000000000,42]]",
		"seriesIndex:1", "[[1700000000000,17.5]]",
		"seriesIndex:2", "[[1700000000000,88.1]]")
}

func TestChartAPI_SetXAxisRange_navigatesToTimeWindow(t *testing.T) {
	t.Parallel()
	// Programmatic time-window navigation — "follow latest 5 min" or
	// "jump to incident at 10:15" buttons set the visible x-axis range.
	fireChartAction(t, "RangeX",
		"setOption", `"xAxis"`, `"min":1700000000000`, `"max":1700000300000`)
}

func TestChartAPI_SetYAxisRange_updatesAxisBoundsAtRuntime(t *testing.T) {
	t.Parallel()
	// Runtime equivalent of WithYAxisRange — for adaptive-scale
	// streams that need to widen/narrow the y bounds as data evolves.
	fireChartAction(t, "RangeY",
		"setOption", `"yAxis"`, `"min":0`, `"max":200`)
}

func TestChartAPI_AppendXY_streamsSinglePointWithoutManualBoxing(t *testing.T) {
	t.Parallel()
	// AppendXY is the terse dense-streaming entry point — no outer
	// [][]any nor inner []any wraps in the call site.
	fireChartAction(t, "AppendXYTyped",
		"appendData", "seriesIndex:0", "[[1700000000000,42.5]]")
}

func TestChartAPI_AppendPoint_singlePointSugar(t *testing.T) {
	t.Parallel()
	fireChartAction(t, "AppendOne", "appendData", "seriesIndex:0", "[[99,7]]")
}

func TestChartAPI_AppendPoint_targetsTheGivenSeriesAndData(t *testing.T) {
	t.Parallel()
	fireChartAction(t, "AppendOneAlt", "appendData", "seriesIndex:1", "[[12,34]]")
}

func TestChartAPI_SetSeries_zeroArgsClearsSeries(t *testing.T) {
	t.Parallel()
	// replaceMerge ensures existing series are dropped rather than
	// index-merged with the empty payload (default echarts behavior).
	fireChartAction(t, "ClearSer", "setOption", `"series":[]`, "replaceMerge")
}

func TestChartAPI_SetTitle_emitsTitleUpdate(t *testing.T) {
	t.Parallel()
	fireChartAction(t, "Retitle",
		"setOption", `"title"`, `"text"`, "Latency (p99)")
}

func TestChartAPI_SetLoading_togglesEchartsBuiltInSpinner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action string
		needle string
	}{
		{"true shows the spinner", "Spin", "showLoading"},
		{"false hides the spinner", "StopSpin", "hideLoading"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fireChartAction(t, tc.action, tc.needle)
		})
	}
}

func TestChartAPI_SetSubtitle_emitsSubtextUpdate(t *testing.T) {
	t.Parallel()
	fireChartAction(t, "Resubtitle",
		"setOption", `"title"`, `"subtext"`, "updated 12:04:33")
}

func TestChartAPI_SetTheme_disposesAndReinitsPreservingOptions(t *testing.T) {
	t.Parallel()
	fireChartAction(t, "Darken",
		"getOption", "dispose", "echarts.init", `"dark"`)
}

func TestChartAPI_Resize_emitsExplicitResizeCall(t *testing.T) {
	t.Parallel()
	fireChartAction(t, "Reflow", ".resize()")
}

func TestChartAPI_SetLegend_togglesLegendVisibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		action  string
		needles []string
	}{
		{"show emits legend.show=true", "ShowLegend",
			[]string{"setOption", `"legend"`, `"show":true`}},
		{"hide emits legend.show=false", "HideLegend",
			[]string{"setOption", `"legend"`, `"show":false`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fireChartAction(t, tc.action, tc.needles...)
		})
	}
}

func TestChartAPI_Clear_emitsChartClearCall(t *testing.T) {
	t.Parallel()
	fireChartAction(t, "Wipe", ".clear()")
}

func TestChartAPI_Dispose_releasesInstanceAndNullsGlobal(t *testing.T) {
	t.Parallel()
	// The ResizeObserver also has to be disconnected, otherwise window
	// resizes fire a callback that touches the disposed instance.
	fireChartAction(t, "Tear", ".dispose()", ".disconnect()")
}

func TestChartAPI_SetTheme_preservesGroupAcrossSwap(t *testing.T) {
	t.Parallel()
	// echarts.init's theme swap drops the chart's group assignment, so
	// SetTheme must capture _e.c.group before dispose and re-attach it
	// after re-init — otherwise WithGroup-linked dashboards lose
	// tooltip sync on a dark-mode toggle. Asserting the JS shape
	// (capture + connect-with-var) proves the wiring is in place
	// regardless of whether THIS chart happens to have a group.
	fireChartAction(t, "Darken",
		"dispose", "echarts.init", "_g=_e.c.group", "echarts.connect(_g)")
}

