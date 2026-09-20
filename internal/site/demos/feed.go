package demos

import (
	"strconv"
	"sync/atomic"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

// Limiter is the per-session rate limit a page hands the demos that let a
// visitor publish. It is an interface rather than *demo.Limiter because the
// demo package embeds this package's source, so this one cannot import it back.
type Limiter interface{ Allow(ctx *via.Ctx) bool }

// keepRows bounds what a connection accumulates: these demos are public and
// anonymous, so nothing they collect may grow without bound.
const keepRows = 50

// feedTopic is a plain package-level value. Nothing about it is via's: any
// goroutine may Publish to it, and a unit's ctx.Listen is what turns a publish
// into a frame on that unit's stream.
var (
	feedTopic = topic.New[FeedPost]()
	feedSeq   atomic.Int64
)

// FeedPost is one broadcast line. Topics carry whatever type you give them.
type FeedPost struct {
	At   time.Time
	Text string
}

// Feed shows every post made while this tab is open — it mirrors no store, so
// a tab that arrives late starts empty. A value every tab must agree on wants
// State.Track instead.
type Feed struct {
	Lim    Limiter
	Posts  via.List[FeedPost]
	Notice via.State[string]
}

func (f *Feed) OnInit(ctx *via.Ctx) error {
	ctx.Listen(feedTopic, f.recv)
	return nil
}

// recv runs on this unit's own goroutine, serialized with its ticks, so it may
// touch unit state without a lock.
func (f *Feed) recv(ctx *via.Ctx, p FeedPost) {
	f.Posts.Append(p)
	if n := len(f.Posts.Get()); n > keepRows {
		f.Posts.Set(f.Posts.Get()[n-keepRows:])
	}
}

func (f *Feed) Post(ctx *via.Ctx) {
	if !f.Lim.Allow(ctx) {
		f.Notice.Set("slow down — 20 posts a minute")
		return
	}
	f.Notice.Set("")
	feedTopic.Publish(FeedPost{At: time.Now(), Text: "event #" + strconv.FormatInt(feedSeq.Add(1), 10)})
}

func (f *Feed) View() h.H {
	return h.Div(
		h.Div(h.Class("row"),
			h.Button(via.On("click", f.Post), h.Str("post an event")),
			h.Span(h.Class("note"), f.Notice.Display()),
		),
		h.Ul(h.Class("loglist"), f.Posts.Each(func(p FeedPost) h.H {
			return h.Li(h.Str(p.At.Format("15:04:05") + "  " + p.Text))
		})),
	)
}
