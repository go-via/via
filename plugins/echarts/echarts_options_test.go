package echarts_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
	"github.com/go-via/via/vt"
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

func TestChartAPI_SetOption_emitsSetOptionScript(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[chartActionPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("SetOpt").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "setOption", "backgroundColor")
}

func TestChartAPI_SetSeries_emitsSeriesUpdate(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[chartActionPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("SetSer").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"series"`, `"line"`)
}

func TestChartAPI_AppendData_emitsAppendDataCall(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[chartActionPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("AppendPoints").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "appendData", "seriesIndex:0")
}
