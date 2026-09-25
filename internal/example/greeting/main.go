// Command greeting is a client-side reactive form: a text input two-way bound to
// a Signal, displayed live next to it. Datastar updates it as you type, and an
// expr built from the same Signal shows or hides the greeting — nothing here
// makes a request.
package main

import (
	"cmp"
	"log"
	"net/http"
	"os"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Bind() and Display() share one wire name, so typing updates the greeting.
type Greeting struct{ Name via.Signal[string] }

func (g *Greeting) View() h.H {
	return h.Div(
		h.H1(h.Str("Greeting")),
		h.Label(
			h.Str("Your name "),
			h.Input(g.Name.Bind(), h.Placeholder("type here")),
		),
		// Ref() is the client-side reference; Eq/Ne build the expression the
		// browser evaluates, with the operand JSON-encoded on the way out.
		h.P(h.DataShow(g.Name.Ref().Ne("")), h.Str("Hello, "), g.Name.Display(), h.Str("!")),
		h.P(h.DataShow(g.Name.Ref().Eq("")), h.Small(h.Str("The greeting appears once you type."))),
	)
}

func main() {
	http.Handle("/", via.Handler(Greeting{}))
	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
