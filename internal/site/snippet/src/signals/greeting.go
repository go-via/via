package signals

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// snippet:start greeting
type Greeting struct {
	Name via.Signal[string] `via:"init=\"Ada\""`
}

func (g *Greeting) View() h.H {
	return h.Div(
		h.Input(g.Name.Bind()),
		h.P(h.Str("Hello, "), g.Name.Display()),
	)
}

// snippet:end

// GreetingHTML is the root element Greeting renders on first paint, as the
// page shows it. greeting_test.go pins it to a real render.
const GreetingHTML = `<div id="root" data-signals='{"name":"Ada"}'>
  <div>
    <input data-bind="name">
    <p>Hello, <span data-text="$name">Ada</span></p>
  </div>
</div>`
