// Command chat is a live multi-user chat room with a presence count: messages
// typed in one tab appear in every connected tab, over one per-tab SSE stream.
package main

import (
	"cmp"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
)

// Message is one chat line. Plain app data.
type Message struct{ Who, Text string }

// Room is the shared, process-wide hub — app-land, not framework. One topic fans
// messages to every tab; another broadcasts the live head-count.
type Room struct {
	bus      *topic.Topic[Message]
	presence *topic.Topic[int64]
	mu       sync.Mutex
	online   int64
}

func NewRoom() *Room {
	return &Room{bus: topic.New[Message](), presence: topic.New[int64]()}
}
func (r *Room) join() { r.add(1) }
func (r *Room) part() { r.add(-1) }

// add holds mu across the change and the publish: two tabs joining at once
// would otherwise publish their counts in either order, and every tab could
// settle on the older one.
func (r *Room) add(n int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.online += n
	r.presence.Publish(r.online)
}

func (r *Room) count() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.online
}

// Chat is one connected tab's live child.
type Chat struct {
	room *Room

	Who    via.Signal[string] // round-trips so Send can author the message
	Draft  via.Signal[string] // two-way bound composer, cleared on send
	Log    via.List[Message]  // server-authoritative, pushed over SSE
	Online via.State[int64]   // presence count, pushed over SSE
}

func (c *Chat) OnInit(ctx *via.Ctx) error {
	ctx.Listen(c.room.bus, c.onMessage)
	c.Online.Track(ctx, c.room.presence, c.room.count)

	// Registered, not performed: OnInit also runs on the plain GET and on every
	// action, and only a real connection gets an OnConnect/OnDispose pair.
	ctx.OnConnect(c.room.join) // tell everyone the head-count rose
	ctx.OnDispose(c.room.part) // …and that it fell when this tab leaves
	return nil
}

func (c *Chat) onMessage(ctx *via.Ctx, m Message) { c.Log.Append(m) }

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
		h.Ul(c.Log.Each(c.row)),
		h.Form(on.Submit(c.Send),
			h.Label(h.Str("you "), h.Input(c.Who.Bind())),
			h.Input(c.Draft.Bind(), h.Placeholder("message")),
			h.Button(h.Str("send")),
		),
	)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	room := NewRoom()
	// via.Handler returns the *Router, so the live half is reachable: every
	// open tab is a goroutine of its own, and only Close drains them.
	r := via.Handler(Chat{room: room})
	srv := &http.Server{Addr: cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), Handler: r}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()

	// Close first: it ends every stream the way a closed tab does, so Shutdown
	// has no open SSE response left to block on until its own deadline.
	r.Close()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil {
		log.Print(err)
	}
}
