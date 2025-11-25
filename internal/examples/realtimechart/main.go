package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via-plugin-picocss/picocss"
	"github.com/go-via/via/h"
)

func main() {
	v := via.New()

	v.Config(via.Options{
		LogLvl:  via.LogLevelDebug,
		DevMode: true,
		Plugins: []via.Plugin{
			picocss.Default,
		},
	})

	v.AppendToHead(
		h.Script(h.Src("https://unpkg.com/echarts@6.0.0/dist/echarts.min.js")),
	)

	v.Page("/", func(c *via.Context) {
		chartComp := c.Component(chartCompFn)

		c.View(func() h.H {
			return h.Div(h.Class("container"),
				h.Section(
					h.Nav(
						h.Ul(h.Li(h.H3(h.Text("⚡Via")))),
						h.Ul(
							h.Li(h.A(h.H5(h.Text("About")), h.Href("https://github.com/go-via/via"))),
							h.Li(h.A(h.H5(h.Text("Resources")), h.Href("https://github.com/orgs/go-via/repositories"))),
							h.Li(h.A(h.H5(h.Text("Say hi!")), h.Href("http://github.com/go-via/via/discussions"))),
						),
					),
				),
				chartComp(),
			)
		})
	})

	v.Start()
}

func chartCompFn(c *via.Context) {
	data := make([]float64, 1000)
	labels := make([]string, 1000)

	isLive := true

	isLiveSig := c.Signal("on")

	refreshRate := c.Signal("1")

	computedTickDuration := func() time.Duration {
		return 1000 / time.Duration(refreshRate.Int()) * time.Millisecond
	}

	updateData := c.Routine(func(r *via.Routine) {

		r.OnInterval(computedTickDuration(), func() {
			labels = append(labels[1:], time.Now().Format("15:04:05.000"))
			data = append(data[1:], rand.Float64()*10)
			labelsTxt, _ := json.Marshal(labels)
			dataTxt, _ := json.Marshal(data)

			c.ExecScript(fmt.Sprintf(`
				if (myChart)
					myChart.setOption({
						xAxis: [{data: %s}],
						series:[{data: %s}]
					},{
						notMerge:false,
						lazyUpdate:true
					});
				`, labelsTxt, dataTxt))
		})

	})
	updateData.Start()

	updateRefreshRate := c.Action(func() {
		updateData.UpdateInterval(computedTickDuration())
	})

	toggleIsLive := c.Action(func() {
		isLive = isLiveSig.Bool()
		if isLive {
			updateData.Start()
		} else {
			updateData.Stop()
		}
	})

	c.View(func() h.H {
		return h.Div(
			h.Div(h.ID("chart"), h.Style("width:100%;height:400px;"), h.Script(h.Raw(`
				var prefersDark = window.matchMedia('(prefers-color-scheme: dark)');
				var myChart = echarts.init(document.getElementById('chart'), prefersDark.matches ? 'dark' : 'light');
				var option = {
					backgroundColor: prefersDark.matches ? 'transparent' : '#ffffff',
					animationDurationUpdate: 0, // affects updates/redraws
					tooltip: {
						trigger: 'axis',
						position: function (pt) {
							return [pt[0], '10%'];
						}
					},
					title: {
						left: 'center',
						text: '📈 Real-Time Chart Example'
					},
					xAxis: {
						type: 'category',
						boundaryGap: false,
						data: [] 
					},
					yAxis: {
						type: 'value',
						boundaryGap: [0, '100%']
					},
					dataZoom: [
						{
							type: 'inside',
							start: 90,
							end: 100
						},
						{
							start: 0,
							end: 100 
						}
					],
					series: [
						{
							name: 'Fake Data',
							type: 'line',
							symbol: 'none',
							sampling: 'lttb',
							itemStyle: {
								color: '#e8ae01'
							},
							lineStyle: { color: '#e8ae01'},
							areaStyle: {
								color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
									{
										offset: 0,
										color: '#fecc63'
									},
									{
										offset: 1,
										color: '#c79400'
									}
								])
							},
							data: []
						}
					]
				};
				option && myChart.setOption(option);
			`))),
			h.Section(
				h.Article(
					h.H5(h.Text("Controls")),
					h.Hr(),
					h.Div(h.Class("grid"),
						h.FieldSet(
							h.Legend(h.Text("Live Data")),
							h.Input(h.Type("checkbox"), h.Role("switch"), isLiveSig.Bind(), toggleIsLive.OnChange()),
						),
						h.Label(h.Text("Refresh Rate (Hz) ― "), refreshRate.Text(),
							h.Input(h.Type("range"), h.Attr("min", "1"), h.Attr("max", "200"), refreshRate.Bind(), updateRefreshRate.OnChange()),
						),
					),
				),
			),
		)
	})
}
