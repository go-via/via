package h_test

import (
	"fmt"
	"io"
	"testing"

	"github.com/go-via/via/h"
)

func renderCounterShape() h.H {
	return h.Div(
		h.ID("c"),
		h.H1(h.Text("Counter")),
		h.P(h.Text("Count: "), h.Span(h.Data("text", "$Hits"))),
		h.Button(h.Type("button"), h.Text("+"), h.Data("on:click", "@post('/_action/Inc')")),
		h.Button(h.Type("button"), h.Text("Reset"), h.Data("on:click", "@post('/_action/Reset')")),
	)
}

// counterShape_T replays the same tree using the short [T] alias so
// the bench captures the call-site brevity path alongside the longer
// [Text] form.
func renderCounterShape_T() h.H {
	return h.Div(
		h.ID("c"),
		h.H1(h.T("Counter")),
		h.P(h.T("Count: "), h.Span(h.Data("text", "$Hits"))),
		h.Button(h.Type("button"), h.T("+"), h.Data("on:click", "@post('/_action/Inc')")),
		h.Button(h.Type("button"), h.T("Reset"), h.Data("on:click", "@post('/_action/Reset')")),
	)
}

func BenchmarkCounterShape_construct(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = renderCounterShape()
	}
}

func BenchmarkCounterShape_render(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = renderCounterShape().Render(io.Discard)
	}
}

func BenchmarkCounterShape_T_construct(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = renderCounterShape_T()
	}
}

func BenchmarkCounterShape_T_render(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = renderCounterShape_T().Render(io.Discard)
	}
}

func BenchmarkWideList_construct(b *testing.B) {
	items := make([]string, 100)
	for i := range items {
		items[i] = "item"
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = h.Ul(h.Each(items, func(s string) h.H {
			return h.Li(h.Text(s))
		}))
	}
}

func BenchmarkWideList_render(b *testing.B) {
	items := make([]string, 100)
	for i := range items {
		items[i] = "item"
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = h.Ul(h.Each(items, func(s string) h.H {
			return h.Li(h.Text(s))
		})).Render(io.Discard)
	}
}

func BenchmarkDeepNest_render(b *testing.B) {
	build := func() h.H {
		n := h.Text("leaf")
		for range 12 {
			n = h.Div(n)
		}
		return n
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = build().Render(io.Discard)
	}
}

func BenchmarkAttrHeavy_render(b *testing.B) {
	build := func() h.H {
		return h.Input(
			h.Type("text"),
			h.Name("user"),
			h.Placeholder("login"),
			h.Value(""),
			h.ID("u"),
			h.Class("input"),
			h.Data("model", "$user"),
			h.Data("on:input", "@post('/_action/Touch')"),
		)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = build().Render(io.Discard)
	}
}

func BenchmarkStaticHeader_render(b *testing.B) {
	// A typical "site shell" header that doesn't change per request
	// — pre-rendered with Static, then re-rendered many times.
	frag := h.Static(
		h.Header(
			h.Nav(
				h.Class("container"),
				h.Ul(h.Li(h.A(h.Href("/"), h.T("home")))),
				h.Ul(h.Li(h.A(h.Href("/about"), h.T("about")))),
			),
		),
	)
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = frag.Render(io.Discard)
	}
}

// SysmonShape mirrors the tree shape rendered by the system-monitor
// example (`internal/examples/sysmon`): a nav with a single title, an
// Article with two controls (a range input + a button), and four
// metric cards. Each card embeds a chart placeholder that — in the
// real app — is an echarts.Mount(); here it's an inert <div> with an
// id, which keeps the H-tree allocation profile faithful while
// keeping the bench dependency-free.
//
// The "live" values for the four metrics are passed in so the bench
// captures the dynamic-text allocation cost of per-render values.

func sysmonMetricCard(title, val, chartID string) h.H {
	return h.Article(
		h.Header(
			h.Div(h.Class("grid"),
				h.Strong(h.T(title)),
				h.Span(
					h.Style("text-align:right;font-size:1.4rem;font-weight:bold;font-variant-numeric:tabular-nums;white-space:nowrap"),
					h.T(val),
				),
			),
		),
		h.Div(h.ID(chartID)),
	)
}

func sysmonDualCard(title, l1, v1, l2, v2, chartID string) h.H {
	row := func(label, val string) h.H {
		return h.Span(
			h.Style("font-variant-numeric:tabular-nums;white-space:nowrap"),
			h.Small(h.T(label+": ")), h.T(val),
		)
	}
	return h.Article(
		h.Header(
			h.Div(h.Style("display:flex;justify-content:space-between;align-items:center;gap:0.5rem;flex-wrap:wrap"),
				h.Strong(h.T(title)),
				h.Div(h.Style("display:flex;gap:1rem"),
					row(l1, v1),
					row(l2, v2),
				),
			),
		),
		h.Div(h.ID(chartID)),
	)
}

func sysmonShape(cpu, ram, dr, dw, rx, tx string) h.H {
	return h.Body(
		h.Nav(h.Class("container-fluid"),
			h.Ul(h.Li(h.Strong(h.T("System Monitor")))),
		),
		h.Main(h.Class("container"),
			h.Article(
				h.Div(h.Class("grid"),
					h.Label(
						h.T("Sample interval: "),
						h.Span(h.Data("text", "$intervalMs")),
						h.T("ms"),
						h.Input(
							h.Type("range"),
							h.Min("50"),
							h.Max("2000"),
							h.Step("50"),
							h.Data("bind", "intervalMs"),
							h.Data("on:change", "@post('/_action/ApplyControls')"),
						),
					),
					h.Button(
						h.Data("text", "$running?'Pause':'Resume'"),
						h.Data("on:click", "@post('/_action/ToggleRunning')"),
					),
				),
			),
			sysmonMetricCard("CPU Load", cpu, "chart-cpu"),
			sysmonMetricCard("RAM Usage", ram, "chart-ram"),
			sysmonDualCard("Disk I/O", "Read", dr, "Write", dw, "chart-disk"),
			sysmonDualCard("Network", "RX", rx, "TX", tx, "chart-net"),
		),
	)
}

func BenchmarkSysmonShape_construct(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = sysmonShape("12.3%", "45.6%", "1.2 MB/s", "320 KB/s", "5.1 MB/s", "120 KB/s")
	}
}

func BenchmarkSysmonShape_render(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = sysmonShape("12.3%", "45.6%", "1.2 MB/s", "320 KB/s", "5.1 MB/s", "120 KB/s").
			Render(io.Discard)
	}
}

// sysmonTick models one frame of the streaming render loop: each
// tick, the metric values change (different bytes — measured via the
// %d suffix that varies per iteration), and the page View is
// re-rendered to the SSE writer. The tree backbone is identical to
// the real Sysmon view; the values are the only thing that varies
// — same shape the via runtime exercises when [via.Stream] fires.
func sysmonTickValues(i int) (string, string, string, string, string, string) {
	return fmt.Sprintf("%d.%d%%", 10+i%80, i%10),
		fmt.Sprintf("%d.%d%%", 40+i%50, (i*3)%10),
		fmt.Sprintf("%d.%d MB/s", i%9, i%10),
		fmt.Sprintf("%d KB/s", 100+i%500),
		fmt.Sprintf("%d.%d MB/s", i%5, i%10),
		fmt.Sprintf("%d KB/s", 50+i%400)
}

// BenchmarkSysmonStream_render replays the per-tick render the via
// runtime does after each [via.Stream] callback: View is invoked
// fresh, fed new metric values, and rendered to discard. This is the
// hot path that scales with tick rate × open tabs.
func BenchmarkSysmonStream_render(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cpu, ram, dr, dw, rx, tx := sysmonTickValues(i)
		_ = sysmonShape(cpu, ram, dr, dw, rx, tx).Render(io.Discard)
	}
}

// sysmonShape_signalBound mirrors the optimized Sysmon view — every
// metric value is a `<span data-text="$key">` rather than an inline
// string, so the View's bytes don't depend on the per-tick metric
// values at all. After this shape is rendered once at page load, the
// streaming callback patches signals over SSE without ever invoking
// View again.
func sysmonShape_signalBound() h.H {
	metricCard := func(title, key, chartID string) h.H {
		return h.Article(
			h.Header(
				h.Div(h.Class("grid"),
					h.Strong(h.T(title)),
					h.Span(
						h.Style("text-align:right;font-size:1.4rem;font-weight:bold;font-variant-numeric:tabular-nums;white-space:nowrap"),
						h.Data("text", "$"+key),
					),
				),
			),
			h.Div(h.ID(chartID)),
		)
	}
	dualCard := func(title, k1Label, k1, k2Label, k2, chartID string) h.H {
		row := func(label, key string) h.H {
			return h.Span(
				h.Style("font-variant-numeric:tabular-nums;white-space:nowrap"),
				h.Small(h.T(label+": ")),
				h.Span(h.Data("text", "$"+key)),
			)
		}
		return h.Article(
			h.Header(
				h.Div(h.Style("display:flex;justify-content:space-between;align-items:center;gap:0.5rem;flex-wrap:wrap"),
					h.Strong(h.T(title)),
					h.Div(h.Style("display:flex;gap:1rem"),
						row(k1Label, k1),
						row(k2Label, k2),
					),
				),
			),
			h.Div(h.ID(chartID)),
		)
	}
	return h.Body(
		h.Nav(h.Class("container-fluid"),
			h.Ul(h.Li(h.Strong(h.T("System Monitor")))),
		),
		h.Main(h.Class("container"),
			h.Article(
				h.Div(h.Class("grid"),
					h.Label(
						h.T("Sample interval: "),
						h.Span(h.Data("text", "$intervalMs")),
						h.T("ms"),
						h.Input(
							h.Type("range"), h.Min("50"), h.Max("2000"), h.Step("50"),
							h.Data("bind", "intervalMs"),
							h.Data("on:change", "@post('/_action/ApplyControls')"),
						),
					),
					h.Button(
						h.Data("text", "$running?'Pause':'Resume'"),
						h.Data("on:click", "@post('/_action/ToggleRunning')"),
					),
				),
			),
			metricCard("CPU Load", "cpuVal", "chart-cpu"),
			metricCard("RAM Usage", "ramVal", "chart-ram"),
			dualCard("Disk I/O", "Read", "diskR", "Write", "diskW", "chart-disk"),
			dualCard("Network", "RX", "netRX", "TX", "netTX", "chart-net"),
		),
	)
}

// BenchmarkSysmonShape_signalBound_render measures the page-load
// render cost AFTER the State→Signal optimization. The shape is the
// same as the inline-value variant, but every value site is now a
// fixed `data-text="$key"` span — datastar fills it client-side. This
// is the only View render Sysmon does per page session.
func BenchmarkSysmonShape_signalBound_render(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = sysmonShape_signalBound().Render(io.Discard)
	}
}

// BenchmarkSysmonStream_signalBound_tick measures one full per-tick
// cost on the optimized Sysmon: no View re-render, just a single
// Signal[string].Set for each of the 6 metrics, encoding the
// resulting payload to the SSE writer. The h-layer cost is zero —
// this bench captures only the signal-encode path so the comparison
// against the old [SysmonStream_render] (which paid the full View
// re-render) is honest.
func BenchmarkSysmonStream_signalBound_tick(b *testing.B) {
	// Simulate the encoding cost of a single signal patch frame
	// carrying the 6 fresh value strings. The actual SSE write path
	// JSON-encodes the signals map; here we approximate with a
	// strings.Builder over the same payload shape so the bench is
	// dependency-free.
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cpu, ram, dr, dw, rx, tx := sysmonTickValues(i)
		_, _ = fmt.Fprintf(io.Discard,
			`{"cpuVal":%q,"ramVal":%q,"diskR":%q,"diskW":%q,"netRX":%q,"netTX":%q}`,
			cpu, ram, dr, dw, rx, tx,
		)
	}
}

// BenchmarkSysmonStream_staticChrome_render is the same tick-shaped
// loop, but the nav + controls subtree (which never depends on per-
// tick values) is wrapped in [Static] so its bytes are written
// verbatim every tick. Only the four metric cards build fresh on
// each iteration. This is the realistic upper bound on what the
// current API gives a streaming page without changing call shape.
func BenchmarkSysmonStream_staticChrome_render(b *testing.B) {
	chrome := h.Static(h.Fragment(
		h.Nav(h.Class("container-fluid"),
			h.Ul(h.Li(h.Strong(h.T("System Monitor")))),
		),
		h.Article(
			h.Div(h.Class("grid"),
				h.Label(
					h.T("Sample interval: "),
					h.Span(h.Data("text", "$intervalMs")),
					h.T("ms"),
					h.Input(
						h.Type("range"),
						h.Min("50"),
						h.Max("2000"),
						h.Step("50"),
						h.Data("bind", "intervalMs"),
						h.Data("on:change", "@post('/_action/ApplyControls')"),
					),
				),
				h.Button(
					h.Data("text", "$running?'Pause':'Resume'"),
					h.Data("on:click", "@post('/_action/ToggleRunning')"),
				),
			),
		),
	))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cpu, ram, dr, dw, rx, tx := sysmonTickValues(i)
		_ = h.Body(
			chrome,
			h.Main(h.Class("container"),
				sysmonMetricCard("CPU Load", cpu, "chart-cpu"),
				sysmonMetricCard("RAM Usage", ram, "chart-ram"),
				sysmonDualCard("Disk I/O", "Read", dr, "Write", dw, "chart-disk"),
				sysmonDualCard("Network", "RX", rx, "TX", tx, "chart-net"),
			),
		).Render(io.Discard)
	}
}

// SysmonShape_staticChrome demonstrates the held-fragment pattern:
// the nav + controls subtree never changes per render, so wrapping it
// in Static() pre-renders it once. Only the four metric cards
// (changing labels) and main wrapping allocate per render.
func BenchmarkSysmonShape_staticChrome_render(b *testing.B) {
	chrome := h.Static(h.Fragment(
		h.Nav(h.Class("container-fluid"),
			h.Ul(h.Li(h.Strong(h.T("System Monitor")))),
		),
		h.Article(
			h.Div(h.Class("grid"),
				h.Label(
					h.T("Sample interval: "),
					h.Span(h.Data("text", "$intervalMs")),
					h.T("ms"),
					h.Input(
						h.Type("range"),
						h.Min("50"),
						h.Max("2000"),
						h.Step("50"),
						h.Data("bind", "intervalMs"),
						h.Data("on:change", "@post('/_action/ApplyControls')"),
					),
				),
				h.Button(
					h.Data("text", "$running?'Pause':'Resume'"),
					h.Data("on:click", "@post('/_action/ToggleRunning')"),
				),
			),
		),
	))
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = h.Body(
			chrome,
			h.Main(h.Class("container"),
				sysmonMetricCard("CPU Load", "12.3%", "chart-cpu"),
				sysmonMetricCard("RAM Usage", "45.6%", "chart-ram"),
				sysmonDualCard("Disk I/O", "Read", "1.2 MB/s", "Write", "320 KB/s", "chart-disk"),
				sysmonDualCard("Network", "RX", "5.1 MB/s", "TX", "120 KB/s", "chart-net"),
			),
		).Render(io.Discard)
	}
}
