package echarts

import "fmt"

// SeriesOption modifies a series options map in place. Pair with the
// base series helpers (Line, Bar, Scatter, Pie, Heatmap) to layer on
// optional behaviour without proliferating per-combination helpers:
//
//	echarts.Line("user", userPts, echarts.Stacked("cpu"))
//	echarts.Line("sys",  sysPts,  echarts.Stacked("cpu"))
//
// New options can be added without breaking existing call sites.
type SeriesOption func(map[string]any)

// Dense applies the standard high-density perf defaults. Type-aware:
// for line series it sets `showSymbol: false` + `sampling: "lttb"`;
// for scatter it sets `large: true` + `symbolSize: 2`. Other series
// types are left untouched — the option is safe to apply blindly.
func Dense() SeriesOption {
	return func(s map[string]any) {
		switch s["type"] {
		case "line":
			s["showSymbol"] = false
			s["sampling"] = "lttb"
		case "scatter":
			s["large"] = true
			s["symbolSize"] = 2
		}
	}
}

// Filled enables area fill on a line series — the empty `areaStyle: {}`
// unlocks echarts' default auto-derived fill color matching the line.
func Filled() SeriesOption {
	return func(s map[string]any) {
		s["areaStyle"] = map[string]any{}
	}
}

// MarkLine adds a horizontal threshold line at the given y value,
// labeled with `name` (shown in the legend and on hover). Standard
// for monitoring dashboards: "warn at 80%", "ceiling at 100 MB/s".
// Replaces the boilerplate `markLine: {data: [{yAxis: ..., name: ...}]}`
// nested config users would otherwise spell out manually.
func MarkLine(name string, y float64) SeriesOption {
	return func(s map[string]any) {
		entry := map[string]any{"yAxis": y, "name": name}
		if ml, ok := s["markLine"].(map[string]any); ok {
			if data, ok := ml["data"].([]any); ok {
				ml["data"] = append(data, entry)
				return
			}
		}
		s["markLine"] = map[string]any{"data": []any{entry}}
	}
}

// MarkArea highlights an x-range with a shaded background overlay,
// labeled with `name`. Standard for monitoring dashboards: incident
// windows, maintenance periods, deployment overlays. x1/x2 are `any`
// so callers pass int64 ms-timestamps on time-axis charts, strings
// on category-axis charts, or whatever else echarts accepts.
// Multiple calls accumulate into a single markArea.data list.
func MarkArea(name string, x1, x2 any) SeriesOption {
	return func(s map[string]any) {
		entry := []any{
			map[string]any{"xAxis": x1, "name": name},
			map[string]any{"xAxis": x2},
		}
		if ma, ok := s["markArea"].(map[string]any); ok {
			if data, ok := ma["data"].([]any); ok {
				ma["data"] = append(data, entry)
				return
			}
		}
		s["markArea"] = map[string]any{"data": []any{entry}}
	}
}

// YAxisIndex routes this series to a specific yAxis when the chart
// has multiple. Use for combo charts where two metrics with very
// different scales share a single chart — e.g. CPU% on the default
// left yAxis (index 0) and throughput in MB/s on a right-side yAxis
// (index 1) configured via WithInitialOption with a yAxis array.
func YAxisIndex(n int) SeriesOption {
	return func(s map[string]any) { s["yAxisIndex"] = n }
}

// Silent disables hover interactions on a series so it doesn't trigger
// tooltips or emphasis effects. Use for context lines (target,
// baseline, threshold reference) that should stay visually present
// without competing for the cursor's attention with the primary
// metric.
func Silent() SeriesOption {
	return func(s map[string]any) { s["silent"] = true }
}

// Progressive enables echarts' chunked rendering for very large
// series. `chunkSize` is the number of points painted per frame;
// `threshold` is the point count above which chunking activates.
// Complements `Dense()` — Dense downsamples (fewer points), while
// Progressive paints every point but spreads the work across frames
// so the main thread stays responsive. Use for 100k+ point series
// where downsampling would lose meaningful detail.
func Progressive(chunkSize, threshold int) SeriesOption {
	return func(s map[string]any) {
		s["progressive"] = chunkSize
		s["progressiveThreshold"] = threshold
	}
}

// ConnectNulls keeps the line continuous across null data points.
// Streaming dashboards routinely produce nulls when a sensor blips or
// a tick misses a sample — without this the line breaks at every
// gap, creating visual noise. With it, echarts interpolates a
// straight segment over the null so the underlying trend stays
// readable.
func ConnectNulls() SeriesOption {
	return func(s map[string]any) { s["connectNulls"] = true }
}

// EndLabel attaches a floating label at the latest data point of a
// line series — the streaming-dashboard UX win where current values
// are visible without hovering. Pass an empty formatter for the
// default (series name); pass an echarts label template otherwise:
//
//	echarts.EndLabel("")            // shows "CPU"
//	echarts.EndLabel("{a}: {c}")    // shows "CPU: 78.3"
//	echarts.EndLabel("{c} %")       // shows "78.3 %"
//
// The {a} and {c} tokens are echarts' label placeholders for series
// name and current value.
func EndLabel(formatter string) SeriesOption {
	return func(s map[string]any) {
		cfg := map[string]any{"show": true}
		if formatter != "" {
			cfg["formatter"] = formatter
		}
		s["endLabel"] = cfg
	}
}

// Stepped renders the line as steps rather than smooth/straight
// segments. `position` is "start", "middle", or "end" — echarts'
// three variants for which side of each step the riser sits on. Use
// for state-change time-series (signal traces, status events) where
// the value is held between samples rather than interpolated.
func Stepped(position string) SeriesOption {
	return func(s map[string]any) { s["step"] = position }
}

// Smoothed enables echarts' line smoothing (smooth: true). Useful for
// noisy time-series where the eye wants a flowing curve rather than
// jaggy point-to-point segments. echarts uses Catmull-Rom interpolation
// — it touches every sample, so smoothing doesn't hide data, just
// renders between points without sharp corners.
func Smoothed() SeriesOption {
	return func(s map[string]any) { s["smooth"] = true }
}

// Field is the escape hatch for any echarts series field the typed
// SeriesOptions don't cover yet — `smooth`, `xAxisIndex`, `emphasis`,
// `lineStyle.type`, custom tooltips, anything echarts accepts at the
// series level. Useful for one-off experiments without waiting for a
// typed helper.
//
//	echarts.Line("CPU", pts, echarts.Field("smooth", true))
//
// For deeply-nested config, pass the nested map directly:
//
//	echarts.Field("emphasis", map[string]any{"focus": "series"})
func Field(key string, value any) SeriesOption {
	return func(s map[string]any) { s[key] = value }
}

// Symbol sets the per-point marker size in pixels. Pairs with Color
// and LineWidth for full visual control — useful when neither the
// default (10px on scatter, varies on line) nor Dense's small marker
// (2px) is what you want. On line series, also set the option even
// when symbols are hidden; echarts uses the size for the hover dot.
func Symbol(size int) SeriesOption {
	return func(s map[string]any) { s["symbolSize"] = size }
}

// LineWidth sets the line series stroke width in pixels. Dense
// dashboards with many overlapping series benefit from thickness
// variation — 1px lines for crowded backgrounds, 3-4px for the
// metric you want to pop. Setting this on bar / scatter is harmless;
// echarts ignores lineStyle there.
func LineWidth(px int) SeriesOption {
	return func(s map[string]any) {
		s["lineStyle"] = map[string]any{"width": px}
	}
}

// Color sets a per-series color (CSS color string or hex). Use it
// when you want dashboard-wide palette consistency without relying
// on the chart-level positional `color` array, which silently
// reassigns colors if series order changes.
func Color(color string) SeriesOption {
	return func(s map[string]any) { s["color"] = color }
}

// Stacked groups this series under a named stack so echarts draws
// each series' area on top of the others rather than overlapping.
// Use for breakdown time-series — CPU by core, RAM by process group,
// any "contribution to total" pattern.
func Stacked(group string) SeriesOption {
	return func(s map[string]any) { s["stack"] = group }
}

func applySeriesOptions(s map[string]any, opts []SeriesOption) map[string]any {
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Tail returns the last n entries of data — or the whole slice if it
// has fewer than n entries. The sliding-window primitive for streaming
// dashboards: maintain the full history server-side, render only the
// most recent N points client-side without a panic on cold start.
//
//	chart.SetSeries(ctx, echarts.LineDense("CPU", echarts.Tail(history, 1000)))
//
// n=0 returns an empty (non-nil) slice for the trivial "render nothing
// yet" case during initial setup.
func Tail(data [][]any, n int) [][]any {
	if n <= 0 {
		return [][]any{}
	}
	if len(data) <= n {
		return data
	}
	return data[len(data)-n:]
}

// Points zips two parallel slices into the [][]any pair shape that
// Line / Bar / Scatter / Heatmap expect as their data argument. The
// canonical use is dense time-series streaming where callers maintain
// a `[]int64` of millisecond timestamps and a `[]float64` of values:
//
//	chart.SetSeries(ctx, echarts.Line("CPU", echarts.Points(times, vals)))
//
// Generic over X and Y so float-second timestamps, int counts, or any
// other shape work without an extra cast. Panics if the slices have
// different lengths — a silent truncate-or-pad would produce a chart
// with confusingly shifted data, so the mismatch should fail loudly
// at the call site.
func Points[X, Y any](xs []X, ys []Y) [][]any {
	if len(xs) != len(ys) {
		panic(fmt.Errorf("echarts: Points: length mismatch (xs=%d, ys=%d)", len(xs), len(ys)))
	}
	out := make([][]any, len(xs))
	for i := range xs {
		out[i] = []any{xs[i], ys[i]}
	}
	return out
}

// Line returns a line series options map suitable for SetSeries / SetOption.
// For time-series charts, data is a slice of [timestampMs, value] pairs;
// for category charts, [categoryName, value]. Variadic SeriesOption args
// layer on optional behaviour (Stacked, …) without per-combination helpers.
func Line(name string, data [][]any, opts ...SeriesOption) map[string]any {
	return applySeriesOptions(map[string]any{"type": "line", "name": name, "data": data}, opts)
}

// LineDense returns a line series tuned for high-density data:
// `Line` composed with `Dense()` (showSymbol off + LTTB sampling).
// Variadic opts layer additional behaviour on top, applied after
// the dense defaults — e.g. `LineDense(name, data, Color("#abc"))`.
func LineDense(name string, data [][]any, opts ...SeriesOption) map[string]any {
	return Line(name, data, append([]SeriesOption{Dense()}, opts...)...)
}

// Bar returns a bar series options map suitable for SetSeries / SetOption.
func Bar(name string, data [][]any, opts ...SeriesOption) map[string]any {
	return applySeriesOptions(map[string]any{"type": "bar", "name": name, "data": data}, opts)
}

// Scatter returns a scatter series options map suitable for SetSeries /
// SetOption. Data is a slice of [x, y] pairs.
func Scatter(name string, data [][]any, opts ...SeriesOption) map[string]any {
	return applySeriesOptions(map[string]any{"type": "scatter", "name": name, "data": data}, opts)
}

// LineAreaDense returns an area-filled, dense-tuned line series:
// `Line` composed with `Dense()` + `Filled()`. The standard shape for
// live CPU/RAM/throughput charts where the filled area carries the
// eye better than a bare line at high sample counts.
func LineAreaDense(name string, data [][]any, opts ...SeriesOption) map[string]any {
	return Line(name, data, append([]SeriesOption{Dense(), Filled()}, opts...)...)
}

// ScatterDense returns a scatter series tuned for high-density data:
// `Scatter` composed with `Dense()` (large-render mode + 2px symbol).
// Use for event clouds, sample distributions, or any scatter with
// thousands of points.
func ScatterDense(name string, data [][]any, opts ...SeriesOption) map[string]any {
	return Scatter(name, data, append([]SeriesOption{Dense()}, opts...)...)
}

// Heatmap returns a heatmap series options map suitable for SetSeries /
// SetOption. Data is a slice of [x, y, value] triples — typically two
// integer-indexed categories (e.g. hour-of-day, day-of-week) with a
// numeric intensity. Pair with a `visualMap` option (via SetOption /
// WithInitialOption) to map intensities to colors.
func Heatmap(name string, data [][]any, opts ...SeriesOption) map[string]any {
	return applySeriesOptions(map[string]any{"type": "heatmap", "name": name, "data": data}, opts)
}

// Pie returns a pie series options map suitable for SetSeries / SetOption.
// Echarts pie series expect data as a slice of {name, value} objects
// rather than the [x, y] pairs used by Line/Bar/Scatter; pass slices
// directly without wrapping.
func Pie(name string, slices []map[string]any, opts ...SeriesOption) map[string]any {
	return applySeriesOptions(map[string]any{"type": "pie", "name": name, "data": slices}, opts)
}
