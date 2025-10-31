# ⚡Via
Pure Go reactive web framework.

### Why Via?
Somewhere along the way, the web became tangled in layers of JavaScript, build chains, and frameworks stacked on frameworks.

Via takes a radical stance:
- No templates.
- No JavaScript.
- No transpilation.
- No hydration.
- No front-end fatigue.
- Single SSE stream.
- Full reactivity
- Pure Go.

### Example
```
```go
package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

type CounterState struct{ Count int }

func main() {
	v := via.New()
	v.Page("/", func(c *via.Context) {

		s := CounterState{Count: 0}

		step := c.Signal(1)

		increment := c.Action(func() {
			s.Count += step.Int()
			c.Sync()
		})

		c.View(func() h.H {
			return h.Div(
				h.P(h.Textf("Count: %d", s.Count)),
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

> ⚠️Via is in its infancy. Things will break often.
```
```

### Contributing
Via is intentionally minimal — and so is contributing.

If you love Go, precision, and small, meaningful abstractions — Come along for the ride.

Fork, branch, build, break things.

Follow the loop: Via → Context → State/Signals → View.

Keep every line purposeful.




### Credits

Via builds upon the work of these amazing projects:

- [Datastar](data-star.dev) - The hypermedia powerhouse powering Via's browser reactivity and real-time HTML and signal patches over a always-on SSE event stream.
- [Gomponents](maragu.dev/gomponents) - The awesome project that enables Via's Go-native HTML UI composition through the `via/h` package.
