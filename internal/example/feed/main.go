// Command feed is a multi-user broadcast: one server-side publisher sends to a
// Topic and every connected browser lists each message. Open it in two tabs.
package main

import (
	"cmp"
	"context"
	"errors"
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

// Feed is a live child fed by a shared Topic. recv appends to Events and via
// element-patches the re-render over SSE.
type Feed struct {
	room   *topic.Topic[string]
	Events via.List[string]
}

// keep bounds Events: the publisher never stops, and each push re-renders the
// whole list.
const keep = 10

func (f *Feed) OnInit(ctx *via.Ctx) error {
	ctx.Listen(f.room, f.recv)
	return nil
}

func (f *Feed) recv(ctx *via.Ctx, msg string) {
	events := append(f.Events.Get(), msg)
	f.Events.Set(events[max(0, len(events)-keep):])
}

func (f *Feed) View() h.H {
	return h.Div(
		h.H1(h.Str("Broadcast feed")),
		h.Ul(f.Events.Each(f.row)),
	)
}

func (f *Feed) row(msg string) h.H { return h.Li(h.Str(msg)) }

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

	r := via.Handler(Feed{room: room})
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
