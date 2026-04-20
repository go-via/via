package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/go-via/via/internal/viaold"
	"github.com/go-via/via/internal/viaold/h"
	"github.com/go-via/via/internal/viaold/plugins/echarts"
	"github.com/go-via/via/internal/viaold/plugins/picocss"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

const (
	updateInterval = 1000 * time.Millisecond
	maxPoints      = 200
)

// --- metric readers ---

func readCPUPercent() float64 {
	pcts, err := cpu.Percent(0, false)
	if err != nil || len(pcts) == 0 {
		return 0
	}
	return math.Round(pcts[0]*10) / 10
}

func readMemPercent() float64 {
	m, err := mem.VirtualMemory()
	if err != nil {
		return 0
	}
	return math.Round(m.UsedPercent*10) / 10
}

type diskSnapshot struct {
	read, write uint64
	t           time.Time
}

func readDiskCounters() diskSnapshot {
	counters, err := disk.IOCountersWithContext(context.Background())
	if err != nil {
		return diskSnapshot{t: time.Now()}
	}
	var r, w uint64
	for _, c := range counters {
		r += c.ReadBytes
		w += c.WriteBytes
	}
	return diskSnapshot{r, w, time.Now()}
}

func diskBPS(prev, cur diskSnapshot) (float64, float64) {
	dt := cur.t.Sub(prev.t).Seconds()
	if dt <= 0 {
		return 0, 0
	}
	return float64(cur.read-prev.read) / dt, float64(cur.write-prev.write) / dt
}

type netSnapshot struct {
	rx, tx uint64
	t      time.Time
}

func readNetCounters() netSnapshot {
	counters, err := net.IOCountersWithContext(context.Background(), false)
	if err != nil || len(counters) == 0 {
		return netSnapshot{t: time.Now()}
	}
	return netSnapshot{counters[0].BytesRecv, counters[0].BytesSent, time.Now()}
}

func netBPS(prev, cur netSnapshot) (float64, float64) {
	dt := cur.t.Sub(prev.t).Seconds()
	if dt <= 0 {
		return 0, 0
	}
	return float64(cur.rx-prev.rx) / dt, float64(cur.tx-prev.tx) / dt
}

// --- helpers ---

func fmtBytes(bps float64) string {
	switch {
	case bps >= 1e9:
		return fmt.Sprintf("%.1f GB/s", bps/1e9)
	case bps >= 1e6:
		return fmt.Sprintf("%.1f MB/s", bps/1e6)
	case bps >= 1e3:
		return fmt.Sprintf("%.1f KB/s", bps/1e3)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}

// histBuf is a fixed-capacity sliding-window buffer for chart data points.
type histBuf struct{ pts [][]any }

func (b *histBuf) push(tsMs int64, v float64) {
	if len(b.pts) >= maxPoints {
		b.pts = b.pts[1:]
	}
	b.pts = append(b.pts, []any{tsMs, math.Round(v*10) / 10})
}

func newHistBuf() *histBuf {
	now := time.Now().UnixMilli()
	pts := make([][]any, maxPoints)
	for i := range pts {
		ts := now - int64(maxPoints-1-i)*int64(updateInterval/time.Millisecond)
		pts[i] = []any{ts, nil}
	}
	return &histBuf{pts: pts}
}

// --- chart option builders ---

func timeAxisOpt(yName string, series ...map[string]any) map[string]any {
	s := make([]any, len(series))
	for i, m := range series {
		s[i] = m
	}
	return map[string]any{
		"tooltip": map[string]any{"trigger": "axis"},
		"xAxis": map[string]any{
			"type":        "time",
			"minInterval": 2000,
			"axisLabel":   map[string]any{"hideOverlap": true, "formatter": "{HH}:{mm}:{ss}"},
		},
		"yAxis":  map[string]any{"name": yName},
		"series": s,
	}
}

func lineSeries(name string, data [][]any) map[string]any {
	return map[string]any{
		"type":     "line",
		"name":     name,
		"symbol":   "none",
		"smooth":   false,
		"sampling": "lttb",
		"large":    true,
		"data":     data,
	}
}

// --- view helpers ---

func metricCard(title string, valElem h.H, chart h.H) h.H {
	return h.Article(
		h.Header(
			h.Div(h.Class("grid"),
				h.Strong(h.Text(title)),
				h.Span(
					h.Style("text-align:right;font-size:1.4rem;font-weight:bold;font-variant-numeric:tabular-nums;white-space:nowrap"),
					valElem,
				),
			),
		),
		chart,
	)
}

func dualMetricCard(title, label1 string, val1 h.H, label2 string, val2 h.H, chart h.H) h.H {
	valSpan := func(label string, val h.H) h.H {
		return h.Span(
			h.Style("font-variant-numeric:tabular-nums;white-space:nowrap"),
			h.Small(h.Text(label+": ")), val,
		)
	}
	return h.Article(
		h.Header(
			h.Div(h.Style("display:flex;justify-content:space-between;align-items:center;gap:0.5rem;flex-wrap:wrap"),
				h.Strong(h.Text(title)),
				h.Div(h.Style("display:flex;gap:1rem"),
					valSpan(label1, val1),
					valSpan(label2, val2),
				),
			),
		),
		chart,
	)
}

// --- main ---

func main() {
	v := via.New(
		via.WithTitle("System Monitor"),
		via.WithPlugins(
			picocss.Plugin(
				picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeSlate}),
				picocss.WithDarkMode(),
			),
			echarts.Plugin(),
		),
	)

	v.Page("/", func(cmp *via.Cmp) {
		// control signals
		intervalMs := via.Signal(cmp, int(updateInterval/time.Millisecond))
		running := via.Signal(cmp, true)

		type settings struct {
			intervalMs int
			running    bool
		}
		settingsCh := make(chan settings, 1)

		applyControls := cmp.Action(func(ctx *via.Ctx) error {
			settingsCh <- settings{
				intervalMs: intervalMs.Get(ctx),
				running:    running.Get(ctx),
			}
			return nil
		})
		toggleRunning := cmp.Action(func(ctx *via.Ctx) error {
			newVal := !running.Get(ctx)
			running.SetValue(ctx, newVal)
			settingsCh <- settings{
				intervalMs: intervalMs.Get(ctx),
				running:    newVal,
			}
			return nil
		})

		darkMode := via.Signal(cmp, true)

		// current-value state (pre-formatted strings)
		cpuVal := via.State(cmp, "--")
		ramVal := via.State(cmp, "--")
		diskR := via.State(cmp, "--")
		diskW := via.State(cmp, "--")
		netRX := via.State(cmp, "--")
		netTX := via.State(cmp, "--")

		// charts
		dims := echarts.WithDimensions("100%", "220px")
		dark := echarts.WithThemeOverride(echarts.ThemeDark)
		cpuChart := echarts.NewChart(echarts.WithElementID("chart-cpu"), dims, dark)
		ramChart := echarts.NewChart(echarts.WithElementID("chart-ram"), dims, dark)
		diskChart := echarts.NewChart(echarts.WithElementID("chart-disk"), dims, dark)
		netChart := echarts.NewChart(echarts.WithElementID("chart-net"), dims, dark)

		toggleDarkMode := cmp.Action(func(ctx *via.Ctx) error {
			isDark := !darkMode.Get(ctx)
			darkMode.SetValue(ctx, isDark)
			theme := echarts.ThemeLight
			if isDark {
				theme = echarts.ThemeDark
			}
			ctx.MarshalAndPatchSignals(map[string]any{"_picoDarkMode": isDark})
			cpuChart.SetTheme(ctx, theme)
			ramChart.SetTheme(ctx, theme)
			diskChart.SetTheme(ctx, theme)
			netChart.SetTheme(ctx, theme)
			return nil
		})

		// history buffers pre-filled so the full time window shows immediately
		cpuBuf := newHistBuf()
		ramBuf := newHistBuf()
		diskRBuf := newHistBuf()
		diskWBuf := newHistBuf()
		netRXBuf := newHistBuf()
		netTXBuf := newHistBuf()

		cmp.Init(func(ctx *via.Ctx) {
			cpuChart.SetOption(ctx, timeAxisOpt("%", lineSeries("CPU", nil)))
			ramChart.SetOption(ctx, timeAxisOpt("%", lineSeries("RAM", nil)))
			diskChart.SetOption(ctx, timeAxisOpt("KB/s",
				lineSeries("Read", nil),
				lineSeries("Write", nil),
			))
			netChart.SetOption(ctx, timeAxisOpt("KB/s",
				lineSeries("RX", nil),
				lineSeries("TX", nil),
			))

			prevDisk := readDiskCounters()
			prevNet := readNetCounters()

			ticker := time.NewTicker(updateInterval)
			go func() {
				defer ticker.Stop()
				paused := false
				for {
					select {
					case <-ctx.Done():
						return
					case s := <-settingsCh:
						paused = !s.running
						newInterval := time.Duration(s.intervalMs) * time.Millisecond
						ticker.Reset(newInterval)
						continue
					case <-ticker.C:
						if paused {
							continue
						}
					}

					now := time.Now().UnixMilli()

					cpuPct := readCPUPercent()
					ramPct := readMemPercent()

					curDisk := readDiskCounters()
					dr, dw := diskBPS(prevDisk, curDisk)
					prevDisk = curDisk

					curNet := readNetCounters()
					rx, tx := netBPS(prevNet, curNet)
					prevNet = curNet

					cpuBuf.push(now, cpuPct)
					ramBuf.push(now, ramPct)
					diskRBuf.push(now, dr/1e3)
					diskWBuf.push(now, dw/1e3)
					netRXBuf.push(now, rx/1e3)
					netTXBuf.push(now, tx/1e3)

					cpuVal.Set(ctx, fmt.Sprintf("%.1f%%", cpuPct))
					ramVal.Set(ctx, fmt.Sprintf("%.1f%%", ramPct))
					diskR.Set(ctx, fmtBytes(dr))
					diskW.Set(ctx, fmtBytes(dw))
					netRX.Set(ctx, fmtBytes(rx))
					netTX.Set(ctx, fmtBytes(tx))
					ctx.Sync()

					cpuChart.SetOption(ctx, map[string]any{
						"series": []any{lineSeries("CPU", cpuBuf.pts)},
					})
					ramChart.SetOption(ctx, map[string]any{
						"series": []any{lineSeries("RAM", ramBuf.pts)},
					})
					diskChart.SetOption(ctx, map[string]any{
						"series": []any{
							lineSeries("Read", diskRBuf.pts),
							lineSeries("Write", diskWBuf.pts),
						},
					})
					netChart.SetOption(ctx, map[string]any{
						"series": []any{
							lineSeries("RX", netRXBuf.pts),
							lineSeries("TX", netTXBuf.pts),
						},
					})
				}
			}()
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Body(
				h.Nav(h.Class("container-fluid"),
					h.Ul(h.Li(h.Strong(h.Text("System Monitor")))),
					h.Ul(h.Li(
						h.Button(
							h.Class("outline secondary"),
							toggleDarkMode.OnClick(),
							h.Text("Toggle dark mode"),
						),
					)),
				),
				h.Main(h.Class("container"),
					h.Article(
						h.Div(h.Class("grid"),
							h.Label(
								h.Text("Sample interval: "),
								intervalMs.Text(), h.Text("ms"),
								h.Input(
									h.Type("range"),
									h.Min("50"),
									h.Max("2000"),
									h.Step("50"),
									intervalMs.Bind(),
									applyControls.OnChange(),
								),
							),
							h.Button(
								h.Data("text", running.Ref()+"?'Pause':'Resume'"),
								toggleRunning.OnClick(),
							),
						),
					),
					metricCard("CPU Load", h.Text(cpuVal.Get(ctx)), cpuChart.Mount()),
					metricCard("RAM Usage", h.Text(ramVal.Get(ctx)), ramChart.Mount()),
					dualMetricCard("Disk I/O", "Read", h.Text(diskR.Get(ctx)), "Write", h.Text(diskW.Get(ctx)), diskChart.Mount()),
					dualMetricCard("Network", "RX", h.Text(netRX.Get(ctx)), "TX", h.Text(netTX.Get(ctx)), netChart.Mount()),
				),
			)
		})
	})

	v.Start()
}
