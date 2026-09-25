package main

import (
	"log"
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

type Message struct{ Who, Text string }

type Room struct {
	bus *topic.Topic[Message]
}

func NewRoom() *Room {
	return &Room{bus: topic.New[Message]()}
}

type Chat struct {
	room *Room

	Who   via.Signal[string]
	Draft via.Signal[string]
	Log   via.List[Message]
}

func (c *Chat) OnInit(ctx *via.Ctx) error {
	ctx.Listen(c.room.bus, c.onMessage)
	return nil
}

func (c *Chat) onMessage(ctx *via.Ctx, m Message) { c.Log.Append(m) }

func (c *Chat) row(m Message) h.H {
	return h.Li(h.B(h.Str(m.Who+": ")), h.Str(m.Text))
}

func (c *Chat) View() h.H {
	return h.Div(
		h.H1(h.Str("Room")),
		h.Ul(c.Log.Each(c.row)),
		h.Label(h.Str("you "), h.Input(c.Who.Bind())),
		h.Input(c.Draft.Bind(), h.Placeholder("message")),
	)
}

func main() {
	room := NewRoom()
	r := via.Handler(Chat{room: room})
	err := http.ListenAndServe(":8080", r)
	r.Close()
	log.Fatal(err)
}
