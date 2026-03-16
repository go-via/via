package echarts_test

import (
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
)

func TestPlugin_ReturnsViaPlugin(t *testing.T) {
	p := echarts.Plugin()
	if p == nil {
		t.Fatal("expected non-nil plugin from Plugin()")
	}

	// Verifies plugin implements the required Plugin interface at compile time
	var _ via.Plugin = p
}

func TestNewChart_WithOptions(t *testing.T) {
	_ = via.New(via.WithPlugins(echarts.Plugin()))

	chart := echarts.NewChart(
		echarts.WithElementID("my-chart"),
		echarts.WithChartType(echarts.TypeLine),
		echarts.WithData([][]any{{1, 10}, {2, 20}}),
	)

	if chart == nil {
		t.Fatal("expected non-nil chart")
	}

	js := chart.InitJS()
	if !strings.Contains(js, "my-chart") {
		t.Error("expected InitJS to contain element ID")
	}
	if !strings.Contains(js, "line") {
		t.Error("expected InitJS to contain chart type")
	}
}

func TestChart_AppendData(t *testing.T) {
	_ = via.New(via.WithPlugins(echarts.Plugin()))

	// Create a minimal page with chart to get context
	var ctx *via.Context
	chart := echarts.NewChart(echarts.WithElementID("test-chart"))

	// AppendData should call ExecScript on the provided context
	newData := [][]any{{1, 100}}
	chart.AppendData(ctx, newData)

	// Test would verify ExecScript was called with expected JS
}

func TestChart_VarName(t *testing.T) {
	chart := echarts.NewChart(echarts.WithElementID("test"))

	// InitJS should generate a unique variable name when not set
	js := chart.InitJS()

	if !strings.Contains(js, "var ") || strings.Count(js, "var ") != 1 {
		t.Error("expected exactly one var declaration in InitJS")
	}
}

func TestNewChart_ReturnsConfiguredChart(t *testing.T) {
	opts := []echarts.ChartOption{
		echarts.WithElementID("my-chart"),
		echarts.WithChartType(echarts.TypeLine),
		echarts.WithData([][]any{{1, 10}}),
	}

	chart := echarts.NewChart(opts...)

	if chart == nil {
		t.Fatal("expected non-nil chart from NewChart")
	}
}

func TestNewChart_GeneratesIDWhenNotProvided(t *testing.T) {
	chart := echarts.NewChart(echarts.WithChartType(echarts.TypeLine))

	// When no element ID is provided, the Mount() method should generate one
	mounted := chart.Mount()

	rendered := renderElement(mounted)
	t.Logf("Rendered: %q", rendered)

	// Should have a non-empty id attribute value
	if strings.Contains(rendered, `id=""`) {
		t.Error("expected auto-generated ID to be non-empty")
	}
}

func TestMount_ReturnsElementWithInitScript(t *testing.T) {
	opts := []echarts.ChartOption{
		echarts.WithElementID("my-chart"),
		echarts.WithChartType(echarts.TypeLine),
		echarts.WithData([][]any{{1, 10}}),
	}

	chart := echarts.NewChart(opts...)
	elem := chart.Mount()

	if elem == nil {
		t.Fatal("expected non-nil h.H from Mount()")
	}

	rendered := renderElement(elem)
	if !strings.Contains(rendered, `id="my-chart"`) {
		t.Error("expected rendered element to have id='my-chart'")
	}
	if !strings.Contains(rendered, "echarts.init") {
		t.Error("expected rendered element to include echarts initialization JS")
	}
}

func renderElement(e h.H) string {
	if e == nil {
		return ""
	}
	var sb strings.Builder
	e.Render(&sb)
	return sb.String()
}
