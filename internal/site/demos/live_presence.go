package demos

import (
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

// presenceTabs is the head count and the topic tells every tab it moved.
// presenceMu covers each change and its publish together, as in shared.go, so
// the last count a tab sees is the current one.
var (
	presenceMu    sync.Mutex
	presenceTabs  int64
	presenceTopic = topic.New[int64]()
)

func presenceAdd(n int64) {
	presenceMu.Lock()
	defer presenceMu.Unlock()
	presenceTabs += n
	presenceTopic.Publish(presenceTabs)
}

func presenceLoad() int64 {
	presenceMu.Lock()
	defer presenceMu.Unlock()
	return presenceTabs
}

// LivePresence counts the tabs holding a stream open on this page. OnConnect
// and OnDispose run only on a real connection, so a crawler's GET, which
// renders the page but never connects, counts for nothing.
type LivePresence struct {
	Tabs via.State[int64]
}

func NewLivePresence() LivePresence {
	return LivePresence{Tabs: via.StateTrack(presenceTopic, presenceLoad)}
}

func (p *LivePresence) OnInit(ctx *via.Ctx) error {
	ctx.OnConnect(p.join)
	ctx.OnDispose(p.leave)
	return nil
}

func (p *LivePresence) join()  { presenceAdd(1) }
func (p *LivePresence) leave() { presenceAdd(-1) }

func (p *LivePresence) View() h.H {
	return h.Div(
		h.P(h.Class("metric"), p.Tabs.Display()),
		h.P(h.Class("note"), h.Str("tabs connected to this page, yours included")),
	)
}
