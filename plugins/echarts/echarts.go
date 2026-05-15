// Package echarts provides an Apache ECharts plugin for the Via engine.
//
// Quick start:
//
//	app := via.New(via.WithPlugins(echarts.Plugin()))
//
// Use NewChart with options for the container, then Mount to render and
// push runtime updates over SSE. Most updates only need SetSeries:
//
//	chart := echarts.NewChart(
//	    echarts.WithElementID("cpu-chart"),
//	    echarts.WithDimensions("100%", "300px"),
//	    echarts.WithThemeOverride(echarts.ThemeDark),
//	)
//	// in View():
//	chart.Mount()
//	// in an action / OnConnect ticker:
//	chart.SetSeries(ctx, echarts.Line("CPU", points))
//
// For unbounded streaming, AppendData skips the replace-everything cost:
//
//	chart.AppendData(ctx, 0, [][]any{{tsMs, value}})
//
// SetOption is the escape hatch — pass any echarts options shape (axes,
// tooltip, legend, …); the chart receives it verbatim.
package echarts

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// CDN configuration defaults
const (
	defaultVersion = "6.0.0"
	cdnBase        = "https://cdn.jsdelivr.net/npm/echarts@%s/dist/echarts.min.js"
)

// EChartsTheme represents the chart theme variant.
type EChartsTheme string

const (
	ThemeLight EChartsTheme = "light"
	ThemeDark  EChartsTheme = "dark"
)

type chartOptions struct {
	version string
}

// PluginOption configures the Echarts plugin. Each option mutates the
// plugin in place; Plugin applies them in argument order.
type PluginOption func(*plugin)

// WithVersion sets the ECharts CDN version.
func WithVersion(version string) PluginOption {
	return func(p *plugin) { p.opts.version = version }
}

// chartCounter provides deterministic, unique IDs across chart instances.
var chartCounter atomic.Uint64

// Chart represents an ECharts component configuration. Construct with
// NewChart, render with Mount, push runtime updates with SetOption.
type Chart struct {
	seq       uint64
	elementID string
	title     string
	width     string
	height    string
	theme     EChartsTheme
}

// ChartOption configures a Chart. Each option mutates the chart in place;
// NewChart applies them in argument order.
type ChartOption func(*Chart)

// WithElementID sets the element ID for the chart container.
func WithElementID(id string) ChartOption {
	return func(c *Chart) { c.elementID = id }
}

// WithTitle sets the chart title.
func WithTitle(title string) ChartOption {
	return func(c *Chart) { c.title = title }
}

// WithDimensions sets container width and height.
// Example: WithDimensions("100%", "400px").
func WithDimensions(width, height string) ChartOption {
	return func(c *Chart) {
		c.width = width
		c.height = height
	}
}

// WithThemeOverride overrides the default theme for this specific chart.
func WithThemeOverride(theme EChartsTheme) ChartOption {
	return func(c *Chart) { c.theme = theme }
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

// mustJSON marshals v to JSON, returning "null" on error.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
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
// container.
func (c *Chart) initJS() string {
	theme := cmp.Or(c.theme, ThemeLight)
	textColor, labelColor, lineColor := themeColors(theme)

	varName := c.varName()
	return fmt.Sprintf(`
		var %s = echarts.init(document.getElementById(%s), %s);
		%s.setOption({
			backgroundColor: 'transparent',
			textStyle: {color: %s},
			animationDurationUpdate: 0,
			animationEasingUpdate: "linear",
			title: {text: %s},
			xAxis: {type: 'category', splitLine: {lineStyle:{color:'rgba(128,128,128,0.25)'}}, axisLabel:{color:%s}, axisLine:{lineStyle:{color:%s}}},
			yAxis: {splitLine: {lineStyle:{color:'rgba(128,128,128,0.25)'}}, axisLabel:{color:%s}, axisLine:{lineStyle:{color:%s}}},
			series: [{}]
		});
		new ResizeObserver(()=>%s.resize()).observe(document.getElementById(%s));
	`,
		varName,
		mustJSON(c.elementID),
		mustJSON(theme),
		varName,
		mustJSON(textColor),
		mustJSON(c.title),
		mustJSON(labelColor), mustJSON(lineColor),
		mustJSON(labelColor), mustJSON(lineColor),
		varName,
		mustJSON(c.elementID),
	)
}

// SetOption pushes an echarts options object to the chart over SSE. This
// is the escape hatch for configuring chart type, data, axes, series, and
// any other echarts feature — pass the raw echarts options shape.
func (c *Chart) SetOption(ctx *via.Ctx, opts map[string]any) {
	if ctx == nil {
		return
	}
	varName := c.varName()
	var sb strings.Builder
	sb.WriteString("if(")
	sb.WriteString(varName)
	sb.WriteString("){")
	sb.WriteString(varName)
	sb.WriteString(".setOption(")
	sb.WriteString(mustJSON(opts))
	sb.WriteString(")}")
	ctx.ExecScript(sb.String())
}

// SetSeries replaces the chart's series with the given configurations.
// Sugar over SetOption for the common case where you only need to update
// data points rather than rebuild the whole options object:
//
//	chart.SetSeries(ctx, echarts.Line("CPU", points))
//	chart.SetSeries(ctx,
//	    echarts.Line("Read", reads),
//	    echarts.Line("Write", writes),
//	)
func (c *Chart) SetSeries(ctx *via.Ctx, series ...map[string]any) {
	if len(series) == 0 {
		return
	}
	out := make([]any, len(series))
	for i, s := range series {
		out[i] = s
	}
	c.SetOption(ctx, map[string]any{"series": out})
}

// AppendData streams new data points to a series via echarts.appendData
// — more efficient than SetSeries for unbounded time-series streaming
// because the existing data isn't replaced. The seriesIdx selects which
// configured series receives the points.
//
//	chart.AppendData(ctx, 0, [][]any{{tsMs, value}})
//
// Bounded sliding windows still want SetSeries with a trimmed snapshot;
// use AppendData when the chart should grow forever (or you handle
// trimming server-side).
func (c *Chart) AppendData(ctx *via.Ctx, seriesIdx int, data [][]any) {
	if ctx == nil || len(data) == 0 {
		return
	}
	varName := c.varName()
	ctx.ExecScriptf(
		"if(%s){%s.appendData({seriesIndex:%d,data:%s})}",
		varName, varName, seriesIdx, mustJSON(data),
	)
}

// Line returns a line series options map suitable for SetSeries / SetOption.
// For time-series charts, data is a slice of [timestampMs, value] pairs;
// for category charts, [categoryName, value].
func Line(name string, data [][]any) map[string]any {
	return map[string]any{"type": "line", "name": name, "data": data}
}

// Bar returns a bar series options map suitable for SetSeries / SetOption.
func Bar(name string, data [][]any) map[string]any {
	return map[string]any{"type": "bar", "name": name, "data": data}
}

// varName returns the JS variable name. Underscore prefix keeps the name
// a valid JS identifier; dots are not allowed.
func (c *Chart) varName() string {
	return fmt.Sprintf("echart_%d", c.seq)
}

// Mount returns an h.H element representing this chart. It includes the
// initialization script inline for page load.
func (c *Chart) Mount() h.H {
	width := cmp.Or(c.width, "100%")
	height := cmp.Or(c.height, "300px")
	return h.Div(
		h.ID(c.elementID),
		h.DataIgnoreMorph(),
		h.Style(fmt.Sprintf("width:%s;height:%s", width, height)),
		h.Script(h.Raw(c.initJS())),
	)
}

// Plugin creates a new Echarts plugin with default settings.
// Use WithVersion() to customize.
func Plugin(opts ...PluginOption) via.Plugin {
	p := &plugin{opts: chartOptions{version: defaultVersion}}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

type plugin struct {
	opts chartOptions
}

func (p *plugin) Register(v *via.App) {
	v.AppendToHead(h.Script(h.Src(fmt.Sprintf(cdnBase, p.opts.version))))
}
