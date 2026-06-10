---
title: "Tutorial: live chatroom"
layout: default
parent: Learn
nav_order: 2
---

# Tutorial: a live chatroom in ~60 lines
{: .no_toc }

The [counter](getting-started) showed the shape of a composition. Now build
the thing that usually means WebSockets, a message bus, and a pile of client
JS — a **live multi-user chatroom**. In Via it falls out of one app-scoped
field: a line typed in any browser appears instantly in every other connected
browser. No WebSocket, no `Broadcast` call, no hand-written JavaScript.

We build it here with the simplest shape that works — one app-scoped slice.
The shipped
[`internal/examples/chat`](https://github.com/go-via/via/tree/main/internal/examples/chat)
(about 60 lines) reaches the same result with the event-sourced
`StateAppEvents` shape, a better fit for a high-churn feed — see
[Distributed state](distributed-state). Same idea either way; start with the
slice.

1. TOC
{:toc}

## 1. The room is one shared field

```go
type Message struct{ From, Body string }

type Room struct {
    // App-scoped: ONE log shared by every session and every tab.
    Log via.StateAppSlice[Message]

    // Tab-local client signals, two-way bound to inputs. Name rides along
    // on each message this tab sends.
    Name  via.SignalStr `via:"name,init=Anon"`
    Draft via.SignalStr `via:"draft"`
}
```

`Log` is `StateAppSlice` — **global** server state. That single choice is the
whole trick: when any action appends to an app-scoped value, Via re-renders
*every tab that read it*, across every session. `Name` and `Draft` are client
signals bound to text inputs, so typing them costs no round-trip.

## 2. The view

```go
func (r *Room) View(ctx *via.CtxR) h.H {
    return h.Main(h.Class("container"),
        h.H1(h.Text("Via Chat")),
        h.Article(h.Style("max-height:60vh;overflow-y:auto"),
            h.Each(r.Log.Read(ctx), func(m Message) h.H {
                return h.P(h.Strong(h.Text(m.From+": ")), h.Text(m.Body))
            }),
        ),
        h.Form(
            h.Input(h.Type("text"), r.Name.Bind(), h.Placeholder("name")),
            h.Input(h.Type("text"), r.Draft.Bind(),
                h.Placeholder("message…"), on.Key("Enter", r.Send)),
            h.Button(h.Type("button"), h.Text("Send"), on.Click(r.Send)),
        ),
    )
}
```

Reading `r.Log.Read(ctx)` here is what **subscribes** this tab to the log —
that's why a `Send` from anyone re-renders it. `on.Key("Enter", r.Send)` sends
on Enter; the button sends on click.

## 3. The send action

```go
func (r *Room) Send(ctx *via.Ctx) {
    body := strings.TrimSpace(r.Draft.Read(ctx))
    if body == "" {
        return
    }
    name := strings.TrimSpace(r.Name.Read(ctx))
    if name == "" {
        name = "Anon"
    }
    r.Log.Op(ctx).Append(Message{From: name, Body: body})
    _ = r.Log.Update(ctx, func(log []Message) ([]Message, error) {
        if len(log) > 50 { // keep a recent window so the room can't grow forever
            log = log[len(log)-50:]
        }
        return log, nil
    })
    r.Draft.Write(ctx, "") // clear the input
}
```

`Op(ctx).Append` appends under a per-key mutex — concurrent senders can't lose
messages — then Via diffs the View and ships the new `<p>` to every subscribed
tab over SSE. The trailing `Update` trims the log to a recent window. Writing
`Draft` back to `""` clears the sender's input.

## 4. Run it

```go
func main() {
    app := via.New(via.WithPlugins(picocss.Plugin()))
    via.Mount[Room](app, "/")
    _ = http.ListenAndServe(":3000", app)
}
```

```bash
go run ./internal/examples/chat
# open http://localhost:3000 in TWO browser windows
```

Type in one window; the message appears in both — live. That is the entire
chatroom.

## 5. Why it's live — and what it cost

The realtime sync is not in your code; it's the framework. `StateApp*` is
server-owned **global** state, and an `Update` to it fans a re-render out to
every connected tab ([Reactive state](reactive-state)). You wrote a struct, a
view, and one action — no WebSocket, no subscription bookkeeping, no client
JS. (`StateApp` is single-process and has no tenant isolation, so this is one
global room — see the [non-goals](why-via).)

That cross-session fan-out is exactly what the example's test
`TestChat_messageFansOutAcrossSessions` asserts end-to-end: two separate
sessions, a `Send` from one, the message live on the other's stream. See
[Testing](testing).

## 6. Where to go next

- **Per-room channels.** Swap the one `StateAppSlice` for a
  `StateAppMap[string, []Message]` keyed by room name.
- **Persistence.** App state is in-memory; back the log with a DB and
  rehydrate in `OnInit`
  ([Production & ops](production#restart-and-tab-survivability)).
- **Presence / typing indicators.** Another app-scoped value, same pattern.
- **Style it** further with [picocss](plugins), or **test it** with
  [`vt`](testing).
