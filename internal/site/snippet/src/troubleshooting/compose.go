package troubleshooting

import (
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

type Chat struct{ Draft via.Signal[string] }

func (c *Chat) Send(ctx *via.Ctx) { c.Draft.Set("") }

func (c *Chat) View() h.H {
	return h.Div(h.Input(c.Draft.Bind()), h.Button(on.Click(c.Send), h.Str("send")))
}

type Clock struct{ Now via.State[string] }

func (c *Clock) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Second, c.tick)
	return nil
}

func (c *Clock) tick(ctx *via.Ctx) { c.Now.Set(time.Now().Format(time.TimeOnly)) }

func (c *Clock) View() h.H { return h.P(c.Now.Display()) }

type Feed struct{ Items via.List[string] }

func (f *Feed) View() h.H { return h.Ul(f.Items.Each(f.item)) }

func (f *Feed) item(s string) h.H { return h.Li(h.Str(s)) }

// snippet:start siblings
type Dashboard struct {
	Chat  Chat
	Clock Clock
	Feed  Feed
}

func (d *Dashboard) View() h.H {
	return h.Main(via.Child(d.Chat), via.Child(d.Clock), via.Child(d.Feed))
}

// snippet:end
