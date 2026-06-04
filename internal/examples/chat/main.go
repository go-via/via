// Chat is a live multi-user chatroom in one file. The message log is an
// app-scoped event log: each line is an immutable event you Append, and the
// rendered list is a pure fold of the log. Appending fans a re-render out to
// every connected tab across every session — so a line typed in one browser
// shows up in all the others. No read-modify-write, no Broadcast, no WebSocket,
// no hand-written JS. Open it in two browsers side by side to watch it sync.
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

// recentWindow caps the rendered list so a long-lived room can't grow every
// fan-out render without bound. The trim lives in the fold (below), not in a
// read-modify-write — the event log itself is bounded later by snapshot +
// compaction, not by rewriting history.
const recentWindow = 50

type Message struct {
	From, Body string
}

// Posted is the immutable fact appended on every Send. The rendered []Message
// is a pure fold of the Posted log. Fold MUST be pure and must not mutate acc
// (two pods replaying the same log have to converge), so it copies before
// appending and trims to the most recent window.
type Posted struct {
	From, Body string
}

func (Posted) Fold(acc []Message, ev Posted) []Message {
	next := append(append([]Message(nil), acc...), Message{From: ev.From, Body: ev.Body})
	if len(next) > recentWindow {
		next = next[len(next)-recentWindow:]
	}
	return next
}

type Room struct {
	// Log is app-scoped: one room shared across every session and tab. Append a
	// Posted event; the per-key projector folds it and fans the new line out to
	// every tab that read the log in View.
	Log via.StateAppEvents[Posted, []Message]
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
	// Append never conflicts — no read-modify-write, no trim-Update. The fold
	// derives the list (and bounds it); the projector renders.
	_, _ = r.Log.Append(ctx, Posted{From: name, Body: body})
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
