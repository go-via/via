// Command shared is a counter every tab shares: the app owns the number, a
// topic announces each bump, and every connection's State tracks it.
package main

import (
	"cmp"
	"log"
	"net/http"
	"os"
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

type Counter struct {
	n     *atomic.Int64
	room  *topic.Topic[int64]
	Count via.State[int64]
}

func (c *Counter) OnInit(ctx *via.Ctx) error {
	c.Count.Track(ctx, c.room, c.n.Load)
	return nil
}

func (c *Counter) Inc(ctx *via.Ctx) { c.room.Publish(c.n.Add(1)) }

func (c *Counter) View() h.H {
	return h.Div(
		h.H1(h.Str("Shared counter")),
		h.P(h.Str("hits: "), c.Count.Display()),
		h.Button(via.On("click", c.Inc), h.Str("+")),
	)
}

func main() {
	http.Handle("/", via.Handler(Counter{n: new(atomic.Int64), room: topic.New[int64]()}))
	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
