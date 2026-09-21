package demos

import (
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

// pingEvent is addressed: one topic carries every visitor's pings and each
// unit drops the ones that are not its own. A topic per session would be a
// map of topics nobody ever cleans up.
type pingEvent struct {
	To  string
	Msg string
}

var pingTopic = topic.New[pingEvent]()

// Ping is per-user fan-out over a shared topic, keyed by the session id.
type Ping struct {
	// Limiter, keepRows and trim: see shared_contract.go.
	Lim    Limiter
	sid    string
	gone   chan struct{}
	Msgs   via.List[string]
	Notice via.State[string]
}

// NewPing takes the limiter the page owns; a nil one is a wiring mistake, so
// it fails here rather than on the first render.
func NewPing(lim Limiter) Ping {
	if lim == nil {
		panic("demos: NewPing: Ping.Lim must not be nil")
	}
	return Ping{Lim: lim}
}

func (p *Ping) OnInit(ctx *via.Ctx) error {
	p.sid = ctx.Session().ID()
	p.gone = make(chan struct{})
	ctx.Listen(pingTopic, p.recv)
	ctx.OnDispose(func() { close(p.gone) })
	return nil
}

func (p *Ping) recv(ctx *via.Ctx, e pingEvent) {
	if e.To != p.sid {
		return
	}
	p.Msgs.Append(e.Msg)
	trim(&p.Msgs, keepRows)
}

func (p *Ping) Send(ctx *via.Ctx) {
	if !p.Lim.Allow(ctx) {
		p.Notice.Set("slow down — 20 pings a minute")
		return
	}
	p.Notice.Set("scheduled")
	// A goroutine may not touch unit state; publishing is how it reaches one.
	// gone is closed by OnDispose, so a tab closed inside the three seconds
	// does not wake the whole page up to deliver a ping nobody waits for.
	to, gone := p.sid, p.gone
	time.AfterFunc(3*time.Second, func() {
		select {
		case <-gone:
			return
		default:
		}
		pingTopic.Publish(pingEvent{To: to, Msg: "ping at " + time.Now().Format("15:04:05")})
	})
}

func (p *Ping) View() h.H {
	return h.Div(
		h.Div(h.Class("row"),
			h.Button(via.On("click", p.Send), h.Str("ping me in 3s")),
			h.Span(h.Class("note"), p.Notice.Display()),
		),
		h.Ul(h.Class("loglist"), h.TabIndex(0), p.Msgs.Each(func(m string) h.H { return h.Li(h.Str(m)) })),
	)
}
