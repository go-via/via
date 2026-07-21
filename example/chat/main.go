// Command chat is the flagship showcase: a live, multi-user chat room with a
// presence count — and it reads like a static page. Messages typed in one tab
// appear in every connected tab, the "N online" header tracks connections, and
// there is no hand-written JavaScript, no WebSocket, no build step. Three signal
// kinds say where each value lives: Signal round-trips to the server, State is
// server-authoritative and pushed, List is server-authoritative slice state.
// Zero '&', no identifier strings, no closures at any call site.
package main

import (
	"net/http"
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

// Message is one chat line. Plain app data.
type Message struct{ Who, Text string }

// Room is the shared, process-wide hub — app-land, not framework. One topic fans
// messages to every tab; another broadcasts the live head-count.
type Room struct {
	bus      *topic.Topic[Message]
	presence *topic.Topic[int]
	online   atomic.Int64
}

func NewRoom() *Room {
	return &Room{bus: topic.New[Message](), presence: topic.New[int]()}
}
func (r *Room) join() { r.presence.Publish(int(r.online.Add(1))) }
func (r *Room) part() { r.presence.Publish(int(r.online.Add(-1))) }

// Chat is one connected tab's live island.
type Chat struct {
	room *Room

	Who    via.Signal[string] // round-trips so Send can author the message
	Draft  via.Signal[string] // two-way bound composer, cleared on send
	Log    via.List[Message]  // server-authoritative, pushed over SSE
	Online via.State[int]     // presence count, pushed over SSE
}

func (c *Chat) OnConnect(ctx *via.Ctx) error {
	msgs := c.room.bus.Subscribe()
	ctx.OnDispose(msgs.Stop)
	via.Subscribe(ctx, msgs.C(), c.onMessage)

	heads := c.room.presence.Subscribe()
	ctx.OnDispose(heads.Stop)
	via.Subscribe(ctx, heads.C(), c.onPresence)

	c.room.join()              // tell everyone the head-count rose
	ctx.OnDispose(c.room.part) // …and that it fell when this tab leaves
	return nil
}

func (c *Chat) onMessage(ctx *via.Ctx, m Message) { c.Log.Append(m) }
func (c *Chat) onPresence(ctx *via.Ctx, n int)    { c.Online.Set(n) }

// Send publishes the drafted line to everyone and clears the composer.
func (c *Chat) Send(ctx *via.Ctx) {
	if c.Draft.Get() == "" {
		return
	}
	c.room.bus.Publish(Message{Who: c.Who.Get(), Text: c.Draft.Get()})
	c.Draft.Set("")
}

func (c *Chat) row(m Message) h.H {
	return h.Li(h.B(h.Str(m.Who+": ")), h.Str(m.Text))
}

func (c *Chat) View() h.H {
	return h.Div(
		h.H1(h.Str("Room — "), c.Online.Display(), h.Str(" online")),
		h.Ul(via.Each(c.Log.Get(), c.row)),
		h.Form(via.OnSubmit(c.Send),
			h.Label(h.Str("you "), h.Input(c.Who.Bind())),
			h.Input(c.Draft.Bind(), h.RawAttr("placeholder", "message")),
			h.Button(h.Str("send")),
		),
	)
}

func main() {
	room := NewRoom()
	http.Handle("/", via.Register(Chat{room: room}))
	http.ListenAndServe(":8080", nil)
}
