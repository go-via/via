// Chat is a live multi-user chatroom in one file. The message log is a
// single app-scoped slice; appending to it fans a re-render out to every
// connected tab across every session — so a line typed in one browser
// shows up instantly in all the others. No Broadcast, no WebSocket, no
// hand-written JS. Open it in two browsers side by side to watch it sync.
//
//	go run ./internal/examples/chat
//	open http://localhost:3000
package main

import (
	"net/http"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/plugins/picocss"
)

// recentWindow caps the shared log so a long-lived room can't grow the
// app store — and every fan-out render — without bound.
const recentWindow = 50

type Message struct {
	From, Body string
}

type Room struct {
	// Log is app-scoped: one room shared across every session and tab.
	// Update fans the new line out to every tab that read it in View.
	Log via.StateAppSlice[Message]
	// Name and Draft are tab-local and two-way bound to their inputs, so
	// the latest client values are injected before Send runs. Name rides
	// along on every message this tab sends.
	Name  via.SignalStr `via:"name,init=Anon"`
	Draft via.SignalStr `via:"draft"`
}

func (r *Room) Send(ctx *via.Ctx) {
	body := strings.TrimSpace(r.Draft.Read(ctx))
	if body == "" {
		return
	}
	name := strings.TrimSpace(r.Name.Read(ctx))
	if name == "" {
		name = "Anon"
	}
	_ = r.Log.Update(ctx, func(log []Message) ([]Message, error) {
		log = append(log, Message{From: name, Body: body})
		if len(log) > recentWindow {
			log = log[len(log)-recentWindow:]
		}
		return log, nil
	})
	r.Draft.Write(ctx, "")
}

func (r *Room) View(ctx *via.CtxR) h.H {
	// Reading Log here subscribes this tab; any Send anywhere re-renders it.
	return h.Main(h.Class("container"),
		h.H1(h.Text("Via Chat")),
		h.P(h.Small(h.Text("Open another browser — messages appear live in both."))),
		h.Article(h.Style("max-height:60vh;overflow-y:auto"),
			h.Each(r.Log.Read(ctx), func(m Message) h.H {
				return h.P(h.Strong(h.Text(m.From+": ")), h.Text(m.Body))
			}),
		),
		// Send is type=button + on.Click so the form's native submit can't
		// race the Datastar action POST (see internal/examples/todos).
		h.Form(h.Style("display:grid;grid-template-columns:auto 1fr auto;gap:0.5rem"),
			h.Input(h.Type("text"), r.Name.Bind(),
				h.Style("max-width:8rem"), h.Placeholder("name")),
			h.Input(h.Type("text"), r.Draft.Bind(),
				h.Placeholder("message…"), on.Key("Enter", r.Send)),
			h.Button(h.Type("button"), h.Text("Send"), on.Click(r.Send)),
		),
	)
}

func main() {
	app := via.New(
		via.WithTitle("Via Chat"),
		via.WithPlugins(picocss.Plugin()),
	)
	via.Mount[Room](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
