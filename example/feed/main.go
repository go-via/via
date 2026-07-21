// Command feed is a multi-user broadcast. A server-side publisher sends to a
// Topic, and every connected browser's island Subscribes and shows the latest
// message live — one source fanning out to all screens, no client code, no
// WebSocket. Open it in two tabs to watch them update in lockstep. The View is
// pure and ctx-free; there is no '&' and no closure at any call site.
package main

import (
	"cmp"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

// Feed is a live island fed by a shared Topic. last holds the most recent
// broadcast; recv updates it and via element-patches the re-render over SSE.
type Feed struct {
	room *topic.Topic[string]
	last via.State[string]
}

func (f *Feed) OnConnect(ctx *via.Ctx) error {
	via.Listen(ctx, f.room, f.recv)
	return nil
}

func (f *Feed) recv(ctx *via.Ctx, msg string) { f.last.Set(msg) }

func (f *Feed) View() h.H {
	return h.Div(
		h.H1(h.Str("Broadcast feed")),
		h.P(h.Str("latest: "), f.last.Display()),
	)
}

func main() {
	room := topic.New[string]()
	go func() {
		for n := 1; ; n++ {
			time.Sleep(time.Second)
			room.Publish(fmt.Sprintf("broadcast #%d", n))
		}
	}()
	http.Handle("/", via.Register(Feed{room: room}))
	http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil)
}
