package echarts

import (
	"encoding/json"
	"fmt"

	"github.com/go-via/via"
)

// SetOption pushes an echarts options object to the chart over SSE. This
// is the escape hatch for configuring chart type, data, axes, series, and
// any other echarts feature — pass the raw echarts options shape. Returns
// an error when opts cannot be marshalled to JSON; in that case no script
// is queued.
func (c *Chart) SetOption(ctx *via.Ctx, opts map[string]any) error {
	b, err := json.Marshal(opts)
	if err != nil {
		return fmt.Errorf("echarts: marshal setOption: %v", err)
	}
	ctx.ExecScript(fmt.Sprintf("%s?.setOption(%s)", c.chartRef(), b))
	return nil
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
//
// Calling with no series clears the chart — emits setOption with an empty
// series array plus replaceMerge:['series'] so existing series are
// dropped rather than merged-by-index (echarts' default). Surfaces
// marshal errors from SetOption verbatim.
func (c *Chart) SetSeries(ctx *via.Ctx, series ...map[string]any) error {
	if len(series) == 0 {
		ctx.ExecScript(fmt.Sprintf(
			`%s?.setOption({"series":[]},{replaceMerge:['series']})`,
			c.chartRef(),
		))
		return nil
	}
	return c.SetOption(ctx, map[string]any{"series": series})
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
// trimming server-side). Returns an error when data cannot be marshalled
// to JSON; in that case no script is queued.
func (c *Chart) AppendData(ctx *via.Ctx, seriesIdx int, data [][]any) error {
	if len(data) == 0 {
		return nil
	}
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("echarts: marshal appendData: %v", err)
	}
	ctx.ExecScript(fmt.Sprintf(
		"%s?.appendData({seriesIndex:%d,data:%s})",
		c.chartRef(), seriesIdx, b,
	))
	return nil
}

// AppendPoint streams a single data point to the given series. Sugar
// over AppendData for the typical one-point-per-tick pattern, removing
// the outer-slice wrapping the variadic shape requires.
func (c *Chart) AppendPoint(ctx *via.Ctx, seriesIdx int, point []any) error {
	return c.AppendData(ctx, seriesIdx, [][]any{point})
}

// AppendXYAt streams one (x, y) sample to each of multiple series
// sharing the same x value — the typical multi-metric dashboard
// shape where CPU/RAM/disk all tick on the same wall-clock instant.
// Values are routed positionally: ys[0] → seriesIndex 0, ys[1] → 1,
// and so on. Each AppendData call is emitted into the same SSE
// frame, so the wire cost is identical to calling AppendXY N times,
// but the call site collapses N lines into one.
//
//	chart.AppendXYAt(ctx, ts, cpuPct, ramPct, diskPct)
//
// Returns the first non-nil error from the underlying AppendData
// calls, or nil if all succeeded.
func (c *Chart) AppendXYAt(ctx *via.Ctx, x any, ys ...any) error {
	for i, y := range ys {
		if err := c.AppendData(ctx, i, [][]any{{x, y}}); err != nil {
			return err
		}
	}
	return nil
}

// AppendXY is the terse entry point for dense streaming: pushes one
// (x, y) sample to a series without the caller having to box either
// the outer [][]any nor inner []any wraps. x/y are `any` so int64
// timestamps + float64 values (the canonical shape), float64 + float64,
// or string + int all work without coercion.
//
//	chart.AppendXY(ctx, 0, tsMs, cpuPct)
func (c *Chart) AppendXY(ctx *via.Ctx, seriesIdx int, x, y any) error {
	return c.AppendData(ctx, seriesIdx, [][]any{{x, y}})
}

// SetTitle updates the chart title at runtime. Sugar over SetOption for
// the common case of refreshing a header without rebuilding the whole
// options object. Pass an empty string to clear the title.
func (c *Chart) SetTitle(ctx *via.Ctx, title string) error {
	return c.SetOption(ctx, map[string]any{"title": map[string]any{"text": title}})
}

// SetSubtitle updates the chart subtitle (echarts' title.subtext) at
// runtime, leaving the main title untouched. Useful for status lines
// like "updated 12:04:33" in dashboards.
func (c *Chart) SetSubtitle(ctx *via.Ctx, subtitle string) error {
	return c.SetOption(ctx, map[string]any{"title": map[string]any{"subtext": subtitle}})
}

// SetXAxisRange updates the xAxis min/max at runtime — useful for
// programmatic navigation in dense time-series ("follow latest 5
// minutes", "jump to incident at 10:15"). min/max are `any` so
// callers pass int64 ms-timestamps for time-axis charts, strings for
// category-axis, or whatever else echarts accepts.
func (c *Chart) SetXAxisRange(ctx *via.Ctx, min, max any) error {
	return c.SetOption(ctx, map[string]any{
		"xAxis": map[string]any{"min": min, "max": max},
	})
}

// SetYAxisRange updates the yAxis min/max at runtime — runtime
// equivalent of WithYAxisRange. Use for adaptive-scale streams: start
// with a tight range, widen once a spike arrives without rebuilding
// the chart. Passing the same bounds repeatedly is harmless.
func (c *Chart) SetYAxisRange(ctx *via.Ctx, min, max float64) error {
	return c.SetOption(ctx, map[string]any{
		"yAxis": map[string]any{"min": min, "max": max},
	})
}

// SetLegend toggles the chart's legend visibility at runtime. Sugar
// over SetOption — emits {"legend":{"show":visible}}. Useful for
// collapsible dashboards or "compact mode" UIs that hide legends to
// save space.
func (c *Chart) SetLegend(ctx *via.Ctx, visible bool) error {
	return c.SetOption(ctx, map[string]any{"legend": map[string]any{"show": visible}})
}

// SetTheme switches the chart's theme at runtime. echarts.init locks
// the theme at construction, so this disposes the current instance,
// captures its options via getOption, re-inits on the same DOM element
// with the new theme, and replays the options so series and config
// survive the swap. The registry entry's .c field is mutated in place,
// so the ResizeObserver — which re-reads the registry on each fire —
// picks up the new instance naturally.
//
// Calling SetTheme before Mount or after Dispose is a safe no-op.
func (c *Chart) SetTheme(ctx *via.Ctx, theme EChartsTheme) {
	ctx.ExecScript(fmt.Sprintf(`(function(){
		var _e=window.__viaCharts&&window.__viaCharts[%d];
		if(_e&&_e.c){
			var _el=_e.c.getDom();
			var _opts=_e.c.getOption();
			var _g=_e.c.group;
			_e.c.dispose();
			_e.c=echarts.init(_el,%s);
			_e.c.setOption(_opts);
			if(_g){_e.c.group=_g;echarts.connect(_g);}
		}
	})()`, c.seq, mustJSON(theme)))
}

// SetLoading toggles echarts' built-in loading spinner over the chart.
// Show it while server-side data is being fetched; hide it once the
// updated series have been pushed. Uses the default echarts spinner
// styling; drop down to SetOption + a custom JS escape hatch if you
// need to pass non-default showLoading(type, opts) arguments.
func (c *Chart) SetLoading(ctx *via.Ctx, loading bool) {
	if loading {
		ctx.ExecScript(c.chartRef() + "?.showLoading()")
	} else {
		ctx.ExecScript(c.chartRef() + "?.hideLoading()")
	}
}

// Resize forces echarts to recompute the chart's layout. The
// ResizeObserver attached at Mount already handles bounding-box
// changes, but it does not fire when a CSS context shifts without a
// dimension change — e.g. a flex layout that nudges the chart's
// effective area after a sibling animates. Call this in those cases.
func (c *Chart) Resize(ctx *via.Ctx) {
	ctx.ExecScript(c.chartRef() + "?.resize()")
}

// Clear drops all components and series from the chart while keeping
// the instance alive — distinct from SetSeries(ctx) which clears only
// series (theme/axes/title remain) and from Dispose which destroys
// the instance entirely. After Clear the chart is a blank canvas;
// re-populate via SetOption to use it again.
func (c *Chart) Clear(ctx *via.Ctx) {
	ctx.ExecScript(c.chartRef() + "?.clear()")
}

// Dispose releases the echarts instance and its resources (canvas,
// listeners, ResizeObserver references). Call when a chart container is
// about to be unmounted from the DOM — otherwise the instance leaks for
// the lifetime of the page. Safe to call multiple times; subsequent
// SetOption/SetSeries/AppendData calls become no-ops via the existing
// instance-presence guard in the emitted script.
func (c *Chart) Dispose(ctx *via.Ctx) {
	ctx.ExecScript(fmt.Sprintf(`(function(){
		var _e=window.__viaCharts&&window.__viaCharts[%d];
		if(_e){
			if(_e.ro)_e.ro.disconnect();
			if(_e.c)_e.c.dispose();
			delete window.__viaCharts[%d];
		}
	})()`, c.seq, c.seq))
}
