# ⚡Via
Real-time engine for building reactive web applications in pure Go.


## Why Via?
Somewhere along the way, the web became tangled in layers of JavaScript, build chains, and frameworks stacked on frameworks.

Via takes a radical stance:

- No templates.
- No JavaScript.
- No transpilation.
- No hydration.
- No front-end fatigue.
- Single SSE stream.
- Full reactivity.
- Built-in Brotli compression.
- Pure Go.


## Example
```go
package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

type Counter struct{ Count int }

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
			return h.Div(
				h.P(h.Textf("Count: %d", data.Count)),
				h.Label(
					h.Text("Update Step: "),
					h.Input(h.Type("number"), step.Bind()),
				),
				h.Button(h.Text("Increment"), increment.OnClick()),
			)
		})
	})

	v.Start(":3000")
}
```


## 🚧 Experimental
Via is still a newborn.
- Version `0.1.0` nears.
- Expect chaos.

## Contributing
- Via is intentionally minimal and opinionated — and so is contributing.
- If you love Go, simplicity, and meaningful abstractions — Come along for the ride!
- Fork, branch, build, tinker with things, submit a pull request.
- Keep every line purposeful.
- Share feedback: open an issue or start a discussion.


## Credits

Via builds upon the work of these amazing projects:

- 🚀 [Datastar](https://data-star.dev) - The hypermedia powerhouse at the core of Via. It powers browser reactivity through Signals and enables real-time HTML/Signal patches over an always-on SSE event stream.
- 🧩 [Gomponents](https://maragu.dev/gomponents) - The awesome project that gifts Via with Go-native HTML composition superpowers through the `via/h` package.

> Thank you for building something that doesn’t just function — it inspires. 🫶
