// Package echarts provides an Apache ECharts plugin for the Via engine.
//
// Quick start:
//
//	app := via.New(via.WithPlugins(echarts.Plugin()))
//
// Construct a chart with options, mount it in View, push runtime updates
// over SSE:
//
//	chart := echarts.NewChart(
//	    echarts.WithElementID("cpu-chart"),
//	    echarts.WithTitle("CPU"),
//	    echarts.WithDimensions("100%", "300px"),
//	    echarts.WithThemeOverride(echarts.ThemeDark),
//	)
//	// in View():
//	chart.Mount()
//	// in an action / OnConnect ticker:
//	chart.SetSeries(ctx, echarts.Line("CPU", points))
//
// # Series helpers
//
// Base helpers: Line, Bar, Scatter, Pie, Heatmap. Each accepts variadic
// SeriesOption args for composable customization. Use Points to zip
// parallel xs/ys slices into the [][]any pair shape; Tail for sliding
// windows.
//
// Escape hatches for anything the typed helpers don't cover:
//   - WithInitialOption(opts) — chart-level options at construction
//   - Field(key, value)        — per-series fields
//   - SetOption(ctx, opts)     — chart-level options at runtime
//
// SeriesOptions: Dense, Filled, Stacked, Color, LineWidth, Symbol,
// Smoothed, Stepped, EndLabel, ConnectNulls, Silent, Progressive,
// YAxisIndex, MarkLine, MarkArea, Field (escape hatch).
//
// Shortcut combos: LineDense, LineAreaDense, ScatterDense.
//
// # Dense time-series
//
// For streaming dashboards with continuous time data:
//
//	chart := echarts.NewChart(
//	    echarts.WithElementID("cpu"),
//	    echarts.WithTimeAxis(),               // continuous x + axis tooltip
//	    echarts.WithYAxisRange(0, 100),       // pin bounds, no rescale jitter
//	    echarts.WithYAxisFormat("{value} %"), // unit suffix on labels
//	    echarts.WithRightYAxis("{value} MB/s"), // optional 2nd axis for combo charts
//	    echarts.WithCompactGrid(),            // tight inset for grid layouts
//	    echarts.WithDataZoom(),               // interactive zoom + slider
//	    echarts.WithCrosshair(),              // cross-axis guides for read-off values
//	    echarts.WithLegend(false),            // hide legend for compact dashboards
//	    echarts.WithPalette("#ff6b6b", "#4ecdc4"),
//	)
//
//	// Tick handler: append one (timestamp, value) per series.
//	chart.AppendXY(ctx, 0, tsMs, cpuPct)
//	// Or in one call when multiple series share the tick's timestamp:
//	chart.AppendXYAt(ctx, tsMs, cpuPct, ramPct, diskPct)
//	// Or refresh the last N points of a sliding window:
//	chart.SetSeries(ctx, echarts.LineDense("CPU",
//	    echarts.Tail(history, 1000),
//	    echarts.EndLabel("{c} %"),  // live value floats at the latest point
//	))
//
// # Runtime methods
//
//   - SetOption / SetSeries: update arbitrary options or replace all
//     series. SetSeries(ctx) with no args clears all series.
//   - SetTitle / SetSubtitle / SetLegend: common header / chrome updates.
//   - SetYAxisRange / SetXAxisRange: pin axis bounds at runtime — adaptive
//     scale, programmatic time-window navigation.
//   - SetTheme: live-swap between ThemeLight and ThemeDark while
//     preserving series state (e.g. for a dark-mode toggle).
//   - SetLoading: toggle echarts' built-in spinner.
//   - AppendData / AppendPoint / AppendXY / AppendXYAt: stream new points
//     to a series without resending the whole dataset. AppendXY is the
//     terse single-series form; AppendXYAt batches one shared x across
//     multiple series in one call.
//   - Resize / Clear / Dispose: layout recompute, blank canvas, full
//     teardown (call Dispose on SPA unmount to avoid leaks).
//
// # Asset delivery
//
// The echarts build ships embedded in the binary (vendored from the
// pinned release), served at a content-hashed /via/assets/echarts/
// path with immutable cache headers — registration does no network I/O
// and pages reference no third-party origin by default.
//
//	echarts.Plugin(echarts.WithSource("/static/echarts.min.js")) // self-host
//	echarts.Plugin(echarts.WithCDN(                              // CDN opt-in
//	    "https://cdn.jsdelivr.net/npm/echarts@6.0.0/dist/echarts.min.js",
//	    "sha384-…")) // SRI integrity is mandatory
//
// WithCDN requires a well-formed integrity hash for the exact build at
// that URL; the emitted tag carries integrity + crossorigin="anonymous".
// Running a different echarts version means supplying its URL and hash
// via WithCDN (or self-hosting it) — a bare WithVersion bump panics.
//
// # Linking charts on a dashboard
//
// Hover-tooltips sync across charts sharing a group name:
//
//	a := echarts.NewChart(echarts.WithGroup("dashboard"), ...)
//	b := echarts.NewChart(echarts.WithGroup("dashboard"), ...)
//
// Marshal failures inside SetOption / AppendData are returned as
// errors; programmer bugs in WithInitialOption panic eagerly.
package echarts
