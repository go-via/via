package demos

import (
	"strings"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
)

// ChatMessage is one chat line.
type ChatMessage struct{ Who, Text string }

// ChatRoom is the tutorial's Room. Nothing here stores a message: the topic
// hands each one to the tabs open now, so a tab that arrives later, or
// reloads, starts empty and there is no history to reset.
type ChatRoom struct {
	bus      *topic.Topic[ChatMessage]
	presence *topic.Topic[int64]
	mu       sync.Mutex
	online   int64
}

func (r *ChatRoom) join() { r.add(1) }
func (r *ChatRoom) part() { r.add(-1) }

// add holds mu across the change and the publish, so counts leave in the
// order they moved and the last one a tab sees is current.
func (r *ChatRoom) add(n int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.online += n
	r.presence.Publish(r.online)
}

func (r *ChatRoom) count() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.online
}

var tutorialRoom = &ChatRoom{bus: topic.New[ChatMessage](), presence: topic.New[int64]()}

// TutorialChat is the tutorial's Chat plus what a public page needs: a
// per-IP send budget, length caps, and a log trimmed to the newest rows.
type TutorialChat struct {
	room *ChatRoom
	// Limiter, keepRows and trim: see shared_contract.go.
	Lim Limiter

	Who    via.Signal[string]
	Draft  via.Signal[string]
	Log    via.List[ChatMessage]
	Online via.State[int64]
	Notice via.State[string]
}

// NewTutorialChat takes the limiter the page owns; a nil one is a wiring
// mistake, so it fails here rather than on the first send.
func NewTutorialChat(lim Limiter) TutorialChat {
	if lim == nil {
		panic("demos: NewTutorialChat: TutorialChat.Lim must not be nil")
	}
	return TutorialChat{room: tutorialRoom, Lim: lim}
}

func (c *TutorialChat) OnInit(ctx *via.Ctx) error {
	ctx.Listen(c.room.bus, c.onMessage)
	c.Online.Track(ctx, c.room.presence, c.room.count)
	ctx.OnConnect(c.room.join)
	ctx.OnDispose(c.room.part)
	return nil
}

func (c *TutorialChat) onMessage(ctx *via.Ctx, m ChatMessage) {
	c.Log.Append(m)
	trim(&c.Log, keepRows)
}

func (c *TutorialChat) Send(ctx *via.Ctx) {
	text := strings.TrimSpace(c.Draft.Get())
	if text == "" {
		c.Draft.Set("")
		return
	}
	if !c.Lim.Allow(ctx) {
		c.Notice.Set("slow down — 20 messages a minute")
		return
	}
	c.Notice.Set("")
	who := firstRunes(strings.TrimSpace(c.Who.Get()), 24)
	if who == "" {
		who = "anon"
	}
	c.room.bus.Publish(ChatMessage{Who: who, Text: firstRunes(text, 200)})
	c.Draft.Set("")
}

// firstRunes is the server's cap; maxlength on the input is only the polite one.
func firstRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (c *TutorialChat) row(m ChatMessage) h.H {
	return h.Li(h.B(h.Str(m.Who+": ")), h.Str(m.Text))
}

func (c *TutorialChat) View() h.H {
	return h.Div(
		h.P(h.Class("note"), c.Online.Display(), h.Str(" online")),
		h.Ul(h.Class("loglist"), h.TabIndex(0), h.Role("log"), h.Aria("label", "Chat messages"), c.Log.Each(c.row)),
		h.Form(on.Submit(c.Send),
			h.Label(h.Str("you "), h.Input(c.Who.Bind(), h.Placeholder("anon"), h.MaxLength(24), h.AutoComplete("off"))),
			h.Div(h.Class("row"),
				h.Input(c.Draft.Bind(), h.Placeholder("message"), h.MaxLength(200), h.AutoComplete("off"),
					h.Aria("label", "Message")),
				h.Button(h.Type("submit"), h.Str("send")),
			),
		),
		h.Small(h.Class("notice"), c.Notice.Display()),
	)
}
