package deploy

import (
	"context"
	"encoding/json"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

// snippet:start bus
// Bus is the part of a pub/sub client the bridge needs.
type Bus interface {
	Publish(ctx context.Context, subject string, payload []byte) error
	Subscribe(ctx context.Context, subject string) (<-chan []byte, error)
}

// snippet:end

type Message struct{ Who, Text string }

// snippet:start bridge
type Room struct {
	ID    string
	Bus   Bus
	Local *topic.Topic[Message]
}

// Bridge feeds what this pod hears on the bus into its local topic. Run one
// per room per pod, for the life of the process.
func (r *Room) Bridge(ctx context.Context) error {
	in, err := r.Bus.Subscribe(ctx, "room."+r.ID)
	if err != nil {
		return err
	}
	for b := range in {
		var m Message
		if json.Unmarshal(b, &m) == nil {
			r.Local.Publish(m)
		}
	}
	return ctx.Err()
}

// Post publishes outward, never to r.Local: the sender's own pod hears it back
// through Bridge like every other pod does.
func (r *Room) Post(ctx context.Context, m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return r.Bus.Publish(ctx, "room."+r.ID, b)
}

// snippet:end

// snippet:start listen
type Chat struct {
	Room *Room
	Log  via.List[Message]
}

func (c *Chat) OnInit(ctx *via.Ctx) error {
	ctx.Listen(c.Room.Local, func(_ *via.Ctx, m Message) { c.Log.Append(m) })
	return nil
}

// snippet:end

func (c *Chat) View() h.H {
	return h.Ul(c.Log.Each(func(m Message) h.H { return h.Li(h.Str(m.Who + ": " + m.Text)) }))
}
