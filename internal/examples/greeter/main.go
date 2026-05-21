// Greeter demonstrates a server-side Signal[string] driven by two actions.
//
//	go run ./internal/examples/greeter
package main

import (
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

type Greeter struct {
	Greeting via.SignalStr `via:"greeting,init=Hello..."`
}

func (g *Greeter) GreetBob(ctx *via.Ctx) {
	g.Greeting.Write(ctx, "Hello Bob!")
}

func (g *Greeter) GreetAlice(ctx *via.Ctx) {
	g.Greeting.Write(ctx, "Hello Alice!")
}

func (g *Greeter) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.P(h.Text("Greeting: "), g.Greeting.Text()),
		h.Button(h.Text("Greet Bob"), on.Click(g.GreetBob)),
		h.Button(h.Text("Greet Alice"), on.Click(g.GreetAlice)),
	)
}

func main() {
	app := via.New(via.WithTitle("Greeter"))
	via.Mount[Greeter](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
