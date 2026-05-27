package echarts

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/go-via/via/h"
)

// EChartsTheme represents the chart theme variant.
type EChartsTheme string

const (
	ThemeLight EChartsTheme = "light"
	ThemeDark  EChartsTheme = "dark"
)

// chartCounter provides deterministic, unique IDs across chart instances.
var chartCounter atomic.Uint64

// Chart represents an ECharts component configuration. Construct with
// NewChart, render with Mount, then push runtime updates via the typed
// methods (SetSeries / SetTitle / AppendData / SetTheme / …) or the
// SetOption escape hatch for anything the typed methods don't cover.
type Chart struct {
	seq         uint64
	elementID   string
	title       string
	width       string
	height      string
	theme       EChartsTheme
	initialOpts map[string]any
	group       string
	classes     []string
	timeAxis    bool
	yMin, yMax  *float64
	dataZoom    bool
	yFormat     string
	compactGrid bool
	palette     []string
	rightYFmt   string
	crosshair   bool
	legendVis   *bool
}

// ChartOption configures a Chart. Each option mutates the chart in place;
// NewChart applies them in argument order.
type ChartOption func(*Chart)

// WithElementID sets the element ID for the chart container. Panics if
// id contains ASCII whitespace — HTML5 permits such IDs but CSS
// selectors like `#foo` can't address them, so a whitespaced id makes
// chart styling silently break with no compile- or runtime-error.
func WithElementID(id string) ChartOption {
	if strings.ContainsAny(id, " \t\n\r\f") {
		panic(fmt.Errorf("echarts: WithElementID: id %q must not contain whitespace", id))
	}
	return func(c *Chart) { c.elementID = id }
}

// WithTitle sets the chart title.
func WithTitle(title string) ChartOption {
	return func(c *Chart) { c.title = title }
}

// WithDimensions sets container width and height. An empty string on
// either side falls back to that side's default ("100%" for width,
// "300px" for height) — so WithDimensions("75%", "") keeps the default
// height while pinning width.
//
// Example: WithDimensions("100%", "400px").
func WithDimensions(width, height string) ChartOption {
	return func(c *Chart) {
		c.width = width
		c.height = height
	}
}

// WithClass adds CSS class names to the chart container. Use it for
// theming, layout, or animation hooks without wrapping Mount in an
// extra div. Empty strings are skipped; when no classes remain the
// class attribute is omitted entirely. Panics if any single arg
// contains whitespace — HTML class attributes split on whitespace,
// so "foo bar" passed as one arg silently becomes two classes; the
// caller should pass them separately.
func WithClass(parts ...string) ChartOption {
	for _, p := range parts {
		if strings.ContainsAny(p, " \t\n\r\f") {
			panic(fmt.Errorf("echarts: WithClass: class name %q must not contain whitespace (use separate args for multiple classes)", p))
		}
	}
	return func(c *Chart) { c.classes = parts }
}

// WithRightYAxis adds a second yAxis on the right side of the chart
// with the given label template. Combine with the YAxisIndex series
// option to route specific series to the secondary axis — the
// canonical combo-chart shape for two metrics with very different
// scales sharing one panel (e.g. CPU % on the left, throughput in
// MB/s on the right).
//
//	echarts.WithYAxisFormat("{value} %")       // left axis
//	echarts.WithRightYAxis("{value} MB/s")     // right axis
//	echarts.Line("CPU", cpuPts)                // implicit yAxisIndex 0
//	echarts.Line("Net", netPts, echarts.YAxisIndex(1))
func WithRightYAxis(format string) ChartOption {
	return func(c *Chart) { c.rightYFmt = format }
}

// WithYAxisFormat sets the yAxis label template — typically a string
// containing the echarts placeholder `{value}` and a unit suffix:
//
//	echarts.WithYAxisFormat("{value} %")      // CPU / RAM percent
//	echarts.WithYAxisFormat("{value} KB/s")   // throughput
//	echarts.WithYAxisFormat("{value} GB")     // capacity
//
// Dense streaming dashboards rely on unit labels to keep the meaning
// of the y-axis legible at a glance; bare numbers force the reader to
// remember which chart is which.
func WithYAxisFormat(template string) ChartOption {
	return func(c *Chart) { c.yFormat = template }
}

// WithPalette sets the chart's positional color array — series are
// assigned colors in order from this list (echarts cycles when there
// are more series than entries). Use for consistent dashboard brand
// palettes without setting Color() on every series. For one-off
// per-series overrides, use the Color SeriesOption instead.
func WithPalette(colors ...string) ChartOption {
	return func(c *Chart) { c.palette = colors }
}

// WithCompactGrid shrinks the chart's plot inset for dense dashboards
// where many charts share a viewport. echarts' default grid leaves
// ~60px top + ~60px bottom of padding which adds up fast in a 4×2
// dashboard layout; compact mode trims to ~25px on each side and
// turns on `containLabel` so axis labels stay inside the container.
func WithCompactGrid() ChartOption {
	return func(c *Chart) { c.compactGrid = true }
}

// WithDataZoom enables echarts' interactive zoom for long histories:
// scroll/pinch inside the chart to zoom, plus a slider underneath for
// big-jump navigation. The standard combo for dense time-series
// dashboards where the rendered window holds far more samples than
// fit comfortably on screen.
func WithDataZoom() ChartOption {
	return func(c *Chart) { c.dataZoom = true }
}

// WithYAxisRange pins the yAxis lower and upper bounds. Without it,
// echarts auto-scales the yAxis on every setOption — when streaming
// metrics arrive with a new max or min the chart visibly jitters as
// the axis recomputes. Use for known-range metrics (CPU%, RAM%,
// percentages of any kind) where you want a stable axis.
func WithYAxisRange(min, max float64) ChartOption {
	return func(c *Chart) {
		c.yMin = &min
		c.yMax = &max
	}
}

// WithLegend explicitly sets the chart's legend visibility at
// construction. Without it, echarts decides based on its own heuristic
// (legend shows when series have names); pass false to suppress it in
// compact dashboards where the default top-position legend eats chart
// space, or true to force it on. The runtime SetLegend(ctx, bool)
// counterpart toggles visibility after Mount.
func WithLegend(visible bool) ChartOption {
	return func(c *Chart) { c.legendVis = &visible }
}

// WithCrosshair enables a cross-style axisPointer — vertical and
// horizontal guide lines at the cursor so users can read exact x
// and y values off the chart axes without hovering a specific data
// point. Pairs naturally with WithTimeAxis for read-off-the-axes
// interaction on dense streaming charts.
func WithCrosshair() ChartOption {
	return func(c *Chart) { c.crosshair = true }
}

// WithTimeAxis configures the chart for dense time-series data:
// switches the xAxis to type "time" (so echarts interpolates
// millisecond timestamps as a continuous axis rather than discrete
// categories) and adds an axis-trigger tooltip so hovering anywhere
// reveals values across all series at that x. Without this, the
// default category xAxis treats each timestamp as a label, which
// crowds and mis-spaces dense time data.
func WithTimeAxis() ChartOption {
	return func(c *Chart) { c.timeAxis = true }
}

// WithThemeOverride overrides the default theme for this specific chart.
func WithThemeOverride(theme EChartsTheme) ChartOption {
	return func(c *Chart) { c.theme = theme }
}

// WithGroup links this chart into a named connection group. Charts that
// share a group sync tooltip / axis-pointer / dataZoom interactions via
// echarts.connect — useful for dashboards where hovering one chart
// should reveal tooltips on its siblings. Empty string is a no-op.
func WithGroup(group string) ChartOption {
	return func(c *Chart) { c.group = group }
}

// WithInitialOption supplies an echarts options object applied at first
// paint, after the built-in defaults. Use it to escape the default
// category xAxis when the chart is a pie / radar / gauge / scatter, or
// to override any baked-in default. echarts.setOption merges, so any
// keys you specify replace the defaults; everything else stays.
//
// A nil or empty map is a no-op. Panics if opts contains a value that
// cannot be marshalled to JSON (channel, func, cycle) — that's a
// programmer bug, surfaced eagerly here rather than silently dropped
// at render time.
func WithInitialOption(opts map[string]any) ChartOption {
	if len(opts) > 0 {
		if _, err := json.Marshal(opts); err != nil {
			panic(fmt.Errorf("echarts: WithInitialOption: %v", err))
		}
	}
	return func(c *Chart) { c.initialOpts = opts }
}

// NewChart creates a new Chart with options. If no element ID is provided,
// one is auto-generated using a monotonic counter.
func NewChart(opts ...ChartOption) *Chart {
	c := &Chart{seq: chartCounter.Add(1)}
	for _, opt := range opts {
		opt(c)
	}
	if c.elementID == "" {
		c.elementID = fmt.Sprintf("echart-%d", c.seq)
	}
	return c
}

// Mount returns an h.H element representing this chart. It includes the
// initialization script inline for page load.
func (c *Chart) Mount() h.H {
	width := cmp.Or(c.width, "100%")
	height := cmp.Or(c.height, "300px")
	return h.Div(
		h.ID(c.elementID),
		h.Class(c.classes...),
		h.DataIgnoreMorph(),
		h.Style(fmt.Sprintf("width:%s;height:%s", width, height)),
		h.Script(h.Raw(c.initJS())),
	)
}

// mustJSON marshals v to JSON. All callers pass values that cannot fail
// json.Marshal (string fields or pre-validated maps from WithInitialOption),
// so an error here is an internal bug and is treated as one.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Errorf("echarts: internal mustJSON: %v", err))
	}
	return string(b)
}

// themeColors returns (textColor, axisLabelColor, axisLineColor) for the given theme.
func themeColors(theme EChartsTheme) (string, string, string) {
	if theme == ThemeDark {
		return "#ccc", "#aaa", "rgba(255,255,255,0.3)"
	}
	return "#222", "#666", "rgba(0,0,0,0.3)"
}

// initJS returns JavaScript to initialize the chart on page load. The
// caller (Mount) wraps it in a <script> so it runs alongside the rendered
// container. All locals live inside an IIFE so nothing leaks to window;
// the instance + observer pair are parked at window.__viaCharts[seq].
func (c *Chart) initJS() string {
	theme := cmp.Or(c.theme, ThemeLight)
	textColor, labelColor, lineColor := themeColors(theme)

	xAxisType := "'category'"
	var tooltip string
	if c.timeAxis {
		xAxisType = "'time'"
		tooltip = "\n\t\t\ttooltip: {trigger: 'axis'},"
	}
	if c.crosshair {
		tooltip = "\n\t\t\ttooltip: {trigger: 'axis', axisPointer: {type: 'cross'}},"
	}

	var yRange string
	if c.yMin != nil && c.yMax != nil {
		yRange = fmt.Sprintf("min: %g, max: %g, ", *c.yMin, *c.yMax)
	}

	yLabelExtra := ""
	if c.yFormat != "" {
		yLabelExtra = fmt.Sprintf(",formatter:%s", mustJSON(c.yFormat))
	}

	var dataZoom string
	if c.dataZoom {
		dataZoom = "\n\t\t\tdataZoom: [{type: 'inside'}, {type: 'slider'}],"
	}

	var grid string
	if c.compactGrid {
		grid = "\n\t\t\tgrid: {top: 25, right: 25, bottom: 25, left: 25, containLabel: true},"
	}

	var palette string
	if len(c.palette) > 0 {
		palette = fmt.Sprintf("\n\t\t\tcolor: %s,", mustJSON(c.palette))
	}

	var legend string
	if c.legendVis != nil {
		legend = fmt.Sprintf("\n\t\t\tlegend: {show:%t},", *c.legendVis)
	}

	leftYAxis := fmt.Sprintf(
		"{%ssplitLine: {lineStyle:{color:'rgba(128,128,128,0.25)'}}, axisLabel:{color:%s%s}, axisLine:{lineStyle:{color:%s}}}",
		yRange, mustJSON(labelColor), yLabelExtra, mustJSON(lineColor),
	)
	yAxis := "yAxis: " + leftYAxis
	if c.rightYFmt != "" {
		rightYAxis := fmt.Sprintf(
			"{position:'right', splitLine: {lineStyle:{color:'rgba(128,128,128,0.25)'}}, axisLabel:{color:%s,formatter:%s}, axisLine:{lineStyle:{color:%s}}}",
			mustJSON(labelColor), mustJSON(c.rightYFmt), mustJSON(lineColor),
		)
		yAxis = fmt.Sprintf("yAxis: [%s, %s]", leftYAxis, rightYAxis)
	}

	var extra string
	if len(c.initialOpts) > 0 {
		extra = fmt.Sprintf("\n\t\t_c.setOption(%s);", mustJSON(c.initialOpts))
	}
	if c.group != "" {
		gj := mustJSON(c.group)
		extra += fmt.Sprintf("\n\t\t_c.group=%s; echarts.connect(%s);", gj, gj)
	}

	return fmt.Sprintf(`(function(){
		window.__viaCharts = window.__viaCharts || {};
		var _el = document.getElementById(%s);
		var _c = echarts.init(_el, %s);
		_c.setOption({
			backgroundColor: 'transparent',
			textStyle: {color: %s},
			animationDurationUpdate: 0,
			animationEasingUpdate: "linear",%s%s%s%s%s
			title: {text: %s},
			xAxis: {type: %s, splitLine: {lineStyle:{color:'rgba(128,128,128,0.25)'}}, axisLabel:{color:%s}, axisLine:{lineStyle:{color:%s}}},
			%s,
			series: [{}]
		});
		var _ro = new ResizeObserver(function(){var _e=window.__viaCharts[%d]; if(_e&&_e.c)_e.c.resize();});
		_ro.observe(_el);
		window.__viaCharts[%d] = {c:_c, ro:_ro};%s
	})();`,
		mustJSON(c.elementID),
		mustJSON(theme),
		mustJSON(textColor),
		tooltip,
		dataZoom,
		grid,
		palette,
		legend,
		mustJSON(c.title),
		xAxisType,
		mustJSON(labelColor), mustJSON(lineColor),
		yAxis,
		c.seq,
		c.seq,
		extra,
	)
}

// chartRef returns the optional-chaining JS expression that resolves to
// the chart instance (or undefined). Use as a prefix for method calls:
// `chart.chartRef() + "?.setOption(...)"`.
func (c *Chart) chartRef() string {
	return fmt.Sprintf("window.__viaCharts?.[%d]?.c", c.seq)
}
