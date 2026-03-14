package echarts

import (
	"fmt"

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
	theme   EChartsTheme
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

// WithTheme sets the default theme for charts.
func WithTheme(theme EChartsTheme) PluginOption {
	return pluginOptionFunc(func(p *plugin) { p.opts.theme = theme })
}

// Chart represents an ECharts component configuration.
// VarName defaults to a unique ID if not set.
type Chart struct {
	elementID  string
	chartType  ChartType
	data       [][]any
	varName    string       // JavaScript variable name, defaults to unique ID
	title      string       // chart title
	xAxisLabel string       // x-axis label
	yAxisLabel string       // y-axis label
	width      string       // container width (e.g., "100%", "600px")
	height     string       // container height (e.g., "400px", "50vh")
	theme      EChartsTheme // chart theme override
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
// If no element ID is provided, one is auto-generated.
func NewChart(opts ...ChartOption) *Chart {
	c := &Chart{}
	for _, opt := range opts {
		opt.apply(c)
	}
	if c.elementID == "" {
		c.elementID = fmt.Sprintf("chart%p", c)
	}
	return c
}

// InitJS returns JavaScript to initialize the chart on page load.
// It creates the echarts instance and sets initial options.
func (c *Chart) InitJS() string {
	theme := "light"
	if c.theme != "" {
		theme = string(c.theme)
	}

	return fmt.Sprintf(`
		var %s = echarts.init(document.getElementById('%s'), '%s');
		%s.setOption({
			title: {text: '%s'},
			xAxis: {name: '%s', type: 'category'},
			yAxis: {name: '%s'},
			series: [{
				type: '%s',
				data: %v
			}]
		});
	`, c.getVarName(), c.elementID, theme, c.getVarName(), c.title, c.xAxisLabel, c.yAxisLabel, c.chartType, c.data)
}

// AppendData sends new data points to the chart via SSE.
// Each data point is appended to series index 0.
func (c *Chart) AppendData(ctx *via.Context, data [][]any) {
	if ctx == nil || len(data) == 0 {
		return
	}
	js := fmt.Sprintf(`if(%s){%s.appendData({seriesIndex:0,data:%v})}`, c.getVarName(), c.getVarName(), data)
	ctx.ExecScript(js)
}

// SetOption sends option updates to the chart via SSE.
func (c *Chart) SetOption(ctx *via.Context, opts map[string]any) {
	if ctx == nil {
		return
	}
	js := fmt.Sprintf(`if(%s){%s.setOption(%v)}`, c.getVarName(), c.getVarName(), opts)
	ctx.ExecScript(js)
}

// getVarName returns the JS variable name, generating a default if empty.
func (c *Chart) getVarName() string {
	if c.varName != "" {
		return c.varName
	}
	return "chart" + fmt.Sprintf("%p", c)[2:]
}

// Mount returns an h.H element representing this chart.
// It includes the initialization script inline for page load.
func (c *Chart) Mount() h.H {
	div := h.Div(
		h.ID(c.elementID),
		h.Script(h.Raw(c.InitJS())),
	)
	if c.width != "" || c.height != "" {
		var style string
		if c.width != "" && c.height != "" {
			style = fmt.Sprintf("width:%s;height:%s", c.width, c.height)
		} else if c.width != "" {
			style = fmt.Sprintf("width:%s", c.width)
		} else {
			style = fmt.Sprintf("height:%s", c.height)
		}
		return h.Div(
			h.ID(c.elementID),
			h.Style(style),
			h.Script(h.Raw(c.InitJS())),
		)
	}
	return div
}

// Plugin creates a new Echarts plugin with default settings.
// Use WithVersion() and WithTheme() to customize.
func Plugin(opts ...PluginOption) via.Plugin {
	p := &plugin{
		opts: chartOptions{
			version: defaultVersion,
			theme:   ThemeLight,
		},
	}
	for _, opt := range opts {
		opt.apply(p)
	}
	return p
}

type plugin struct {
	opts    chartOptions
	version string // cached version for use in Register
	theme   EChartsTheme
}

func (p *plugin) Register(v *via.App) {
	// Load ECharts from CDN with configured version
	v.AppendToHead(h.Script(h.Src(fmt.Sprintf(cdnBase, p.opts.version))))
}
