// Chatcluster is the single-file chat from internal/examples/chat, but wired to
// run as MANY nodes over a shared backplane. The message log is an app-scoped
// event log (each line is an immutable event you Append; the rendered list is a
// pure fold), so with a durable backplane the log is no longer pod-local: a line
// typed against one node fans out to every tab on every OTHER node. A banner
// shows which node served the page, so you can watch state cross between them.
//
// Run the whole cluster (NATS + two nodes) with Docker Compose:
//
//	docker compose -f internal/examples/chatcluster/docker-compose.yml up --build
//	open http://localhost:3001   # node one
//	open http://localhost:3002   # node two — type in one, watch it appear in both
//
// Or run a single node against your own NATS server:
//
//	NATS_URL=nats://localhost:4222 PORT=3001 NODE_NAME=node-one \
//	  go run ./internal/examples/chatcluster
//
// With NATS_URL unset it falls back to the in-process InMemory backplane, so it
// still runs as a normal single node with no infrastructure.
package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/plugins/picocss"
	"github.com/go-via/vianats"
	"github.com/nats-io/nats.go"
)

// recentWindow caps the rendered list so a long-lived room can't grow every
// fan-out render without bound. The trim lives in the fold (below), not in a
// read-modify-write — the event log itself is bounded by snapshot + compaction,
// not by rewriting history.
const recentWindow = 50

// nodeName identifies the process that serves a page. It is pod-local: every
// node sets its own, and it deliberately does NOT travel through the backplane
// (that carries chat state, not deployment identity). main() resolves it once.
var nodeName = "node"

type Message struct {
	From, Body string
}

// Posted is the immutable fact appended on every Send. The rendered []Message
// is a pure fold of the Posted log. Fold MUST be pure and must not mutate acc —
// two nodes replaying the same log have to converge — so it copies before
// appending and trims to the most recent window.
type Posted struct {
	From, Body string
}

func (Posted) Fold(acc []Message, ev Posted) []Message {
	next := append(append([]Message(nil), acc...), Message(ev))
	if len(next) > recentWindow {
		next = next[len(next)-recentWindow:]
	}
	return next
}

type Room struct {
	// Messages is app-scoped AND, with a backplane, cluster-scoped: one room
	// shared across every session, tab, and node. Append a Posted event; each
	// node's projector folds it and fans the new line out to its tabs.
	Messages via.StateAppEvents[Posted, []Message]
	// Name and Draft are tab-local and two-way bound to their inputs.
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
	// Append never conflicts — no read-modify-write. The fold derives (and
	// bounds) the list; every node's projector renders it.
	_, _ = r.Messages.Append(ctx, Posted{From: name, Body: body})
	r.Draft.Write(ctx, "")
}

func (r *Room) View(ctx *via.CtxR) h.H {
	// Reading Messages here subscribes this tab; any Send on any node re-renders it.
	return h.Main(h.Class("container"),
		h.H1(h.Text("Via Cluster Chat")),
		h.P(h.Small(h.Textf("served by %s — open another node and watch messages sync across both", nodeName))),
		h.Article(h.Style("max-height:60vh;overflow-y:auto"),
			h.Each(r.Messages.Read(ctx), func(m Message) h.H {
				return h.P(h.Strong(h.Text(m.From+": ")), h.Text(m.Body))
			}),
		),
		// Send is type=button + on.Click so the form's native submit can't race
		// the Datastar action POST.
		h.Form(h.Style("display:grid;grid-template-columns:auto 1fr auto;gap:0.5rem"),
			h.Input(h.Type("text"), r.Name.Bind(),
				h.Style("max-width:8rem"), h.Placeholder("name")),
			h.Input(h.Type("text"), r.Draft.Bind(),
				h.Placeholder("message…"), on.Key("Enter", r.Send)),
			h.Button(h.Type("button"), h.Text("Send"), on.Click(r.Send)),
		),
	)
}

// resolveNodeName picks this process's pod-local identity: an explicit NODE_NAME
// wins, else the hostname, else a constant — so the banner is never empty.
func resolveNodeName(getenv func(string) string, hostname func() (string, error)) string {
	if n := strings.TrimSpace(getenv("NODE_NAME")); n != "" {
		return n
	}
	if h, err := hostname(); err == nil && strings.TrimSpace(h) != "" {
		return strings.TrimSpace(h)
	}
	return "node"
}

// backplane returns a durable JetStream backplane when NATS_URL is set, else the
// in-process InMemory one so the example still runs with no infrastructure.
func backplane(url string) (via.Backplane, error) {
	if strings.TrimSpace(url) == "" {
		return via.InMemory(), nil
	}
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, err
	}
	bp, err := vianats.JetStream(nc)
	if err != nil {
		// JetStream failed after the connection opened (stream/KV setup, etc.).
		// Nothing owns nc on this path, so close it rather than leak it.
		nc.Close()
		return nil, err
	}
	return bp, nil
}

func main() {
	nodeName = resolveNodeName(os.Getenv, os.Hostname)

	bp, err := backplane(os.Getenv("NATS_URL"))
	if err != nil {
		log.Fatalf("backplane: %v", err)
	}

	app := via.New(
		via.WithTitle("Via Cluster Chat"),
		via.WithPlugins(picocss.Plugin()),
		via.WithBackplane(bp),
	)
	via.Mount[Room](app, "/")

	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "3000"
	}
	addr := ":" + port
	log.Printf("%s listening on %s", nodeName, addr)
	log.Fatal(http.ListenAndServe(addr, app))
}
