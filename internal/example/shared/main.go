// Command shared is a counter every tab shares, tracked by a State literal on
// the field, so the page needs no OnInit at all.
package main

import (
	"cmp"
	"log"
	"net/http"
	"os"
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
)

type Counter struct {
	n    *atomic.Int64
	room *topic.Topic[int64]
	Hits via.State[int64]
}

func (c *Counter) Inc(ctx *via.Ctx) { c.room.Publish(c.n.Add(1)) }

func (c *Counter) View() h.H {
	return h.Div(
		h.H1(h.Str("Shared counter")),
		h.P(h.Str("hits: "), c.Hits.Display()),
		h.Button(on.Click(c.Inc), h.Str("+")),
	)
}

func main() {
	n := new(atomic.Int64)
	room := topic.New[int64]()
	http.Handle("/", via.Handler(Counter{n: n, room: room, Hits: via.StateTrack(room, n.Load)}))
	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
