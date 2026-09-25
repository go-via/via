package main

import (
	"log"
	"net/http"
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// snippet:start teaser
type Counter struct{ n *atomic.Int64 }

func (c *Counter) Inc(ctx *via.Ctx) { c.n.Add(1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.n.Add(-1) }

// snippet:start view
func (c *Counter) View() h.H {
	return h.Div(
		h.Button(on.Click(c.Dec), h.Str("-")),
		h.H1(h.Str(c.n.Load())),
		h.Button(on.Click(c.Inc), h.Str("+")),
	)
}

// snippet:end
// snippet:end

func main() {
	r := via.Handler(Counter{n: new(atomic.Int64)})
	err := http.ListenAndServe(":8080", r)
	r.Close()
	log.Fatal(err)
}
