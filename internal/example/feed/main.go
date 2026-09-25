// Command feed is a multi-user broadcast: one server-side publisher sends to a
// Topic and every connected browser shows the latest message. Open it in two tabs.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

// Feed is a live child fed by a shared Topic. recv updates Last and via
// element-patches the re-render over SSE.
type Feed struct {
	room *topic.Topic[string]
	Last via.State[string]
}

func (f *Feed) OnInit(ctx *via.Ctx) error {
	ctx.Listen(f.room, f.recv)
	return nil
}

func (f *Feed) recv(ctx *via.Ctx, msg string) { f.Last.Set(msg) }

func (f *Feed) View() h.H {
	return h.Div(
		h.H1(h.Str("Broadcast feed")),
		h.P(h.Str("latest: "), f.Last.Display()),
	)
}

func publish(ctx context.Context, room *topic.Topic[string]) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			room.Publish(fmt.Sprintf("broadcast #%d", n))
		}
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	room := topic.New[string]()
	go publish(ctx, room)

	http.Handle("/", via.Handler(Feed{room: room}))
	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
