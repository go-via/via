package main

import (
	"log"
	"net/http"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
)

var (
	mu    sync.Mutex
	count int64
	moved = topic.New[int64]()
)

func add(n int64) {
	mu.Lock()
	defer mu.Unlock()
	count += n
	moved.Publish(count)
}

func load() int64 {
	mu.Lock()
	defer mu.Unlock()
	return count
}

type Counter struct{ N via.State[int64] }

func (c *Counter) Inc(ctx *via.Ctx) { add(1) }
func (c *Counter) Dec(ctx *via.Ctx) { add(-1) }

func (c *Counter) View() h.H {
	return h.Div(
		h.Button(on.Click(c.Dec), h.Str("-")),
		h.H1(c.N.Display()),
		h.Button(on.Click(c.Inc), h.Str("+")),
	)
}

func main() {
	r := via.Handler(Counter{N: via.StateTrack(moved, load)})
	err := http.ListenAndServe(":8080", r)
	r.Close()
	log.Fatal(err)
}
