package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
	"github.com/go-via/via/plugins/picocss"
)

const (
	updateInterval = 1000 * time.Millisecond
	maxPoints      = 200
)

// --- metric types ---

type cpuStat struct{ user, nice, system, idle, iowait, irq, softirq, steal uint64 }

func (s cpuStat) total() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}
func (s cpuStat) active() uint64 { return s.total() - s.idle - s.iowait }

type diskStat struct {
	readSectors, writeSectors uint64
	t                         time.Time
}

type netStat struct {
	rxBytes, txBytes uint64
	t                time.Time
}

// --- /proc readers ---

func readCPU() (cpuStat, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuStat{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		var nums [8]uint64
		for i := 0; i < 8 && i+1 < len(fields); i++ {
			nums[i], _ = strconv.ParseUint(fields[i+1], 10, 64)
		}
		return cpuStat{nums[0], nums[1], nums[2], nums[3], nums[4], nums[5], nums[6], nums[7]}, nil
	}
	return cpuStat{}, fmt.Errorf("cpu line not found in /proc/stat")
}

func cpuPercent(prev, cur cpuStat) float64 {
	dTotal := float64(cur.total() - prev.total())
	if dTotal == 0 {
		return 0
	}
	return math.Round(float64(cur.active()-prev.active())/dTotal*1000) / 10
}

func readMem() (float64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var total, available uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = v
		case "MemAvailable:":
			available = v
		}
	}
	if total == 0 {
		return 0, fmt.Errorf("MemTotal missing from /proc/meminfo")
	}
	return math.Round(float64(total-available)/float64(total)*1000) / 10, nil
}

func isWholeDisk(name string) bool {
	if strings.HasPrefix(name, "nvme") {
		return !strings.Contains(name[4:], "p")
	}
	last := name[len(name)-1]
	return last >= 'a' && last <= 'z'
}

func readDisk() (diskStat, error) {
	f, err := os.Open("/proc/diskstats")
	if err != nil {
		return diskStat{}, err
	}
	defer f.Close()
	var rs, ws uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 14 || !isWholeDisk(fields[2]) {
			continue
		}
		r, _ := strconv.ParseUint(fields[5], 10, 64)
		w, _ := strconv.ParseUint(fields[9], 10, 64)
		rs += r
		ws += w
	}
	return diskStat{rs, ws, time.Now()}, nil
}

func diskBPS(prev, cur diskStat) (readBPS, writeBPS float64) {
	dt := cur.t.Sub(prev.t).Seconds()
	if dt <= 0 {
		return 0, 0
	}
	return float64((cur.readSectors-prev.readSectors)*512) / dt,
		float64((cur.writeSectors-prev.writeSectors)*512) / dt
}

func readNet() (netStat, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return netStat{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // header 1
	sc.Scan() // header 2
	var rx, tx uint64
	for sc.Scan() {
		line := sc.Text()
		col := strings.Index(line, ":")
		if col < 0 || strings.TrimSpace(line[:col]) == "lo" {
			continue
		}
		fields := strings.Fields(line[col+1:])
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fields[0], 10, 64)
		t, _ := strconv.ParseUint(fields[8], 10, 64)
		rx += r
		tx += t
	}
	return netStat{rx, tx, time.Now()}, nil
}

func netBPS(prev, cur netStat) (rxBPS, txBPS float64) {
	dt := cur.t.Sub(prev.t).Seconds()
	if dt <= 0 {
		return 0, 0
	}
	return float64(cur.rxBytes-prev.rxBytes) / dt,
		float64(cur.txBytes-prev.txBytes) / dt
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
		"xAxis":   map[string]any{"type": "time", "name": ""},
		"yAxis":   map[string]any{"name": yName},
		"series":  s,
	}
}

func lineSeries(name string, data [][]any) map[string]any {
	return map[string]any{
		"type":   "line",
		"name":   name,
		"symbol": "none",
		"smooth": false,
		"data":   data,
	}
}

// --- view helpers (accept h.H to avoid referencing unexported signalOf) ---

func metricCard(title string, valElem h.H, chart h.H) h.H {
	return h.Article(
		h.Header(
			h.Div(h.Class("grid"),
				h.Strong(h.Text(title)),
				h.Span(
					h.Attr("style", "text-align:right;font-size:1.4rem;font-weight:bold"),
					valElem,
				),
			),
		),
		chart,
	)
}

func dualMetricCard(title, label1 string, val1 h.H, label2 string, val2 h.H, chart h.H) h.H {
	return h.Article(
		h.Header(
			h.Div(h.Class("grid"),
				h.Strong(h.Text(title)),
				h.Span(
					h.Attr("style", "text-align:right"),
					h.Small(h.Text(label1+": ")), val1,
					h.Text("  ·  "),
					h.Small(h.Text(label2+": ")), val2,
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
			picocss.New(
				picocss.WithDefaultTheme(picocss.PicoThemeSlate),
				picocss.WithColorClasses(),
			),
			echarts.Plugin(),
		),
	)

	v.Page("/", func(c *via.Context) {
		// control signals
		intervalMs := via.Signal(c, int(updateInterval/time.Millisecond))
		running := via.Signal(c, true)

		type settings struct {
			intervalMs int
			running    bool
		}
		settingsCh := make(chan settings, 1)

		applyControls := c.Action(func() error {
			settingsCh <- settings{
				intervalMs: intervalMs.Get(c),
				running:    running.Get(c),
			}
			return nil
		})
		toggleRunning := c.Action(func() error {
			newVal := !running.Get(c)
			running.SetValue(newVal)
			settingsCh <- settings{
				intervalMs: intervalMs.Get(c),
				running:    newVal,
			}
			return nil
		})

		// current-value state (pre-formatted strings)
		cpuVal := via.State(c, "--")
		ramVal := via.State(c, "--")
		diskR := via.State(c, "--")
		diskW := via.State(c, "--")
		netRX := via.State(c, "--")
		netTX := via.State(c, "--")

		// charts (dimensions fixed at creation; Mount() needs no args)
		dims := echarts.WithDimensions("100%", "220px")
		cpuChart := echarts.NewChart(echarts.WithElementID("chart-cpu"), dims)
		ramChart := echarts.NewChart(echarts.WithElementID("chart-ram"), dims)
		diskChart := echarts.NewChart(echarts.WithElementID("chart-disk"), dims)
		netChart := echarts.NewChart(echarts.WithElementID("chart-net"), dims)

		// history buffers pre-filled with nil so the full time window shows immediately
		cpuBuf := newHistBuf()
		ramBuf := newHistBuf()
		diskRBuf := newHistBuf()
		diskWBuf := newHistBuf()
		netRXBuf := newHistBuf()
		netTXBuf := newHistBuf()

		done := make(chan struct{})
		c.Dispose(func() { close(done) })
		c.Init(func() {
			// Configure all charts: time axis, correct series count, no animation.
			cpuChart.SetOption(c, timeAxisOpt("%", lineSeries("CPU", nil)))
			ramChart.SetOption(c, timeAxisOpt("%", lineSeries("RAM", nil)))
			diskChart.SetOption(c, timeAxisOpt("B/s",
				lineSeries("Read", nil),
				lineSeries("Write", nil),
			))
			netChart.SetOption(c, timeAxisOpt("B/s",
				lineSeries("RX", nil),
				lineSeries("TX", nil),
			))

			// Seed initial readings for delta-based metrics.
			prevCPU, _ := readCPU()
			prevDisk, _ := readDisk()
			prevNet, _ := readNet()

			ticker := time.NewTicker(updateInterval)
			go func() {
				defer ticker.Stop()
				paused := false
				for {
					select {
					case <-done:
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

					curCPU, err := readCPU()
					cpu := 0.0
					if err == nil {
						cpu = cpuPercent(prevCPU, curCPU)
						prevCPU = curCPU
					}

					ram, _ := readMem()

					curDisk, err := readDisk()
					dr, dw := 0.0, 0.0
					if err == nil {
						dr, dw = diskBPS(prevDisk, curDisk)
						prevDisk = curDisk
					}

					curNet, err := readNet()
					rx, tx := 0.0, 0.0
					if err == nil {
						rx, tx = netBPS(prevNet, curNet)
						prevNet = curNet
					}

					cpuBuf.push(now, cpu)
					ramBuf.push(now, ram)
					diskRBuf.push(now, dr)
					diskWBuf.push(now, dw)
					netRXBuf.push(now, rx)
					netTXBuf.push(now, tx)

					cpuVal.Set(c, fmt.Sprintf("%.1f%%", cpu))
					ramVal.Set(c, fmt.Sprintf("%.1f%%", ram))
					diskR.Set(c, fmtBytes(dr))
					diskW.Set(c, fmtBytes(dw))
					netRX.Set(c, fmtBytes(rx))
					netTX.Set(c, fmtBytes(tx))
					c.Sync()

					cpuChart.SetOption(c, map[string]any{
						"series": []any{lineSeries("CPU", cpuBuf.pts)},
					})
					ramChart.SetOption(c, map[string]any{
						"series": []any{lineSeries("RAM", ramBuf.pts)},
					})
					diskChart.SetOption(c, map[string]any{
						"series": []any{
							lineSeries("Read", diskRBuf.pts),
							lineSeries("Write", diskWBuf.pts),
						},
					})
					netChart.SetOption(c, map[string]any{
						"series": []any{
							lineSeries("RX", netRXBuf.pts),
							lineSeries("TX", netTXBuf.pts),
						},
					})
				}
			}()
		})

		c.View(func() h.H {
			return h.Body(
				h.Nav(h.Class("container-fluid"),
					h.Ul(h.Li(h.Strong(h.Text("System Monitor")))),
					h.Ul(h.Li(
						h.Button(
							h.Class("outline secondary"),
							h.Data("on:click", "$_picoDarkMode=!$_picoDarkMode"),
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
									h.Attr("type", "range"),
									h.Attr("min", "50"),
									h.Attr("max", "2000"),
									h.Attr("step", "50"),
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
					h.Div(h.Class("grid"),
						metricCard("CPU Load", h.Text(cpuVal.Get(c)), cpuChart.Mount()),
						metricCard("RAM Usage", h.Text(ramVal.Get(c)), ramChart.Mount()),
					),
					h.Div(h.Class("grid"),
						dualMetricCard("Disk I/O", "Read", h.Text(diskR.Get(c)), "Write", h.Text(diskW.Get(c)), diskChart.Mount()),
						dualMetricCard("Network", "RX", h.Text(netRX.Get(c)), "TX", h.Text(netTX.Get(c)), netChart.Mount()),
					),
				),
			)
		})
	})

	v.Start()
}
