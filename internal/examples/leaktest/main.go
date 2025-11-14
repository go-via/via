package main

import (
	"fmt"
	"runtime"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

type Counter struct{ Count int }

func PrintMemUsage(v *via.V) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("Alloc = %v MiB", m.Alloc/1024/1024)
	fmt.Printf("\tTotalAlloc = %v MiB", m.TotalAlloc/1024/1024)
	fmt.Printf("\tSys = %v MiB", m.Sys/1024/1024)
	fmt.Printf("\tNumGC = %v", m.NumGC)
	fmt.Printf("\tContexts = %v\n", v.ContextCount())
}

func main() {
	v := via.New()

	v.Page("/", func(c *via.Context) {

		data := Counter{Count: 0}
		step := c.Signal(1)

		increment := c.Action(func() {
			data.Count += step.Int()
			c.Sync()
		})

	c.View(func() h.H {
		PrintMemUsage(v)
		return h.Div(
				h.P(h.Textf("Count: %d", data.Count)),
				h.P(h.Span(h.Text("Step: ")), h.Span(step.Text())),
				h.Label(
					h.Text("Update Step: "),
					h.Input(h.Type("number"), step.Bind()),
				),
				h.Button(h.Text("Increment"), increment.OnClick()),
			)
		})
	})

	v.Start()
}
