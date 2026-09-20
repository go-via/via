package demos

import (
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

// The store is the atomic; the topic only announces that it moved. State is
// per connection, so a number every visitor shares cannot live in one.
var (
	sharedHits  atomic.Int64
	sharedTopic = topic.New[int64]()
)

// ResetShared zeroes the counter and announces it, so every tab holding this
// page redraws instead of showing a total nobody can reach any more.
func ResetShared() {
	sharedHits.Store(0)
	sharedTopic.Publish(0)
}

// Shared keeps Hits equal to the process-wide counter: Track seeds from the
// store, re-seeds once the stream has subscribed (so a publish that beat the
// subscribe is not lost), then follows every publish.
//
// The literal form does the same job from the parent's side:
//
//	Shared{Hits: via.StateTrack(sharedTopic, sharedHits.Load)}
type Shared struct {
	Lim    Limiter
	Hits   via.State[int64]
	Notice via.State[string]
}

func (s *Shared) OnInit(ctx *via.Ctx) error {
	s.Hits.Track(ctx, sharedTopic, sharedHits.Load)
	return nil
}

func (s *Shared) Inc(ctx *via.Ctx) {
	if !s.Lim.Allow(ctx) {
		s.Notice.Set("slow down — 20 clicks a minute")
		return
	}
	s.Notice.Set("")
	sharedTopic.Publish(sharedHits.Add(1))
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
