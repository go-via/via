package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func main() {
	v := via.New()

	v.Config(via.Configuration{
		DocumentTitle: "Via",
		DocumentHeadIncludes: []h.H{
			h.Link(h.Rel("stylesheet"), h.Href("https://cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css")),
			h.Script(h.Src("https://unpkg.com/echarts@6.0.0/dist/echarts.min.js")),
		},
	})

	v.Page("/", func(c *via.Context) {
		chartComp := c.Component(chartCompFn)

		c.View(func() h.H {
			return h.Div(h.Class("container"),
				chartComp(),
			)
		})
	})

	v.Start(":3000")
}

func chartCompFn(c *via.Context) {
	data := make([]float64, 1000)
	labels := make([]string, 1000)

	go func() {
		tkr := time.NewTicker(60 * time.Millisecond)
		defer tkr.Stop()
		for range tkr.C {
			labels = append(labels[1:], time.Now().Format("15:04:05.000"))
			data = append(data[1:], rand.Float64()*1000)
			labelsTxt, _ := json.Marshal(labels)
			dataTxt, _ := json.Marshal(data)

			c.ExecScript(fmt.Sprintf(`
			if (myChart)
				myChart.setOption({
					xAxis: [{data: %s}],
					series:[{data: %s}]
				});
			`, labelsTxt, dataTxt))
		}
	}()

	c.View(func() h.H {
		return h.Div(h.ID("chart"), h.Style("width:100%;height:400px;"),
			h.Script(h.Raw(`
				const prefersDark = window.matchMedia('(prefers-color-scheme: dark)');
				var myChart = echarts.init(document.getElementById('chart'), prefersDark.matches ? 'dark' : 'light');
				var option = {
					backgroundColor: prefersDark.matches ? 'transparent' : '#ffffff',
					animationDurationUpdate: 60,  //  affects updates/redraws
					tooltip: {
						trigger: 'axis',
						position: function (pt) {
							return [pt[0], '10%'];
						}
					},
					title: {
						left: 'center',
						text: 'Large Area Chart'
					},
					toolbox: {
						feature: {
							dataZoom: {
								yAxisIndex: 'none'
							},
							restore: {},
							saveAsImage: {}
						}
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
							start: 80,
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
								color: '#1488CC'
							},
							areaStyle: {
								color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
									{
										offset: 0,
										color: '#1488CC'
									},
									{
										offset: 1,
										color: '#2B32B2'
									}
								])
							},
							data: []
						}
					]
				};
				option && myChart.setOption(option);
			`)),
		)
	})
}
