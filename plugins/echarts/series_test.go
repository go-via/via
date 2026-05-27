package echarts_test

import (
	"testing"

	"github.com/go-via/via/plugins/echarts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTail_returnsLastNPoints(t *testing.T) {
	t.Parallel()

	// Sliding-window pattern: server keeps the full history but the
	// chart only renders the most recent N points. Tail wraps the
	// `data[len(data)-n:]` slice with the bounds check users always
	// have to write — "if I have fewer than n points, return them all."
	full := [][]any{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}}

	assert.Equal(t, [][]any{{3, 4}, {4, 5}}, echarts.Tail(full, 2),
		"Tail must return the last n entries when len >= n")
	assert.Equal(t, full, echarts.Tail(full, 10),
		"Tail must return the full slice when n exceeds length, not panic")
	assert.Equal(t, full, echarts.Tail(full, len(full)),
		"Tail with n equal to length must return the original slice")
	assert.Equal(t, [][]any{}, echarts.Tail(full, 0),
		"Tail with n=0 must return empty, not nil and not the whole slice")
}

func TestPoints_zipsParallelSlicesIntoXYPairs(t *testing.T) {
	t.Parallel()

	// The typical dense time-series shape: int64 ms timestamps in one
	// slice, float64 values in another. Points zips them into the
	// [][]any pair shape that Line/Bar/Scatter expect, removing the
	// boilerplate zip loop callers otherwise write.
	ts := []int64{1700000000000, 1700000060000, 1700000120000}
	vs := []float64{12.5, 18.0, 14.25}
	got := echarts.Points(ts, vs)

	require.Equal(t, 3, len(got),
		"Points must produce one pair per element in the input slices")
	assert.Equal(t, []any{int64(1700000000000), 12.5}, got[0],
		"first pair must zip xs[0] and ys[0]")
	assert.Equal(t, []any{int64(1700000060000), 18.0}, got[1])
	assert.Equal(t, []any{int64(1700000120000), 14.25}, got[2])
}

func TestPoints_genericOverMixedNumericTypes(t *testing.T) {
	t.Parallel()

	// Generic over X and Y so callers aren't forced into a specific
	// numeric type — e.g. float64 timestamps for sub-millisecond
	// pyhsical sims or int values for counts.
	got := echarts.Points([]float64{0.5, 1.5}, []int{7, 9})
	assert.Equal(t, []any{0.5, 7}, got[0])
	assert.Equal(t, []any{1.5, 9}, got[1])
}

func TestPoints_panicsOnLengthMismatch(t *testing.T) {
	t.Parallel()

	// Length mismatch is a programmer bug — silently truncating or
	// padding would surface as a chart with confusingly-shifted data.
	assert.Panics(t, func() {
		_ = echarts.Points([]int64{1, 2, 3}, []float64{1.0, 2.0})
	}, "Points must panic when xs and ys have different lengths")
}

func TestStacked_setsStackFieldOnSeries(t *testing.T) {
	t.Parallel()

	// Stacked series share a stack name so echarts draws each series'
	// area on top of the others rather than overlapping. Standard for
	// breakdown time-series — e.g. CPU usage stacked by core, RAM by
	// process group.
	user := echarts.Line("user", [][]any{{0, 10}, {1, 12}}, echarts.Stacked("usage"))
	sys := echarts.Line("system", [][]any{{0, 5}, {1, 7}}, echarts.Stacked("usage"))

	assert.Equal(t, "usage", user["stack"],
		"Stacked option must set the stack field on a series")
	assert.Equal(t, "usage", sys["stack"],
		"the same stack name must apply equally across siblings")
	// Base fields still survive composition.
	assert.Equal(t, "line", user["type"])
	assert.Equal(t, "user", user["name"])
}

func TestMarkArea_addsShadedXRangeOverlay(t *testing.T) {
	t.Parallel()

	// Highlight a time range (incident window, maintenance period) with
	// a shaded background overlay. echarts markArea is a `[][]any`
	// shape — each entry is a pair of [{xAxis: start}, {xAxis: end}].
	got := echarts.Line("CPU", [][]any{{0, 1}},
		echarts.MarkArea("incident", int64(1700000000000), int64(1700000300000)),
	)

	ma := got["markArea"].(map[string]any)
	data := ma["data"].([]any)
	require.Equal(t, 1, len(data), "one MarkArea call must produce one range entry")
	pair := data[0].([]any)
	require.Equal(t, 2, len(pair), "each markArea entry must hold a start/end pair")
	start := pair[0].(map[string]any)
	end := pair[1].(map[string]any)
	assert.Equal(t, int64(1700000000000), start["xAxis"], "start xAxis must reach the entry")
	assert.Equal(t, "incident", start["name"], "the label rides on the start of the pair")
	assert.Equal(t, int64(1700000300000), end["xAxis"], "end xAxis must reach the entry")
}

func TestMarkArea_multipleCallsCompose(t *testing.T) {
	t.Parallel()

	// Multiple incident windows on the same chart accumulate rather
	// than overwrite — same composition semantics as MarkLine.
	got := echarts.Line("CPU", [][]any{{0, 1}},
		echarts.MarkArea("a", 100, 200),
		echarts.MarkArea("b", 300, 400),
	)

	data := got["markArea"].(map[string]any)["data"].([]any)
	require.Equal(t, 2, len(data), "two MarkArea calls must accumulate")
	assert.Equal(t, "a", data[0].([]any)[0].(map[string]any)["name"])
	assert.Equal(t, "b", data[1].([]any)[0].(map[string]any)["name"])
}

func TestMarkLine_multipleCallsComposeIntoOneMarkLineEntry(t *testing.T) {
	t.Parallel()

	// Real dashboards stack thresholds — "warn at 80, crit at 95" both
	// rendered simultaneously. Multiple MarkLine calls on a single
	// series must accumulate into one markLine.data array rather than
	// the later call overwriting the earlier one.
	got := echarts.Line("CPU", [][]any{{0, 70}},
		echarts.MarkLine("warn", 80),
		echarts.MarkLine("crit", 95),
	)

	ml := got["markLine"].(map[string]any)
	data := ml["data"].([]any)
	require.Equal(t, 2, len(data),
		"two MarkLine calls must produce two markLine.data entries")
	assert.Equal(t, "warn", data[0].(map[string]any)["name"],
		"first MarkLine's name must survive the second's addition")
	assert.Equal(t, "crit", data[1].(map[string]any)["name"],
		"second MarkLine's name must append, not replace")
}

func TestMarkLine_addsHorizontalThresholdLine(t *testing.T) {
	t.Parallel()

	// Dense monitoring charts need threshold overlays ("warn at 80%")
	// so the eye can spot breaches without zooming into individual
	// points. echarts' markLine config is nested-verbose; MarkLine
	// absorbs the boilerplate.
	got := echarts.Line("CPU", [][]any{{0, 70}, {1, 85}}, echarts.MarkLine("warn", 80))

	ml, ok := got["markLine"].(map[string]any)
	require.True(t, ok, "MarkLine must add a markLine map to the series")
	data, ok := ml["data"].([]any)
	require.True(t, ok && len(data) == 1, "markLine.data must hold one entry")
	entry := data[0].(map[string]any)
	assert.Equal(t, 80.0, entry["yAxis"],
		"the threshold value must reach the yAxis field of the markLine data entry")
	assert.Equal(t, "warn", entry["name"],
		"the threshold name must label the line in the chart legend/tooltip")
}

func TestYAxisIndex_routesSeriesToSecondaryYAxis(t *testing.T) {
	t.Parallel()

	// Combo charts use multiple yAxes — e.g. CPU% on the left,
	// throughput in MB/s on the right. Each series declares which
	// yAxis it belongs to via yAxisIndex.
	got := echarts.Line("throughput", [][]any{{0, 12}}, echarts.YAxisIndex(1))
	assert.Equal(t, 1, got["yAxisIndex"],
		"YAxisIndex must set the yAxisIndex field to route the series")
}

func TestSilent_disablesHoverInteraction(t *testing.T) {
	t.Parallel()

	// Reference series (target, baseline, context) shouldn't compete
	// with the primary metric for hover attention. `silent: true`
	// keeps the series visible but inert.
	got := echarts.Line("target", [][]any{{0, 80}}, echarts.Silent())
	assert.Equal(t, true, got["silent"],
		"Silent must set silent=true on the series")
}

func TestProgressive_enablesChunkedRendering(t *testing.T) {
	t.Parallel()

	// Progressive rendering chunks the canvas paint so the main thread
	// doesn't block on enormous datasets. Different from `Dense()`'s
	// LTTB sampling (which reduces points): progressive paints all
	// points but in chunks. Useful for 100k+ point series where
	// downsampling would lose meaningful detail.
	got := echarts.Scatter("samples", [][]any{{0, 1}}, echarts.Progressive(500, 3000))

	assert.Equal(t, 500, got["progressive"],
		"Progressive must set the per-chunk size")
	assert.Equal(t, 3000, got["progressiveThreshold"],
		"Progressive must set the threshold count above which chunking activates")
}

func TestConnectNulls_keepsLineContinuousAcrossGaps(t *testing.T) {
	t.Parallel()

	// Streaming data often has occasional null samples (sensor blip,
	// transient network drop). Without connectNulls, echarts breaks
	// the line at every gap — visually noisy for dense feeds where
	// the underlying trend stays smooth.
	got := echarts.Line("CPU", [][]any{{0, 1}, {1, nil}, {2, 3}}, echarts.ConnectNulls())
	assert.Equal(t, true, got["connectNulls"],
		"ConnectNulls must enable the connectNulls field on the series")
}

func TestEndLabel_showsLatestValueInline(t *testing.T) {
	t.Parallel()

	// Streaming dashboards want current values visible without
	// hovering — EndLabel attaches a floating label at the latest
	// data point of each line. Default formatter (empty string) just
	// shows the series name.
	plain := echarts.Line("CPU", [][]any{{0, 1}}, echarts.EndLabel(""))
	el := plain["endLabel"].(map[string]any)
	assert.Equal(t, true, el["show"], "EndLabel must enable the label")
	_, hasFormatter := el["formatter"]
	assert.False(t, hasFormatter,
		"empty formatter must leave the field unset so echarts uses its default")

	withFmt := echarts.Line("CPU", [][]any{{0, 1}}, echarts.EndLabel("{a}: {c}"))
	el2 := withFmt["endLabel"].(map[string]any)
	assert.Equal(t, "{a}: {c}", el2["formatter"],
		"non-empty formatter must reach the endLabel config")
}

func TestStepped_setsStepPosition(t *testing.T) {
	t.Parallel()

	// Step lines are how echarts renders state-change time-series —
	// the line stays flat between samples and steps at the position
	// (start | middle | end). "end" is the canonical choice for "value
	// held until next reading."
	got := echarts.Line("status", [][]any{{0, 0}, {1, 1}, {2, 0}}, echarts.Stepped("end"))

	assert.Equal(t, "end", got["step"],
		"Stepped must set the step field to the named position")
}

func TestSmoothed_enablesLineInterpolation(t *testing.T) {
	t.Parallel()

	// Smoothed flips echarts' line smoothing on — a frequent ask for
	// noisy dense time-series where the eye wants a flowing curve.
	got := echarts.Line("CPU", [][]any{{0, 1}}, echarts.Smoothed())

	assert.Equal(t, true, got["smooth"],
		"Smoothed must set smooth=true on the series")
}

func TestField_setsArbitraryEchartsSeriesField(t *testing.T) {
	t.Parallel()

	// Escape hatch for any echarts series field the typed helpers
	// don't cover yet — `smooth`, `xAxisIndex`, `emphasis`, etc.
	got := echarts.Line("CPU", [][]any{{0, 1}},
		echarts.Field("smooth", true),
		echarts.Field("xAxisIndex", 1),
	)

	assert.Equal(t, true, got["smooth"],
		"Field must set the named key on the series")
	assert.Equal(t, 1, got["xAxisIndex"],
		"multiple Field calls compose; each sets its own key")
	// Base fields still survive.
	assert.Equal(t, "line", got["type"])
}

func TestSymbol_setsSymbolSize(t *testing.T) {
	t.Parallel()

	// Symbol customises the per-point marker size in pixels. Pairs
	// with Color and LineWidth for full visual control per series.
	line := echarts.Line("CPU", [][]any{{0, 1}}, echarts.Symbol(4))
	sc := echarts.Scatter("samples", [][]any{{0, 1}}, echarts.Symbol(1))

	assert.Equal(t, 4, line["symbolSize"],
		"Symbol must set symbolSize on a Line series")
	assert.Equal(t, 1, sc["symbolSize"],
		"Symbol must apply equally to Scatter series")
}

func TestLineWidth_setsLineStyleWidth(t *testing.T) {
	t.Parallel()

	// Dense dashboards distinguish many overlapping series via
	// thickness as a second visual axis. The width lives under
	// lineStyle in echarts; LineWidth saves the nested-map plumbing.
	got := echarts.Line("CPU", [][]any{{0, 1}}, echarts.LineWidth(3))

	ls, ok := got["lineStyle"].(map[string]any)
	require.True(t, ok, "LineWidth must add a lineStyle map")
	assert.Equal(t, 3, ls["width"],
		"LineWidth must set lineStyle.width to the px value")
}

func TestDense_appliesPerTypePerfDefaults(t *testing.T) {
	t.Parallel()

	// Dense is type-aware: for line it disables symbols + enables
	// LTTB sampling; for scatter it switches to large-render and
	// shrinks the symbol. Non-applicable types are left untouched.
	line := echarts.Line("CPU", [][]any{{0, 1}}, echarts.Dense())
	assert.Equal(t, false, line["showSymbol"], "Dense on Line must disable showSymbol")
	assert.Equal(t, "lttb", line["sampling"], "Dense on Line must enable LTTB sampling")

	sc := echarts.Scatter("samples", [][]any{{0, 1}}, echarts.Dense())
	assert.Equal(t, true, sc["large"], "Dense on Scatter must enable large-render mode")
	assert.Equal(t, 2, sc["symbolSize"], "Dense on Scatter must shrink the symbol")

	bar := echarts.Bar("bar", [][]any{{0, 1}}, echarts.Dense())
	_, hasShowSymbol := bar["showSymbol"]
	_, hasLarge := bar["large"]
	assert.False(t, hasShowSymbol, "Dense must not touch line-specific fields on a Bar")
	assert.False(t, hasLarge, "Dense must not touch scatter-specific fields on a Bar")
}

func TestFilled_addsAreaStyle(t *testing.T) {
	t.Parallel()
	got := echarts.Line("CPU", [][]any{{0, 1}}, echarts.Filled())
	assert.Equal(t, map[string]any{}, got["areaStyle"],
		"Filled must enable area fill with the empty {} marker echarts uses for auto-derived styling")
}

func TestLineDense_acceptsExtraSeriesOptions(t *testing.T) {
	t.Parallel()
	// LineDense must thread extra opts through after applying its own
	// dense defaults — users layer Color/LineWidth/Stacked on top.
	got := echarts.LineDense("CPU", [][]any{{0, 1}}, echarts.Color("#ff6b6b"))
	assert.Equal(t, false, got["showSymbol"], "dense defaults must still apply")
	assert.Equal(t, "lttb", got["sampling"])
	assert.Equal(t, "#ff6b6b", got["color"], "extra option must layer on top")
}

func TestColor_setsPerSeriesColor(t *testing.T) {
	t.Parallel()

	// Per-series colors keep dashboard palettes consistent without
	// relying on the chart's positional `color` array — each series
	// declares its own.
	line := echarts.Line("CPU", [][]any{{0, 1}}, echarts.Color("#ff6b6b"))
	bar := echarts.Bar("Hits", [][]any{{0, 1}}, echarts.Color("#4ecdc4"))

	assert.Equal(t, "#ff6b6b", line["color"],
		"Color option must set the series-level color")
	assert.Equal(t, "#4ecdc4", bar["color"],
		"Color must apply to non-line series too")
}

func TestSeriesOption_threadedThroughEveryBaseHelper(t *testing.T) {
	t.Parallel()

	// Variadic opts on each helper must actually apply — guards
	// against a regression that drops the variadic on one of them.
	tests := []struct {
		name string
		got  map[string]any
	}{
		{"Line", echarts.Line("a", [][]any{{0, 1}}, echarts.Stacked("g"))},
		{"Bar", echarts.Bar("a", [][]any{{0, 1}}, echarts.Stacked("g"))},
		{"Scatter", echarts.Scatter("a", [][]any{{0, 1}}, echarts.Stacked("g"))},
		{"Heatmap", echarts.Heatmap("a", [][]any{{0, 1}}, echarts.Stacked("g"))},
		{"Pie", echarts.Pie("a", []map[string]any{{"name": "x", "value": 1}}, echarts.Stacked("g"))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, "g", tc.got["stack"],
				"%s must apply SeriesOption args", tc.name)
		})
	}
}

func TestSeriesHelpers_acceptVariadicOptionsBackwardCompatibly(t *testing.T) {
	t.Parallel()

	// Existing callers without options must still get the original
	// shape — the variadic addition is purely additive.
	plain := echarts.Line("CPU", [][]any{{0, 1}})
	_, hasStack := plain["stack"]
	assert.False(t, hasStack,
		"Line without options must not carry a stack field")
}

func TestLineAreaDense_addsAreaFillOnTopOfDenseDefaults(t *testing.T) {
	t.Parallel()

	// Area-filled line + dense perf defaults — the standard shape for
	// CPU/RAM/throughput live charts where the filled area gives a
	// stronger visual sense of magnitude than a bare line.
	pts := [][]any{{0, 12}, {1, 18}}
	lad := echarts.LineAreaDense("CPU", pts)

	assert.Equal(t, "line", lad["type"],
		"LineAreaDense must still produce a line series")
	assert.Equal(t, "CPU", lad["name"])
	assert.Equal(t, pts, lad["data"])
	assert.Equal(t, false, lad["showSymbol"],
		"LineAreaDense must inherit dense perf defaults: no per-point symbols")
	assert.Equal(t, "lttb", lad["sampling"],
		"LineAreaDense must inherit dense perf defaults: LTTB sampling")
	assert.Equal(t, map[string]any{}, lad["areaStyle"],
		"LineAreaDense must enable the area fill — empty {} unlocks echarts' default styling")
}

func TestLineDense_setsLTTBSamplingAndHidesSymbols(t *testing.T) {
	t.Parallel()

	// Dense time-series (thousands of points) crush browser perf if
	// echarts renders every point as a DOM symbol and skips sampling.
	// LineDense bakes in the two options every dense-data user
	// eventually discovers: showSymbol: false + sampling: "lttb".
	pts := [][]any{{0, 1}, {1, 2}, {2, 3}}
	ld := echarts.LineDense("CPU", pts)

	assert.Equal(t, "line", ld["type"],
		"LineDense must still produce a line series — only the rendering defaults change")
	assert.Equal(t, "CPU", ld["name"])
	assert.Equal(t, pts, ld["data"])
	assert.Equal(t, false, ld["showSymbol"],
		"LineDense must hide per-point symbols so 10k points don't render 10k DOM nodes")
	assert.Equal(t, "lttb", ld["sampling"],
		"LineDense must enable LTTB sampling so the renderer skips visually-redundant points")
}

func TestLine_andBar_returnSeriesOptionMaps(t *testing.T) {
	t.Parallel()

	linePts := [][]any{{0, 12}, {1, 18}}
	line := echarts.Line("CPU", linePts)
	assert.Equal(t, "line", line["type"])
	assert.Equal(t, "CPU", line["name"])
	assert.Equal(t, linePts, line["data"],
		"Line must preserve [][]any data unchanged for echarts to read")

	barPts := [][]any{{0, 7}, {1, 9}}
	bar := echarts.Bar("Hits", barPts)
	assert.Equal(t, "bar", bar["type"])
	assert.Equal(t, "Hits", bar["name"])
	assert.Equal(t, barPts, bar["data"],
		"Bar must preserve [][]any data unchanged for echarts to read")
}

func TestScatterDense_enablesLargeRenderModeAndShrinksSymbol(t *testing.T) {
	t.Parallel()

	// Dense scatter (event clouds, sample distributions) needs echarts'
	// `large: true` mode to switch from per-point rendering to a
	// batched canvas path. A smaller symbol keeps the overlap from
	// turning into a solid blob.
	pts := [][]any{{1.0, 2.0}, {1.1, 2.1}, {1.2, 2.0}}
	sd := echarts.ScatterDense("samples", pts)

	assert.Equal(t, "scatter", sd["type"],
		"ScatterDense must still produce a scatter series")
	assert.Equal(t, "samples", sd["name"])
	assert.Equal(t, pts, sd["data"])
	assert.Equal(t, true, sd["large"],
		"ScatterDense must enable large-render mode for batched canvas drawing")
	assert.Equal(t, 2, sd["symbolSize"],
		"ScatterDense must shrink the per-point symbol so dense clouds stay readable")
}

func TestScatter_returnsSeriesOptionMap(t *testing.T) {
	t.Parallel()

	pts := [][]any{{1.5, 3.2}, {2.1, 4.8}}
	s := echarts.Scatter("Galaxy", pts)
	assert.Equal(t, "scatter", s["type"])
	assert.Equal(t, "Galaxy", s["name"])
	assert.Equal(t, pts, s["data"],
		"Scatter must preserve [][]any data unchanged for echarts to read")
}

func TestHeatmap_returnsSeriesOptionMapWithXYVTriples(t *testing.T) {
	t.Parallel()

	cells := [][]any{
		{0, 0, 5},
		{1, 0, 10},
		{0, 1, 7},
	}
	h := echarts.Heatmap("Activity", cells)
	assert.Equal(t, "heatmap", h["type"])
	assert.Equal(t, "Activity", h["name"])
	assert.Equal(t, cells, h["data"],
		"Heatmap must preserve [x,y,value] triples unchanged for echarts to read")
}

func TestPie_returnsSeriesOptionMapWithSliceData(t *testing.T) {
	t.Parallel()

	slices := []map[string]any{
		{"name": "A", "value": 30},
		{"name": "B", "value": 70},
	}
	p := echarts.Pie("Breakdown", slices)
	assert.Equal(t, "pie", p["type"])
	assert.Equal(t, "Breakdown", p["name"])
	assert.Equal(t, slices, p["data"],
		"Pie must preserve the {name,value} slice shape echarts expects")
}
