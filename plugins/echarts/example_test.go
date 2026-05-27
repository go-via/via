package echarts_test

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
)

// quickstartPage shows the typical Via + echarts shape: a chart held
// on the page type, constructed once in OnInit, mounted in View, and
// updated from action handlers that flow over SSE.
type quickstartPage struct {
	Chart *echarts.Chart
}

func (p *quickstartPage) OnInit(ctx *via.Ctx) error {
	if p.Chart == nil {
		p.Chart = echarts.NewChart(
			echarts.WithElementID("cpu"),
			echarts.WithTitle("CPU"),
			echarts.WithDimensions("100%", "300px"),
		)
	}
	return nil
}

func (p *quickstartPage) Refresh(ctx *via.Ctx) error {
	return p.Chart.SetSeries(ctx, echarts.Line("CPU", [][]any{{0, 12}, {1, 18}}))
}

func (p *quickstartPage) View(ctx *via.CtxR) h.H {
	if p.Chart == nil {
		return h.Div()
	}
	return p.Chart.Mount()
}

// Quickstart for a single-chart page: register the plugin, mount a page
// whose Chart is built in OnInit and rendered via Mount, and push
// updates from action handlers.
func Example() {
	app := via.New(via.WithPlugins(echarts.Plugin()))
	via.Mount[quickstartPage](app, "/")
}

// Self-host echarts.min.js — for air-gapped, offline, or strict-CSP
// deployments where the default jsDelivr CDN can't be used.
func ExamplePlugin_selfHosted() {
	_ = echarts.Plugin(echarts.WithSource("/static/echarts.min.js"))
}

// Link two charts so hovering one reveals the tooltip on its sibling —
// echarts.connect under the hood, driven by sharing a WithGroup name.
func ExampleWithGroup() {
	cpu := echarts.NewChart(
		echarts.WithElementID("cpu"),
		echarts.WithGroup("dashboard"),
	)
	ram := echarts.NewChart(
		echarts.WithElementID("ram"),
		echarts.WithGroup("dashboard"),
	)
	_ = cpu.Mount()
	_ = ram.Mount()
}

// Release a chart's echarts instance + ResizeObserver before its
// container leaves the DOM. Without Dispose, an SPA-style app that
// swaps charts in and out leaks one instance per swap for the lifetime
// of the page.
func ExampleChart_Dispose() {
	chart := echarts.NewChart(echarts.WithElementID("cpu"))
	var ctx *via.Ctx // in practice, the *via.Ctx passed to your action handler
	chart.Dispose(ctx)
}

// Override the default category-xAxis init for chart types like pie
// that need a different first-paint shape.
func ExampleWithInitialOption() {
	_ = echarts.NewChart(
		echarts.WithElementID("breakdown"),
		echarts.WithInitialOption(map[string]any{
			"series": []any{echarts.Pie("Slices", []map[string]any{
				{"name": "A", "value": 30},
				{"name": "B", "value": 70},
			})},
		}),
	)
}
