package echarts_test

import (
	"strings"
	"testing"

	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renderH(t *testing.T, node h.H) string {
	t.Helper()
	var sb strings.Builder
	require.NoError(t, node.Render(&sb))
	return sb.String()
}

func TestChartOptions_propagateIntoMountedNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opts     []echarts.ChartOption
		contains []string
	}{
		{
			name: "WithElementID overrides the auto-id",
			opts: []echarts.ChartOption{echarts.WithElementID("cpu-chart")},
			// The id must reach BOTH the container's HTML attribute and
			// the init script's getElementById call — the latter is what
			// binds echarts to the DOM, so a divergence between the two
			// would render the chart but never attach to it.
			contains: []string{`id="cpu-chart"`, `getElementById("cpu-chart")`},
		},
		{
			name:     "WithTitle surfaces in the init script",
			opts:     []echarts.ChartOption{echarts.WithTitle("CPU usage")},
			contains: []string{`"CPU usage"`},
		},
		{
			name: "WithDimensions sets inline style width and height",
			opts: []echarts.ChartOption{
				echarts.WithDimensions("75%", "400px"),
			},
			contains: []string{"width:75%", "height:400px"},
		},
		{
			name: "WithThemeOverride flips the init theme to dark",
			opts: []echarts.ChartOption{echarts.WithThemeOverride(echarts.ThemeDark)},
			// Match the quoted JS literal echarts.init(_el, "dark") so
			// the assertion can't pass by accident on any 4-char "dark"
			// substring that lands elsewhere in the rendered output.
			contains: []string{`"dark"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := echarts.NewChart(tc.opts...)
			out := renderH(t, c.Mount())
			for _, want := range tc.contains {
				assert.Contains(t, out, want,
					"chart option must surface inside the Mount node")
			}
		})
	}
}

func TestChartOptions_WithClass_setsContainerClassAttribute(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("ch"),
		echarts.WithClass("dash-chart", "rounded"),
	)
	out := renderH(t, c.Mount())

	assert.Contains(t, out, `class="dash-chart rounded"`,
		"WithClass must put the joined class string on the container div")
}

func TestChartOptions_WithClass_omittedWhenUnset(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("ch"))
	out := renderH(t, c.Mount())

	assert.NotContains(t, out, `class="`,
		"charts without WithClass must not emit any class attribute")
}

func TestChartOptions_WithGroup_linksChartIntoSharedGroup(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("cpu"),
		echarts.WithGroup("dashboard"),
	)
	out := renderH(t, c.Mount())

	assert.Contains(t, out, `.group="dashboard"`,
		"WithGroup must set the echarts instance group")
	assert.Contains(t, out, `echarts.connect("dashboard")`,
		"WithGroup must register the group so charts inside it link up")
}

func TestChartOptions_WithGroup_omittedWhenUnset(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("plain"))
	out := renderH(t, c.Mount())

	assert.NotContains(t, out, "echarts.connect",
		"charts without WithGroup must not pull in connect() — that would link every chart on the page")
	assert.NotContains(t, out, ".group=",
		"charts without WithGroup must not set the group property")
}

func TestChartOptions_WithInitialOption_appliedAfterDefaultInit(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("pie"),
		echarts.WithInitialOption(map[string]any{
			"series": []any{map[string]any{"type": "pie", "name": "slices"}},
		}),
	)
	out := renderH(t, c.Mount())

	assert.Contains(t, out, `"type":"pie"`,
		"WithInitialOption payload must reach the rendered init script")

	// Default init runs first so its category xAxis exists; the user
	// initial option must come AFTER so it can override / extend.
	defaultIdx := strings.Index(out, "category")
	userIdx := strings.Index(out, `"type":"pie"`)
	require.NotEqual(t, -1, defaultIdx, "default init must still render")
	require.NotEqual(t, -1, userIdx, "user initial option must render")
	assert.Less(t, defaultIdx, userIdx,
		"WithInitialOption must be applied after the default setOption call so user values win")
}

func TestChartOptions_WithInitialOption_panicsOnUnmarshalableValue(t *testing.T) {
	t.Parallel()

	// A non-marshalable value (channel/func/cycle) inside the options
	// map is a programmer bug. Surfacing the panic at WithInitialOption
	// time points the stack trace at the bad call site rather than at a
	// silent `_c.setOption(null)` in the rendered script.
	assert.Panics(t, func() {
		echarts.WithInitialOption(map[string]any{"x": make(chan int)})
	}, "WithInitialOption must reject unmarshalable values at construction")
}

func TestChartOptions_WithInitialOption_nilOrEmptyIsNoop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opt  map[string]any
	}{
		{"nil map", nil},
		{"empty map", map[string]any{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := echarts.NewChart(
				echarts.WithElementID("plain"),
				echarts.WithInitialOption(tc.opt),
			)
			out := renderH(t, c.Mount())

			// The default init renders exactly one .setOption( call.
			// A non-empty WithInitialOption would add a second; nil/empty must not.
			assert.Equal(t, 1, strings.Count(out, ".setOption("),
				"WithInitialOption with %s must not emit a second setOption call", tc.name)
		})
	}
}

func TestChartOptions_WithDimensions_partialEmptyFallsBackPerSide(t *testing.T) {
	t.Parallel()

	// Users sometimes want to override just one dimension (e.g.
	// stretch the chart wide but keep the default height). Mount uses
	// cmp.Or per side, so an empty arg falls back to that side's
	// default without dragging the other side along.
	c := echarts.NewChart(
		echarts.WithElementID("ch"),
		echarts.WithDimensions("75%", ""),
	)
	out := renderH(t, c.Mount())

	assert.Contains(t, out, "width:75%",
		"explicit width must reach the inline style")
	assert.Contains(t, out, "height:300px",
		"empty height must fall through to the per-side default")
}

func TestThemeConstants_matchEchartsWireFormat(t *testing.T) {
	t.Parallel()
	// echarts.init(dom, "light"|"dark") expects the lowercase string.
	// If these constants get renamed/recased silently, charts would
	// fall through to whatever echarts treats as "unknown theme" with
	// no compile-time complaint from downstream code.
	assert.Equal(t, "light", string(echarts.ThemeLight),
		"ThemeLight must serialise as the lowercase string echarts recognises")
	assert.Equal(t, "dark", string(echarts.ThemeDark),
		"ThemeDark must serialise as the lowercase string echarts recognises")
}

func TestChartOptions_WithClass_panicsOnWhitespaceInClassName(t *testing.T) {
	t.Parallel()

	// HTML's class attribute is whitespace-separated, so a single arg
	// like "foo bar" silently becomes two classes "foo" and "bar".
	// That's almost never what the caller meant — they should pass two
	// args instead. Surface this at construction so the wrong intent
	// doesn't ship.
	assert.Panics(t, func() {
		echarts.WithClass("foo bar", "baz")
	}, "WithClass must reject whitespace inside a single class arg")
	assert.Panics(t, func() {
		echarts.WithClass("a\tb")
	}, "WithClass must reject tab characters")
}

func TestChartOptions_WithRightYAxis_addsSecondaryAxisOnTheRight(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("combo"),
		echarts.WithYAxisFormat("{value} %"),
		echarts.WithRightYAxis("{value} MB/s"),
	)
	out := renderH(t, c.Mount())

	// Combo charts route series to yAxisIndex 0 (left, percent) or 1
	// (right, throughput). The yAxis field must serialize as an array
	// with both axes when WithRightYAxis is set.
	assert.Contains(t, out, "yAxis: [",
		"WithRightYAxis must flip yAxis to an array shape")
	assert.Contains(t, out, "position:'right'",
		"the secondary axis must be positioned on the right side")
	assert.Contains(t, out, `formatter:"{value} MB/s"`,
		"the right-axis formatter must reach the rendered config")
	assert.Contains(t, out, `formatter:"{value} %"`,
		"the existing left-axis formatter must coexist")
}

func TestChartOptions_WithRightYAxis_omittedKeepsSingleYAxis(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("plain"))
	out := renderH(t, c.Mount())

	assert.NotContains(t, out, "yAxis: [",
		"plain chart must keep yAxis as a single object, not an array")
	assert.NotContains(t, out, "position:'right'",
		"no right-side axis without WithRightYAxis")
}

func TestChartOptions_WithYAxisFormat_addsLabelTemplate(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("net"),
		echarts.WithYAxisFormat("{value} KB/s"),
	)
	out := renderH(t, c.Mount())

	// The template appears in the yAxis.axisLabel.formatter field so
	// echarts substitutes {value} per tick. Streaming charts gain
	// unit-labeled axes ("12 KB/s") instead of bare numbers.
	assert.Contains(t, out, `formatter:"{value} KB/s"`,
		"WithYAxisFormat must set axisLabel.formatter to the template")
}

func TestChartOptions_WithYAxisFormat_omittedKeepsBareNumbers(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("plain"))
	out := renderH(t, c.Mount())

	assert.NotContains(t, out, "formatter",
		"plain chart must not carry a yAxis formatter")
}

func TestChartOptions_WithPalette_setsChartLevelColorArray(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("dash"),
		echarts.WithPalette("#ff6b6b", "#4ecdc4", "#45b7d1"),
	)
	out := renderH(t, c.Mount())

	// echarts assigns the chart-level `color` array to series in order;
	// each entry must reach the rendered init script.
	assert.Contains(t, out, `"#ff6b6b"`,
		"first palette color must reach the chart color array")
	assert.Contains(t, out, `"#4ecdc4"`,
		"second palette color must reach the chart color array")
	assert.Contains(t, out, `"#45b7d1"`,
		"third palette color must reach the chart color array")
}

func TestChartOptions_WithPalette_omittedKeepsEchartsDefault(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("plain"))
	out := renderH(t, c.Mount())

	assert.NotContains(t, out, "color: [",
		"plain chart must not inject a custom color array")
}

func TestChartOptions_WithCompactGrid_shrinksEchartsDefaultPadding(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("cpu"),
		echarts.WithCompactGrid(),
	)
	out := renderH(t, c.Mount())

	// echarts' default grid leaves ~60px top + ~60px bottom — wasted
	// in dense dashboards where 8 charts share the viewport. The
	// compact preset trims the plot inset on all sides so the data
	// region fills the container.
	assert.Contains(t, out, "grid:",
		"WithCompactGrid must surface a grid config in the init script")
	assert.Contains(t, out, "containLabel: true",
		"containLabel ensures axis labels stay inside the container even with tight padding")
}

func TestChartOptions_WithCompactGrid_omittedKeepsDefaultPadding(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("plain"))
	out := renderH(t, c.Mount())

	assert.NotContains(t, out, "grid:",
		"plain chart must not pull in a custom grid")
}

func TestChartOptions_WithDataZoom_addsInteractiveZoomAndSlider(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("ts"),
		echarts.WithDataZoom(),
	)
	out := renderH(t, c.Mount())

	// echarts dataZoom is the standard escape hatch for long
	// time-series — pinch/scroll inside the chart, plus a slider
	// underneath for big jumps. The 'inside' + 'slider' pair is the
	// canonical combo.
	assert.Contains(t, out, "dataZoom",
		"WithDataZoom must surface a dataZoom config in the init script")
	assert.Contains(t, out, "inside",
		"the inside-zoom variant must be enabled for pinch/scroll interactions")
	assert.Contains(t, out, "slider",
		"the slider variant must be enabled for big-jump navigation")
}

func TestChartOptions_WithDataZoom_omittedKeepsChartStatic(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("plain"))
	out := renderH(t, c.Mount())

	assert.NotContains(t, out, "dataZoom",
		"plain chart must not pull in dataZoom — adds zoom UI nobody asked for")
}

func TestChartOptions_WithYAxisRange_pinsAxisBoundsToPreventRescaleJitter(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("cpu"),
		echarts.WithYAxisRange(0, 100),
	)
	out := renderH(t, c.Mount())

	// Streaming percentage metrics (CPU%, RAM%, disk%) need fixed
	// bounds — without them the chart's yAxis auto-rescales every
	// tick when a new max or min point arrives, producing visible
	// jitter. The min/max must reach the rendered yAxis config.
	assert.Contains(t, out, "min: 0",
		"WithYAxisRange must pin the yAxis lower bound")
	assert.Contains(t, out, "max: 100",
		"WithYAxisRange must pin the yAxis upper bound")
}

func TestChartOptions_WithYAxisRange_omittedKeepsAutoScale(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("plain"))
	out := renderH(t, c.Mount())

	// Without WithYAxisRange, no min/max should be injected — echarts'
	// auto-scaling default applies. Asserting on the yAxis-adjacent
	// substring avoids false-matching min/max occurring elsewhere.
	assert.NotContains(t, out, "min: ",
		"plain chart must not carry yAxis min")
	assert.NotContains(t, out, "max: ",
		"plain chart must not carry yAxis max")
}

func TestChartOptions_WithLegend_setsExplicitInitialVisibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		visible bool
		want    string
	}{
		{"hide", false, `legend: {show:false}`},
		{"show", true, `legend: {show:true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := echarts.NewChart(
				echarts.WithElementID("c"),
				echarts.WithLegend(tc.visible),
			)
			out := renderH(t, c.Mount())
			assert.Contains(t, out, tc.want,
				"WithLegend(%v) must set legend.show explicitly", tc.visible)
		})
	}
}

func TestChartOptions_WithLegend_omittedLeavesEchartsDefault(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("plain"))
	out := renderH(t, c.Mount())

	assert.NotContains(t, out, "legend: ",
		"plain chart must not inject any legend config — echarts default applies")
}

func TestChartOptions_WithCrosshair_addsCrossAxisPointer(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("ts"),
		echarts.WithCrosshair(),
	)
	out := renderH(t, c.Mount())

	// Crosshair tooltips render both a vertical (x) and horizontal (y)
	// guide at the cursor — readable values off either axis without
	// hovering a specific data point.
	assert.Contains(t, out, "axisPointer",
		"WithCrosshair must add an axisPointer config")
	assert.Contains(t, out, "'cross'",
		"the axisPointer type must be 'cross' for the dual-axis guide")
}

func TestChartOptions_WithCrosshair_omittedKeepsDefaultTooltip(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("plain"))
	out := renderH(t, c.Mount())

	assert.NotContains(t, out, "axisPointer",
		"plain chart must not pull in axisPointer config")
}

func TestChartOptions_WithTimeAxis_switchesXAxisToTimeAndAddsAxisTooltip(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(
		echarts.WithElementID("ts"),
		echarts.WithTimeAxis(),
	)
	out := renderH(t, c.Mount())

	// Dense time-series needs xAxis.type=time so echarts interpolates
	// millisecond timestamps instead of treating each one as a discrete
	// category label. axis-trigger tooltip is the conventional partner
	// — hovering anywhere reveals values across all series at that x.
	assert.Contains(t, out, "type: 'time'",
		"WithTimeAxis must flip xAxis.type from the default 'category' to 'time'")
	assert.NotContains(t, out, "type: 'category'",
		"the default category xAxis must not also remain")
	assert.Contains(t, out, "tooltip:",
		"WithTimeAxis must add an axis-trigger tooltip")
	assert.Contains(t, out, "axis",
		"tooltip trigger must be axis")
}

func TestChartOptions_WithTimeAxis_omittedKeepsCategoryDefault(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("plain"))
	out := renderH(t, c.Mount())

	assert.Contains(t, out, "type: 'category'",
		"without WithTimeAxis the default category xAxis must remain")
	assert.NotContains(t, out, "type: 'time'",
		"time-axis JS must not leak into charts that didn't request it")
}

func TestChartOptions_WithElementID_panicsOnWhitespace(t *testing.T) {
	t.Parallel()

	// HTML5 lets an id contain whitespace, but CSS selectors (`#foo`)
	// and developer expectations break silently the moment one slips
	// through — getElementById finds the element but no `#foo bar`
	// CSS rule will. Panic at construction so the call site shows
	// in the stack trace rather than the failure surfacing as
	// "my chart styling does nothing."
	cases := []string{"has space", "tab\there", "new\nline"}
	for _, id := range cases {
		assert.Panics(t, func() {
			echarts.WithElementID(id)
		}, "WithElementID must reject %q", id)
	}
}

func TestChartMount_doesNotPolluteWindowWithPerChartGlobals(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart(echarts.WithElementID("c1"))
	out := renderH(t, c.Mount())

	// Each chart used to declare two top-level `var` identifiers
	// (echart_N + echart_ro_N), so N charts on a page meant 2*N globals
	// hoisted onto window. The init script must register the instance
	// through a shared namespace instead.
	assert.NotContains(t, out, "var echart_",
		"Mount must not declare per-chart `var echart_N` globals")
	assert.NotContains(t, out, "var echart_ro_",
		"Mount must not declare per-chart ResizeObserver globals")
	assert.NotContains(t, out, "window.echart_",
		"Mount must not assign through window.echart_N (still pollutes)")
	assert.Contains(t, out, "__viaCharts",
		"Mount must register chart + observer via the shared registry")
}
