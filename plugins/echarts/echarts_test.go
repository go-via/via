package echarts_test

import (
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
	"github.com/stretchr/testify/assert"
)

func renderElement(t *testing.T, e h.H) string {
	t.Helper()
	if e == nil {
		return ""
	}
	var sb strings.Builder
	e.Render(&sb)
	return sb.String()
}

func TestPlugin_returnsViaPlugin(t *testing.T) {
	t.Parallel()
	p := echarts.Plugin()
	assert.NotNil(t, p)
	var _ via.Plugin = p
}

func TestPlugin_defaultsAreValid(t *testing.T) {
	t.Parallel()
	p := echarts.Plugin()
	assert.NotNil(t, p)
}

func TestNewChart_returnsConfiguredChart(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(
		echarts.WithElementID("my-chart"),
		echarts.WithChartType(echarts.TypeLine),
		echarts.WithData([][]any{{1, 10}, {2, 20}}),
	)
	assert.NotNil(t, chart)
}

func TestNewChart_generatesDeterministicIDs(t *testing.T) {
	t.Parallel()
	c1 := echarts.NewChart()
	c2 := echarts.NewChart()
	js1 := c1.InitJS()
	js2 := c2.InitJS()
	assert.NotEqual(t, js1, js2, "two charts must produce different InitJS")
	assert.Equal(t, 1, strings.Count(js1, "var "))
	assert.Equal(t, 1, strings.Count(js2, "var "))
}

func TestNewChart_uniqueIDsUnderConcurrency(t *testing.T) {
	t.Parallel()
	const n = 100
	ids := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() {
			c := echarts.NewChart()
			ids <- c.InitJS()
		}()
	}
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		js := <-ids
		assert.False(t, seen[js], "duplicate InitJS output detected")
		seen[js] = true
	}
}

func TestChart_InitJS_containsExpectedOutput(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(
		echarts.WithElementID("my-chart"),
		echarts.WithChartType(echarts.TypeLine),
		echarts.WithTitle("Revenue"),
		echarts.WithXAxisLabel("Month"),
		echarts.WithYAxisLabel("USD"),
		echarts.WithData([][]any{{1, 10}, {2, 20}}),
	)
	js := chart.InitJS()

	tests := []struct {
		name string
		want string
	}{
		{"elementID", `"my-chart"`},
		{"chartType", `"line"`},
		{"title", `"Revenue"`},
		{"xAxisLabel", `"Month"`},
		{"yAxisLabel", `"USD"`},
		{"data as JSON", `[[1,10],[2,20]]`},
		{"echarts.init call", "echarts.init"},
		{"setOption call", ".setOption("},
		{"var declaration", "var "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, js, tt.want)
		})
	}
}

func TestChart_InitJS_setsUpdateAnimation(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart()
	js := chart.InitJS()
	assert.Contains(t, js, "animationDurationUpdate: 0", "animation off by default")
}

func TestChart_InitJS_customAnimationDuration(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(echarts.WithAnimationDuration(500))
	js := chart.InitJS()
	assert.Contains(t, js, "500")
}

func TestChart_InitJS_attachesResizeObserver(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(echarts.WithElementID("my-chart"))
	js := chart.InitJS()
	assert.Contains(t, js, "ResizeObserver")
	assert.Contains(t, js, ".resize()")
}

func TestChart_InitJS_producesValidJSON(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(
		echarts.WithData([][]any{{1, 10}, {2, 20}}),
	)
	js := chart.InitJS()
	assert.Contains(t, js, `[[1,10],[2,20]]`, "data must be JSON-encoded, not Go %%v format")
	assert.NotContains(t, js, `[[1 10]`, "Go %%v format must not appear")
}

func TestChart_InitJS_escapesSpecialChars(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		title string
		notIn string
	}{
		{"single quote", "O'Brien", "'O'Brien'"},
		{"double quote", `Say "hello"`, `Say "hello"`},
		{"backslash", `C:\path`, `C:\path`},
		{"newline", "line1\nline2", "line1\nline2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chart := echarts.NewChart(echarts.WithTitle(tt.title))
			js := chart.InitJS()
			assert.NotContains(t, js, tt.notIn, "unescaped value must not appear verbatim")
		})
	}
}

func TestChart_InitJS_escapesElementID(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(
		echarts.WithElementID(`x"><script>alert(1)</script>`),
	)
	js := chart.InitJS()
	assert.NotContains(t, js, `<script>`, "XSS payload must be escaped in elementID")
}

func TestChart_InitJS_nilData(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart()
	js := chart.InitJS()
	assert.Contains(t, js, "null")
}

func TestChart_InitJS_emptyData(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(echarts.WithData([][]any{}))
	js := chart.InitJS()
	assert.Contains(t, js, "[]")
}

func TestChart_InitJS_mixedTypes(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(
		echarts.WithData([][]any{{1, "hello", 3.14, true, nil}}),
	)
	js := chart.InitJS()
	assert.Contains(t, js, `"hello"`)
	assert.Contains(t, js, "3.14")
	assert.Contains(t, js, "true")
	assert.Contains(t, js, "null")
}

func TestChart_varName_usesCustomWhenSet(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(echarts.WithVarName("myChartVar"))
	js := chart.InitJS()
	assert.Contains(t, js, "var myChartVar")
	assert.Equal(t, 1, strings.Count(js, "var "))
}

func TestChart_Mount_appliesStyleAttribute(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		opts      []echarts.ChartOption
		wantStyle string
	}{
		{
			name:      "both width and height",
			opts:      []echarts.ChartOption{echarts.WithDimensions("100%", "400px")},
			wantStyle: `style="width:100%;height:400px"`,
		},
		{
			name:      "width only",
			opts:      []echarts.ChartOption{echarts.WithElementID("w"), echarts.WithDimensions("50%", "")},
			wantStyle: `style="width:50%;height:300px"`,
		},
		{
			name:      "height only",
			opts:      []echarts.ChartOption{echarts.WithElementID("h"), echarts.WithDimensions("", "300px")},
			wantStyle: `style="width:100%;height:300px"`,
		},
		{
			name:      "neither",
			opts:      []echarts.ChartOption{echarts.WithElementID("n")},
			wantStyle: `style="width:100%;height:300px"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chart := echarts.NewChart(tt.opts...)
			rendered := renderElement(t, chart.Mount())
			assert.Contains(t, rendered, tt.wantStyle)
		})
	}
}

func TestChart_Mount_returnsElementWithInitScript(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(
		echarts.WithElementID("my-chart"),
		echarts.WithChartType(echarts.TypeLine),
	)
	elem := chart.Mount()
	assert.NotNil(t, elem)
	rendered := renderElement(t, elem)
	assert.Contains(t, rendered, `id="my-chart"`)
	assert.Contains(t, rendered, "echarts.init")
}

func TestChart_Mount_generatesNonEmptyID(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(echarts.WithChartType(echarts.TypeLine))
	rendered := renderElement(t, chart.Mount())
	assert.NotContains(t, rendered, `id=""`)
}

func TestChart_AppendData_nilContextIsNoOp(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(echarts.WithElementID("test-chart"))
	assert.NotPanics(t, func() {
		chart.AppendData(nil, [][]any{{1, 100}})
	})
}

func TestChart_AppendData_emptyDataIsNoOp(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart(echarts.WithElementID("test-chart"))
	assert.NotPanics(t, func() {
		chart.AppendData(nil, [][]any{})
	})
}

func TestChart_SetOption_nilContextIsNoOp(t *testing.T) {
	t.Parallel()
	chart := echarts.NewChart()
	assert.NotPanics(t, func() {
		chart.SetOption(nil, map[string]any{"title": "x"})
	})
}

func TestMultipleCharts_uniqueVarNames(t *testing.T) {
	t.Parallel()
	c1 := echarts.NewChart()
	c2 := echarts.NewChart()
	assert.NotEqual(t, c1.InitJS(), c2.InitJS())
}

func BenchmarkChart_AppendData(b *testing.B) {
	chart := echarts.NewChart(echarts.WithElementID("bench"))
	data := [][]any{{1, 100}, {2, 200}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chart.AppendData(nil, data)
	}
}

func BenchmarkChart_AppendDataBatch(b *testing.B) {
	chart := echarts.NewChart(echarts.WithElementID("bench-batch"))
	d1 := [][]any{{1, 100}}
	d2 := [][]any{{2, 200}}
	d3 := [][]any{{3, 300}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chart.AppendDataBatch(nil, d1, d2, d3)
	}
}
