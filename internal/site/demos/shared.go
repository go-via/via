package demos

import (
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
)

// sharedHits is the store and the topic announces that it moved. sharedMu
// covers each change and its publish together, so publishes leave in the order
// the count moved and the last one a tab sees is the current count.
var (
	sharedMu    sync.Mutex
	sharedHits  int64
	sharedTopic = topic.New[int64]()
)

func sharedAdd(n int64) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	sharedHits += n
	sharedTopic.Publish(sharedHits)
}

func sharedLoad() int64 {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	return sharedHits
}

func resetShared() {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	sharedHits = 0
	sharedTopic.Publish(0)
}

// Shared follows the counter through the via.StateTrack literal NewShared
// builds; the topic re-seeds it on every publish.
type Shared struct {
	// Limiter: see shared_contract.go.
	Lim    Limiter
	Hits   via.State[int64]
	Notice via.State[string]
}

// NewShared takes the limiter the page owns, and seeds Hits from the store the
// topic announces. A nil limiter is a wiring mistake, so it fails here rather
// than on the first render.
func NewShared(lim Limiter) Shared {
	if lim == nil {
		panic("demos: NewShared: Shared.Lim must not be nil")
	}
	return Shared{Lim: lim, Hits: via.StateTrack(sharedTopic, sharedLoad)}
}

func (s *Shared) Inc(ctx *via.Ctx) {
	if !s.Lim.Allow(ctx) {
		s.Notice.Set("slow down — 20 clicks a minute")
		return
	}
	s.Notice.Set("")
	sharedAdd(1)
}

func (s *Shared) View() h.H {
	return h.Div(
		h.P(h.Class("metric"), s.Hits.Display()),
		h.Div(h.Class("row"),
			h.Button(on.Click(s.Inc), h.Str("+1 for everyone")),
			h.Span(h.Class("note"), s.Notice.Display()),
		),
	)
}
