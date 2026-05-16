package echarts_test

import (
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
	viatest "github.com/go-via/via/test"
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
			name:     "WithElementID overrides the auto-id",
			opts:     []echarts.ChartOption{echarts.WithElementID("cpu-chart")},
			contains: []string{`id="cpu-chart"`, "cpu-chart"},
		},
		{
			name:     "WithTitle surfaces in the init script",
			opts:     []echarts.ChartOption{echarts.WithTitle("CPU usage")},
			contains: []string{"CPU usage"},
		},
		{
			name: "WithDimensions sets inline style width and height",
			opts: []echarts.ChartOption{
				echarts.WithDimensions("75%", "400px"),
			},
			contains: []string{"width:75%", "height:400px"},
		},
		{
			name:     "WithThemeOverride flips the init theme to dark",
			opts:     []echarts.ChartOption{echarts.WithThemeOverride(echarts.ThemeDark)},
			contains: []string{"dark"},
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

func TestLine_andBar_returnSeriesOptionMaps(t *testing.T) {
	t.Parallel()

	line := echarts.Line("CPU", [][]any{{0, 12}})
	assert.Equal(t, "line", line["type"])
	assert.Equal(t, "CPU", line["name"])

	bar := echarts.Bar("Hits", [][]any{{0, 7}})
	assert.Equal(t, "bar", bar["type"])
	assert.Equal(t, "Hits", bar["name"])
}

type chartActionPage struct {
	Chart *echarts.Chart
}

func (p *chartActionPage) SetOpt(ctx *via.Ctx) error {
	p.Chart.SetOption(ctx, map[string]any{"backgroundColor": "#fff"})
	return nil
}

func (p *chartActionPage) SetSer(ctx *via.Ctx) error {
	p.Chart.SetSeries(ctx, echarts.Line("L", [][]any{{0, 1}, {1, 2}}))
	return nil
}

func (p *chartActionPage) AppendPoints(ctx *via.Ctx) error {
	p.Chart.AppendData(ctx, 0, [][]any{{42, 17}})
	return nil
}

func (p *chartActionPage) View(ctx *via.Ctx) h.H { return p.Chart.Mount() }

func TestChartAPI_SetOption_pushesExecScript(t *testing.T) {
	t.Parallel()
	p := &chartActionPage{Chart: echarts.NewChart(echarts.WithElementID("ch"))}
	ctx := viatest.NewCtx(t, p)
	require.NoError(t, p.SetOpt(ctx))
	scripts := ctx.PendingScripts()
	assert.Contains(t, scripts, "setOption",
		"SetOption must queue a setOption(...) JS statement")
	assert.Contains(t, scripts, "backgroundColor")
}

func TestChartAPI_SetSeries_emitsSeriesUpdate(t *testing.T) {
	t.Parallel()
	p := &chartActionPage{Chart: echarts.NewChart(echarts.WithElementID("ch2"))}
	ctx := viatest.NewCtx(t, p)
	require.NoError(t, p.SetSer(ctx))
	scripts := ctx.PendingScripts()
	assert.Contains(t, scripts, `"series"`,
		"SetSeries must wrap its argument in a series key")
	assert.Contains(t, scripts, `"line"`,
		"the series payload should still carry the original Line() shape")
}

func TestChartAPI_AppendData_emitsAppendDataCall(t *testing.T) {
	t.Parallel()
	p := &chartActionPage{Chart: echarts.NewChart(echarts.WithElementID("ch3"))}
	ctx := viatest.NewCtx(t, p)
	require.NoError(t, p.AppendPoints(ctx))
	scripts := ctx.PendingScripts()
	assert.Contains(t, scripts, "appendData",
		"AppendData must dispatch a chart.appendData call")
	assert.Contains(t, scripts, "seriesIndex:0")
}
