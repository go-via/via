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

type Message struct{ Who, Text string }

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

type Chat struct {
	room *Room

	Who    via.Signal[string]
	Draft  via.Signal[string]
	Log    via.List[Message]
	Online via.State[int64]
}

func (c *Chat) OnInit(ctx *via.Ctx) error {
	ctx.Listen(c.room.bus, c.onMessage)
	c.Online.Track(ctx, c.room.presence, c.room.count)

	ctx.OnConnect(c.room.join)
	ctx.OnDispose(c.room.part)
	return nil
}

func (c *Chat) onMessage(ctx *via.Ctx, m Message) { c.Log.Append(m) }

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
	r := via.Handler(Chat{room: room})
	srv := &http.Server{Addr: cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), Handler: r}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()

	r.Close()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil {
		log.Print(err)
	}
}
