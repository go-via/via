package demos

import (
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
)

// startMu covers the change and its publish together, so publishes leave in
// the order the count moved and the last one a tab sees is the current count.
var (
	startMu    sync.Mutex
	startCount int64
	startMoved = topic.New[int64]()
)

func startAdd(n int64) {
	startMu.Lock()
	defer startMu.Unlock()
	startCount += n
	startMoved.Publish(startCount)
}

func startLoad() int64 {
	startMu.Lock()
	defer startMu.Unlock()
	return startCount
}

func resetStart() {
	startMu.Lock()
	defer startMu.Unlock()
	startCount = 0
	startMoved.Publish(0)
}

// StartCounter is the live counter from Getting started: every tab on the page
// follows startCount through the topic, so a click in one redraws all of them.
type StartCounter struct {
	// Limiter: see shared_contract.go.
	Lim Limiter
	N   via.State[int64]
}

func NewStartCounter(lim Limiter) StartCounter {
	if lim == nil {
		panic("demos: NewStartCounter: StartCounter.Lim must not be nil")
	}
	return StartCounter{Lim: lim, N: via.StateTrack(startMoved, startLoad)}
}

func (c *StartCounter) Inc(ctx *via.Ctx) {
	if c.Lim.Allow(ctx) {
		startAdd(1)
	}
}

func (c *StartCounter) Dec(ctx *via.Ctx) {
	if c.Lim.Allow(ctx) {
		startAdd(-1)
	}
}

func (c *StartCounter) View() h.H {
	return h.Div(h.Class("row"),
		h.Button(on.Click(c.Dec), h.Str("−")),
		h.Output(c.N.Display()),
		h.Button(on.Click(c.Inc), h.Str("+")),
	)
}
