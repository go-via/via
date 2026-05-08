package echarts

import (
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

// ChartType represents the type of chart to render.
type ChartType string

const (
	TypeLine       ChartType = "line"
	TypeBar        ChartType = "bar"
	TypePie        ChartType = "pie"
	TypeScatter    ChartType = "scatter"
	TypeArea       ChartType = "area"
	TypeCandle     ChartType = "candlestick"
	TypeRadar      ChartType = "radar"
	TypeGauge      ChartType = "gauge"
	TypeHeatmap    ChartType = "heatmap"
	TypeTree       ChartType = "tree"
	TypeGraph      ChartType = "graph"
	TypeLiquidFill ChartType = "liquidFill"
)

// chartOptions holds plugin configuration options.
type chartOptions struct {
	version string
}

// PluginOption configures the Echarts plugin.
type PluginOption interface {
	apply(*plugin)
}

type pluginOptionFunc func(*plugin)

func (f pluginOptionFunc) apply(p *plugin) { f(p) }

// WithVersion sets the ECharts CDN version.
func WithVersion(version string) PluginOption {
	return pluginOptionFunc(func(p *plugin) { p.opts.version = version })
}

// chartCounter provides deterministic, unique IDs across chart instances.
var chartCounter atomic.Uint64

// Chart represents an ECharts component configuration.
type Chart struct {
	seq              uint64
	elementID        string
	chartType        ChartType
	data             [][]any
	varName          string       // JavaScript variable name, defaults to unique ID
	title            string       // chart title
	xAxisLabel       string       // x-axis label
	yAxisLabel       string       // y-axis label
	width            string       // container width (e.g., "100%", "600px")
	height           string       // container height (e.g., "400px", "50vh")
	theme            EChartsTheme // chart theme override
	updateDurationMs int          // animationDurationUpdate override; 0 = use default (950)
}

// ChartOption configures a Chart.
type ChartOption interface {
	apply(*Chart)
}

type chartOptionFunc func(*Chart)

func (f chartOptionFunc) apply(c *Chart) { f(c) }

// WithElementID sets the element ID for the chart container.
func WithElementID(id string) ChartOption {
	return chartOptionFunc(func(c *Chart) { c.elementID = id })
}

// WithChartType sets the chart type.
func WithChartType(t ChartType) ChartOption {
	return chartOptionFunc(func(c *Chart) { c.chartType = t })
}

// WithData sets initial data for the chart.
func WithData(data [][]any) ChartOption {
	return chartOptionFunc(func(c *Chart) { c.data = data })
}

// WithVarName sets a custom JavaScript variable name.
// This is useful when you need to reference the chart instance externally.
func WithVarName(name string) ChartOption {
	return chartOptionFunc(func(c *Chart) { c.varName = name })
}

// WithTitle sets the chart title.
func WithTitle(title string) ChartOption {
	return chartOptionFunc(func(c *Chart) { c.title = title })
}

// WithXAxisLabel sets the x-axis label.
func WithXAxisLabel(label string) ChartOption {
	return chartOptionFunc(func(c *Chart) { c.xAxisLabel = label })
}

// WithYAxisLabel sets the y-axis label.
func WithYAxisLabel(label string) ChartOption {
	return chartOptionFunc(func(c *Chart) { c.yAxisLabel = label })
}

// WithAnimationDuration sets animationDurationUpdate in milliseconds.
// Animation on full dataset replacement looks wrong — only use this when appending
// small numbers of points where ECharts can meaningfully interpolate between frames.
// Defaults to 0 (no animation).
func WithAnimationDuration(ms int) ChartOption {
	return chartOptionFunc(func(c *Chart) { c.updateDurationMs = ms })
}

// WithDimensions sets container width and height.
// Example: WithDimensions("100%", "400px")
func WithDimensions(width, height string) ChartOption {
	return chartOptionFunc(func(c *Chart) {
		c.width = width
		c.height = height
	})
}

// WithThemeOverride overrides the default theme for this specific chart.
func WithThemeOverride(theme EChartsTheme) ChartOption {
	return chartOptionFunc(func(c *Chart) { c.theme = theme })
}

// NewChart creates a new Chart with options.
// If no element ID is provided, one is auto-generated using a monotonic counter.
func NewChart(opts ...ChartOption) *Chart {
	c := &Chart{}
	c.seq = chartCounter.Add(1)
	for _, opt := range opts {
		opt.apply(c)
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

// InitJS returns JavaScript to initialize the chart on page load.
// It creates the echarts instance and sets initial options.
func (c *Chart) InitJS() string {
	theme := "light"
	if c.theme != "" {
		theme = string(c.theme)
	}

	textColor, labelColor, lineColor := themeColors(EChartsTheme(theme))

	varName := c.getVarName()
	updateDuration := c.updateDurationMs
	return fmt.Sprintf(`
		var %s = echarts.init(document.getElementById(%s), %s);
		%s.setOption({
			backgroundColor: 'transparent',
			textStyle: {color: %s},
			animationDurationUpdate: %d,
			animationEasingUpdate: "linear",
			title: {text: %s},
			xAxis: {name: %s, type: 'category', splitLine: {lineStyle:{color:'rgba(128,128,128,0.25)'}}, axisLabel:{color:%s}, axisLine:{lineStyle:{color:%s}}},
			yAxis: {name: %s, splitLine: {lineStyle:{color:'rgba(128,128,128,0.25)'}}, axisLabel:{color:%s}, axisLine:{lineStyle:{color:%s}}},
			series: [{
				type: %s,
				data: %s
			}]
		});
		new ResizeObserver(()=>%s.resize()).observe(document.getElementById(%s));
	`,
		varName,
		mustJSON(c.elementID),
		mustJSON(theme),
		varName,
		mustJSON(textColor),
		updateDuration,
		mustJSON(c.title),
		mustJSON(c.xAxisLabel), mustJSON(labelColor), mustJSON(lineColor),
		mustJSON(c.yAxisLabel), mustJSON(labelColor), mustJSON(lineColor),
		mustJSON(string(c.chartType)),
		mustJSON(c.data),
		varName,
		mustJSON(c.elementID),
	)
}

// AppendData sends new data points to the chart via SSE.
// Each data point is appended to series index 0.
func (c *Chart) AppendData(ctx *via.Ctx, data [][]any) {
	if ctx == nil || len(data) == 0 {
		return
	}
	var sb strings.Builder
	varName := c.getVarName()
	sb.WriteString("if(")
	sb.WriteString(varName)
	sb.WriteString("){")
	sb.WriteString(varName)
	sb.WriteString(".appendData({seriesIndex:0,data:")
	sb.WriteString(mustJSON(data))
	sb.WriteString("})}")
	ctx.ExecScript(sb.String())
}

// AppendDataBatch sends multiple data arrays to the chart in a single SSE patch.
// Prefer this over calling AppendData in a loop to reduce per-update overhead.
func (c *Chart) AppendDataBatch(ctx *via.Ctx, batches ...[][]any) {
	if ctx == nil || len(batches) == 0 {
		return
	}
	varName := c.getVarName()
	var sb strings.Builder
	sb.WriteString("if(")
	sb.WriteString(varName)
	sb.WriteString("){")
	for _, data := range batches {
		if len(data) == 0 {
			continue
		}
		sb.WriteString(varName)
		sb.WriteString(".appendData({seriesIndex:0,data:")
		sb.WriteString(mustJSON(data))
		sb.WriteString("});")
	}
	sb.WriteString("}")
	ctx.ExecScript(sb.String())
}

// SetOption sends option updates to the chart via SSE.
func (c *Chart) SetOption(ctx *via.Ctx, opts map[string]any) {
	if ctx == nil {
		return
	}
	varName := c.getVarName()
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

// SetTheme disposes and re-creates the chart with a new theme, preserving the current options.
func (c *Chart) SetTheme(ctx *via.Ctx, theme EChartsTheme) {
	if ctx == nil {
		return
	}
	varName := c.getVarName()
	elemID := mustJSON(c.elementID)
	textColor, labelColor, lineColor := themeColors(theme)
	// Reset axis colors that the previous theme may have baked in.
	// Without this, switching from dark→light keeps the dark theme's
	// near-white axis label/line colors, making them invisible on white.
	ctx.ExecScript(fmt.Sprintf(
		`if(%s){var _o=%s.getOption();_o.backgroundColor='transparent';_o.textStyle=[{color:%s}];`+
			`var _ax={axisLabel:{color:%s},axisLine:{lineStyle:{color:%s}},splitLine:{lineStyle:{color:'rgba(128,128,128,0.25)'}}};`+
			`if(_o.xAxis){for(var i=0;i<_o.xAxis.length;i++){Object.assign(_o.xAxis[i],_ax)}}`+
			`if(_o.yAxis){for(var i=0;i<_o.yAxis.length;i++){Object.assign(_o.yAxis[i],_ax)}}`+
			`%s.dispose();%s=echarts.init(document.getElementById(%s),%s);%s.setOption(_o)}`,
		varName, varName, mustJSON(textColor), mustJSON(labelColor), mustJSON(lineColor),
		varName, varName, elemID, mustJSON(string(theme)), varName,
	))
}

// getVarName returns the JS variable name, generating a deterministic default if empty.
func (c *Chart) getVarName() string {
	if c.varName != "" {
		return c.varName
	}
	return fmt.Sprintf("echart_%d", c.seq)
}

// Mount returns an h.H element representing this chart.
// It includes the initialization script inline for page load.
func (c *Chart) Mount() h.H {
	children := []h.H{
		h.ID(c.elementID),
		h.DataIgnoreMorph(),
	}
	width := c.width
	if width == "" {
		width = "100%"
	}
	height := c.height
	if height == "" {
		height = "300px"
	}
	children = append(children, h.Style(fmt.Sprintf("width:%s;height:%s", width, height)))
	children = append(children, h.Script(h.Raw(c.InitJS())))
	return h.Div(children...)
}

// Plugin creates a new Echarts plugin with default settings.
// Use WithVersion() to customize.
func Plugin(opts ...PluginOption) via.Plugin {
	p := &plugin{
		opts: chartOptions{
			version: defaultVersion,
		},
	}
	for _, opt := range opts {
		opt.apply(p)
	}
	return p
}

type plugin struct {
	opts chartOptions
}

func (p *plugin) Register(v *via.App) {
	// Load ECharts from CDN with configured version
	v.AppendToHead(h.Script(h.Src(fmt.Sprintf(cdnBase, p.opts.version))))
}
