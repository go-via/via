package demos

import (
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

// The atomic is the store; the topic announces that it moved. Exported for the
// via.StateTrack literal in content/live.go.
var (
	SharedHits  atomic.Int64
	SharedTopic = topic.New[int64]()
)

func resetShared() {
	SharedHits.Store(0)
	SharedTopic.Publish(0)
}

// Hits follows the counter through a via.StateTrack literal set where the page
// is mounted; nothing in this file has to run for that.
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
	return Shared{Lim: lim, Hits: via.StateTrack(SharedTopic, SharedHits.Load)}
}

func (s *Shared) Inc(ctx *via.Ctx) {
	if !s.Lim.Allow(ctx) {
		s.Notice.Set("slow down — 20 clicks a minute")
		return
	}
	s.Notice.Set("")
	SharedTopic.Publish(SharedHits.Add(1))
}

func (s *Shared) View() h.H {
	return h.Div(
		h.P(h.Class("metric"), s.Hits.Display()),
		h.Div(h.Class("row"),
			h.Button(via.On("click", s.Inc), h.Str("+1 for everyone")),
			h.Span(h.Class("note"), s.Notice.Display()),
		),
	)
}
