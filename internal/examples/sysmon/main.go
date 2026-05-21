// Sysmon is a live system monitor: CPU, RAM, disk I/O, and network
// throughput, streamed to the browser over SSE. The data-collection
// goroutine fires only on OnConnect so bots that never open the SSE
// stream don't pay the cost.
//
//	go run ./internal/examples/sysmon
package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/plugins/echarts"
	"github.com/go-via/via/plugins/picocss"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

const (
	updateInterval = 1000 * time.Millisecond
	maxPoints      = 200
)

// Metric readers

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
	c, err := disk.IOCountersWithContext(context.Background())
	if err != nil {
		return diskSnapshot{t: time.Now()}
	}
	var r, w uint64
	for _, x := range c {
		r += x.ReadBytes
		w += x.WriteBytes
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
	c, err := net.IOCountersWithContext(context.Background(), false)
	if err != nil || len(c) == 0 {
		return netSnapshot{t: time.Now()}
	}
	return netSnapshot{c[0].BytesRecv, c[0].BytesSent, time.Now()}
}

func netBPS(prev, cur netSnapshot) (float64, float64) {
	dt := cur.t.Sub(prev.t).Seconds()
	if dt <= 0 {
		return 0, 0
	}
	return float64(cur.rx-prev.rx) / dt, float64(cur.tx-prev.tx) / dt
}

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

type histBuf struct {
	mu  sync.Mutex
	pts [][]any
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

func (b *histBuf) push(tsMs int64, v float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pts) >= maxPoints {
		b.pts = b.pts[1:]
	}
	b.pts = append(b.pts, []any{tsMs, math.Round(v*10) / 10})
}

func (b *histBuf) snapshot() [][]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.pts)
}

// Chart-option builders

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

// View helpers
//
// value is passed as h.H so callers can hand in a signal-bound text
// node directly — the rendered span carries `data-text="$key"` and
// datastar fills its content from each [Signal.Set] patch, so streaming
// updates skip the View render path entirely.

const metricValueStyle = "text-align:right;font-size:1.4rem;font-weight:bold;font-variant-numeric:tabular-nums;white-space:nowrap"

func metricCard(title string, value h.H, chart h.H) h.H {
	return h.Article(
		h.Header(
			h.Div(h.Class("grid"),
				h.Strong(h.T(title)),
				h.With(value, h.Style(metricValueStyle)),
			),
		),
		chart,
	)
}

func dualMetricCard(title, l1 string, v1 h.H, l2 string, v2 h.H, chart h.H) h.H {
	row := func(label string, val h.H) h.H {
		return h.Span(
			h.Style("font-variant-numeric:tabular-nums;white-space:nowrap"),
			h.Small(h.T(label+": ")), val,
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
		chart,
	)
}

// Composition

type Page struct {
	IntervalMs via.SignalNum[int] `via:"intervalMs,init=1000"`
	Running    via.SignalBool     `via:"running,init=true"`

	// Metric values are datastar-bound signals: the rendered view
	// emits `<span data-text="$key">`, then [via.Stream] just queues
	// signal patches per tick. The View is never re-rendered for a
	// metric update — bytes are sent as a tiny PatchSignals frame
	// instead of a full element fragment.
	CPUVal via.SignalStr `via:"cpuVal,init=--"`
	RAMVal via.SignalStr `via:"ramVal,init=--"`
	DiskR  via.SignalStr `via:"diskR,init=--"`
	DiskW  via.SignalStr `via:"diskW,init=--"`
	NetRX  via.SignalStr `via:"netRX,init=--"`
	NetTX  via.SignalStr `via:"netTX,init=--"`

	cpuChart  *echarts.Chart
	ramChart  *echarts.Chart
	diskChart *echarts.Chart
	netChart  *echarts.Chart

	cpuBuf, ramBuf, diskRBuf, diskWBuf, netRXBuf, netTXBuf *histBuf

	ticker *via.Ticker
}

func (p *Page) OnInit(ctx *via.Ctx) error {
	dims := echarts.WithDimensions("100%", "220px")
	dark := echarts.WithThemeOverride(echarts.ThemeDark)
	p.cpuChart = echarts.NewChart(echarts.WithElementID("chart-cpu"), dims, dark)
	p.ramChart = echarts.NewChart(echarts.WithElementID("chart-ram"), dims, dark)
	p.diskChart = echarts.NewChart(echarts.WithElementID("chart-disk"), dims, dark)
	p.netChart = echarts.NewChart(echarts.WithElementID("chart-net"), dims, dark)

	p.cpuBuf = newHistBuf()
	p.ramBuf = newHistBuf()
	p.diskRBuf = newHistBuf()
	p.diskWBuf = newHistBuf()
	p.netRXBuf = newHistBuf()
	p.netTXBuf = newHistBuf()
	return nil
}

func (p *Page) ApplyControls(ctx *via.Ctx) {
	p.ticker.SetInterval(time.Duration(p.IntervalMs.Read(ctx)) * time.Millisecond)
	if p.Running.Read(ctx) {
		p.ticker.Resume()
	} else {
		p.ticker.Pause()
	}
}

func (p *Page) ToggleRunning(ctx *via.Ctx) {
	v := !p.Running.Read(ctx)
	p.Running.Set(ctx, v)
	if v {
		p.ticker.Resume()
	} else {
		p.ticker.Pause()
	}
}

func (p *Page) OnConnect(ctx *via.Ctx) error {
	p.cpuChart.SetOption(ctx, timeAxisOpt("%", lineSeries("CPU", nil)))
	p.ramChart.SetOption(ctx, timeAxisOpt("%", lineSeries("RAM", nil)))
	p.diskChart.SetOption(ctx, timeAxisOpt("KB/s",
		lineSeries("Read", nil),
		lineSeries("Write", nil),
	))
	p.netChart.SetOption(ctx, timeAxisOpt("KB/s",
		lineSeries("RX", nil),
		lineSeries("TX", nil),
	))

	prevDisk := readDiskCounters()
	prevNet := readNetCounters()

	p.ticker = via.Stream(ctx, updateInterval, func(ctx *via.Ctx, _ time.Time) {
		now := time.Now().UnixMilli()
		cpuPct := readCPUPercent()
		ramPct := readMemPercent()
		curDisk := readDiskCounters()
		dr, dw := diskBPS(prevDisk, curDisk)
		prevDisk = curDisk
		curNet := readNetCounters()
		rx, tx := netBPS(prevNet, curNet)
		prevNet = curNet

		p.cpuBuf.push(now, cpuPct)
		p.ramBuf.push(now, ramPct)
		p.diskRBuf.push(now, dr/1e3)
		p.diskWBuf.push(now, dw/1e3)
		p.netRXBuf.push(now, rx/1e3)
		p.netTXBuf.push(now, tx/1e3)

		p.CPUVal.Set(ctx, fmt.Sprintf("%.1f%%", cpuPct))
		p.RAMVal.Set(ctx, fmt.Sprintf("%.1f%%", ramPct))
		p.DiskR.Set(ctx, fmtBytes(dr))
		p.DiskW.Set(ctx, fmtBytes(dw))
		p.NetRX.Set(ctx, fmtBytes(rx))
		p.NetTX.Set(ctx, fmtBytes(tx))

		p.cpuChart.SetSeries(ctx, lineSeries("CPU", p.cpuBuf.snapshot()))
		p.ramChart.SetSeries(ctx, lineSeries("RAM", p.ramBuf.snapshot()))
		p.diskChart.SetSeries(ctx,
			lineSeries("Read", p.diskRBuf.snapshot()),
			lineSeries("Write", p.diskWBuf.snapshot()),
		)
		p.netChart.SetSeries(ctx,
			lineSeries("RX", p.netRXBuf.snapshot()),
			lineSeries("TX", p.netTXBuf.snapshot()),
		)
	})
	return nil
}

// View emits the page shape once at first load. Every datastar-bound
// span (the 6 metric values, the interval label, the pause/resume
// button label) carries `data-text="$key"` — datastar fills it from
// the per-tab signal store, which the stream callback patches each
// tick. The view itself never re-renders during streaming.
func (p *Page) View(ctx *via.CtxR) h.H {
	return h.Body(
		h.Nav(h.Class("container-fluid"),
			h.Ul(h.Li(h.Strong(h.T("System Monitor")))),
		),
		h.Main(h.Class("container"),
			h.Article(
				h.Div(h.Class("grid"),
					h.Label(
						h.T("Sample interval: "),
						p.IntervalMs.Text(), h.T("ms"),
						h.Input(
							h.Type("range"),
							h.Min("50"),
							h.Max("2000"),
							h.Step("50"),
							p.IntervalMs.Bind(),
							on.Change(p.ApplyControls),
						),
					),
					h.Button(
						h.Data("text", "$running?'Pause':'Resume'"),
						on.Click(p.ToggleRunning),
					),
				),
			),
			metricCard("CPU Load", p.CPUVal.Text(), p.cpuChart.Mount()),
			metricCard("RAM Usage", p.RAMVal.Text(), p.ramChart.Mount()),
			dualMetricCard("Disk I/O",
				"Read", p.DiskR.Text(),
				"Write", p.DiskW.Text(),
				p.diskChart.Mount()),
			dualMetricCard("Network",
				"RX", p.NetRX.Text(),
				"TX", p.NetTX.Text(),
				p.netChart.Mount()),
		),
	)
}

func main() {
	app := via.New(
		via.WithTitle("System Monitor"),
		via.WithPlugins(
			picocss.Plugin(
				picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeSlate}),
				picocss.WithDarkMode(),
			),
			echarts.Plugin(),
		),
	)
	via.Mount[Page](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
