package main

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/go-via/via"
	// "github.com/go-via/via-plugin-picocss/picocss"
	"github.com/go-via/via/h"
)

func main() {
	v := via.New()

	v.Config(via.Options{
		LogLvl:  via.LogLevelDebug,
		DevMode: true,
		Plugins: []via.Plugin{
			// picocss.Default,
		},
	})

	v.AppendToHead(
		h.Script(h.Src("https://unpkg.com/echarts@6.0.0/dist/echarts.min.js")),
	)

	v.Page("/", func(c *via.Context) {

		isLive := true

		isLiveSig := c.Signal("on")

		refreshRate := c.Signal("24")

		computedTickDuration := func() time.Duration {
			return 1000 / time.Duration(refreshRate.Int()) * time.Millisecond
		}

		updateData := c.OnInterval(computedTickDuration(), func() {
			ts := time.Now().UnixMilli()
			val := rand.ExpFloat64() * 10

			c.ExecScript(fmt.Sprintf(`
			if (myChart) {
				myChart.appendData({seriesIndex: 0, data: [[%d, %f]]});
				myChart.setOption({},{notMerge:false,lazyUpdate:true});
			};
		`, ts, val))
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
			return h.Div(h.Style("overflow-x:hidden"),
				h.Section(h.Class("container"),
					h.Nav(
						h.Ul(h.Li(h.H3(h.Text("⚡Via")))),
						h.Ul(
							h.Li(h.A(h.H5(h.Text("About")), h.Href("https://github.com/go-via/via"))),
							h.Li(h.A(h.H5(h.Text("Resources")), h.Href("https://github.com/orgs/go-via/repositories"))),
							h.Li(h.A(h.H5(h.Text("Say hi!")), h.Href("http://github.com/go-via/via/discussions"))),
						),
					),
				),
				h.Div(
					h.Div(h.ID("chart"), h.DataIgnoreMorph(), h.Style("width:100%;height:400px;"),
						h.Script(h.Raw(`
							var prefersDark = window.matchMedia('(prefers-color-scheme: dark)');
							var myChart = echarts.init(document.getElementById('chart'), prefersDark.matches ? 'dark' : 'light');
							var option = {
								backgroundColor: prefersDark.matches ? 'transparent' : '#ffffff',
								animationDurationUpdate: 0, // affects updates/redraws
								tooltip: {
									trigger: 'axis',
									position: function (pt) {
										return [pt[0], '10%'];
									},
									syncStrategy: 'closestSampledPoint',
									backgroundColor: prefersDark.matches ? '#13171fc0' : '#eeeeeec0',
									extraCssText: 'backdrop-filter: blur(2px); -webkit-backdrop-filter: blur(2px);'
								},
								title: {
									left: 'center',
									text: '📈 Real-Time Chart Example'
								},
								xAxis: {
									type: 'time',
									boundaryGap: false,
									axisLabel: {
										hideOverlap: true
									}
								},
								yAxis: {
									type: 'value',
									boundaryGap: [0, '100%'],
									min: 0,
									max: 100
								},
								dataZoom: [
									{
										type: 'inside',
										start: 1,
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
										sampling: 'max',
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
										large: true,
										data: []
									}
								]
							};
							option && myChart.setOption(option);
						`)),
					),
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
				),
			)
		})
	})

	v.Start()
}
