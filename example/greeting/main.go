// Command greeting is a client-side reactive form: a text input two-way bound
// to a Signal, with the same Signal displayed live next to it. Datastar updates
// the greeting as you type — no action, no server round-trip, no client JS. The
// View has no '&', no identifier strings, and no closures at any call site.
package main

import (
	"cmp"
	"net/http"
	"os"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Greeting holds a single client-resident signal. Bind() wires it to the input
// two-way; Display() shows the same signal live — they share one wire name, so
// typing updates the greeting instantly in the browser.
type Greeting struct{ Name via.Signal[string] }

func (g *Greeting) View() h.H {
	return h.Div(
		h.H1(h.Str("Greeting")),
		h.Label(
			h.Str("Your name "),
			h.Input(g.Name.Bind(), h.RawAttr("placeholder", "type here")),
		),
		h.P(h.Str("Hello, "), g.Name.Display(), h.Str("!")),
	)
}

func main() {
	http.Handle("/", via.Register(Greeting{}))
	http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil)
}
