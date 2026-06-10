package ui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/viashowcase/internal/core"
	"github.com/go-via/viashowcase/internal/store"
)

// Join is the phone-facing audience view: vote, ask, or drop a pin. The room
// Kind drives which controls render. Presence is bumped on connect so the
// audience is counted alongside the host.
type Join struct {
	Votes   via.StateAppEvents[core.Vote, core.Tallies]   `via:"votes"`
	QA      via.StateAppEvents[core.QAEvent, core.Boards] `via:"qa"`
	Pins    via.StateAppEvents[core.Pin, core.PinSets]    `via:"pins"`
	Present via.StateApp[map[string]int]                  `via:"present"`

	Code string `path:"code"`

	room     store.Room
	notFound bool // the code didn't resolve to a room → show a friendly state

	Nick   via.SignalStr `via:"nick"`
	Draft  via.SignalStr `via:"draft"`  // word-cloud entry / new question text
	Upvote via.SignalStr `via:"upvote"` // question id to upvote (kept separate so it never pollutes the ask box)

	// Pin coordinates, two-way bound so a geolocate/number control can set them.
	Lng via.SignalNum[float64] `via:"lng,init=0"`
	Lat via.SignalNum[float64] `via:"lat,init=0"`
}

func (j *Join) OnInit(ctx *via.Ctx) error {
	r, err := Deps.DB.RoomByCode(context.Background(), j.Code)
	if err != nil {
		// A stale/typo'd share link: render a friendly state, not a broken page.
		j.notFound = true
		return nil
	}
	j.room = r
	j.Nick.Write(ctx, "guest-"+strconv.FormatInt(time.Now().UnixNano()%10000, 10))
	return nil
}

func (j *Join) OnConnect(ctx *via.Ctx) error { return j.bump(ctx, +1) }
func (j *Join) OnDispose(ctx *via.Ctx)       { _ = j.bump(ctx, -1) }

func (j *Join) bump(ctx *via.Ctx, d int) error {
	return j.Present.Update(ctx, func(m map[string]int) (map[string]int, error) {
		return core.BumpPresence(m, j.Code, d), nil
	})
}

func (j *Join) nick(ctx *via.Ctx) string {
	n := core.NormalizeText(j.Nick.Read(ctx), 40)
	if n == "" {
		return "guest"
	}
	return n
}

// Vote records a choice for poll rooms (the choice label rides on the signal).
// The vote buttons are bypassable — the choice arrives on a client-set signal —
// so poll rooms reject anything not on the fixed ballot; word-cloud rooms accept
// free text.
func (j *Join) Vote(ctx *via.Ctx) error {
	c := strings.TrimSpace(j.Draft.Read(ctx))
	if c == "" || (j.room.Kind == "poll" && !slices.Contains(j.room.Choices, c)) {
		return nil
	}
	if j.room.Kind == "cloud" {
		c = core.NormalizeText(c, 40) // cap free-text words; poll choices stay exact for ballot matching
	}
	// Poll votes are one-per-voter (latest choice wins); word-cloud words all count.
	_, err := j.Votes.Append(ctx, core.Vote{
		Room: j.Code, Choice: c, By: j.nick(ctx), Single: j.room.Kind == "poll",
	})
	j.Draft.Op(ctx).Clear()
	if err == nil {
		ctx.Notify("Vote counted ✓") // phone feedback — the tap registered
	}
	return err
}

// Ask appends a new question to the board.
func (j *Join) Ask(ctx *via.Ctx) error {
	t := core.NormalizeText(j.Draft.Read(ctx), 280)
	if t == "" {
		return nil
	}
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	_, err := j.QA.Append(ctx, core.QAEvent{Room: j.Code, Kind: "ask", ID: id, Text: t, By: j.nick(ctx)})
	j.Draft.Op(ctx).Clear()
	if err == nil {
		ctx.Notify("Question posted ✓")
	}
	return err
}

// Up upvotes a question. The id rides on its own Upvote signal (set client-side
// just before the post via on.SetSignal), so it never overwrites the ask box.
func (j *Join) Up(ctx *via.Ctx) error {
	id := strings.TrimSpace(j.Upvote.Read(ctx))
	if id == "" {
		return nil
	}
	_, err := j.QA.Append(ctx, core.QAEvent{Room: j.Code, Kind: "up", ID: id, By: j.nick(ctx)})
	return err
}

// DropPin appends the participant's current coordinates to the room map.
func (j *Join) DropPin(ctx *via.Ctx) error {
	_, err := j.Pins.Append(ctx, core.Pin{
		Room: j.Code, Lng: j.Lng.Read(ctx), Lat: j.Lat.Read(ctx), By: j.nick(ctx),
	})
	if err == nil {
		ctx.Notify("Pin added ✓")
	}
	return err
}

func (j *Join) View(ctx *via.CtxR) h.H {
	if j.notFound {
		return Shell(ctx, "",
			h.Article(h.Class("notfound"),
				h.H2(h.Text("Room not found")),
				h.P(h.Class("muted"), h.Text("This link is invalid or the room has ended.")),
				h.A(h.Href("/"), h.Role("button"), h.Text("Back to Signal")),
			),
		)
	}
	body := h.Switch(j.room.Kind,
		h.Case("poll", j.pollView()),
		h.Case("cloud", j.cloudView()),
		h.Case("qa", j.qaView(ctx)),
	)
	return Shell(ctx, j.room.Title,
		h.Div(h.Class("join-wrap"),
			h.P(h.Class("you-are"),
				h.Text("You are"),
				h.Span(h.Class("nick-chip"), j.Nick.Text()),
			),
			h.FieldSet(h.Class("nick-field"),
				h.Input(h.Type("text"), j.Nick.Bind(), h.Placeholder("Pick a nickname"),
					h.Aria("label", "Your nickname"), h.AutoComplete("nickname")),
			),
			body,
			j.pinView(),
		),
	)
}

// pollView renders one big button per choice; clicking votes for it.
func (j *Join) pollView() h.H {
	return h.Div(h.Class("poll-grid"),
		h.Each(j.room.Choices, func(c string) h.H {
			return h.Button(h.Class("contrast", "big-tap"), h.Text(c),
				on.Click(j.Vote, on.SetSignal(&j.Draft.Signal, c)),
			)
		}),
	)
}

// cloudView renders a text input + submit that appends the typed word.
func (j *Join) cloudView() h.H {
	return h.FieldSet(h.Role("group"),
		h.Input(h.Type("text"), j.Draft.Bind(), h.Placeholder("one word…"),
			on.Key("Enter", j.Vote)),
		h.Button(h.Class("big-tap"), h.Text("Send"), on.Click(j.Vote)),
	)
}

// qaView renders an ask box plus an upvotable list of questions.
func (j *Join) qaView(ctx *via.CtxR) h.H {
	qs := j.QA.Read(ctx).For(j.Code)
	return h.Div(
		h.FieldSet(h.Role("group"),
			h.Input(h.Type("text"), j.Draft.Bind(), h.Placeholder("ask a question…"),
				on.Key("Enter", j.Ask, on.Debounce("200ms"))),
			h.Button(h.Class("big-tap"), h.Text("Ask"), on.Click(j.Ask)),
		),
		h.When(len(qs) == 0, func() h.H {
			return h.P(h.Class("muted", "empty-hint"), h.Text("No questions yet — be the first to ask."))
		}),
		h.Each(qs, func(q core.Question) h.H {
			return h.Article(h.Class("qa-row"),
				h.Span(h.Text(q.Text)),
				h.Button(h.Class("outline", "big-tap"), h.Textf("▲ %d", q.Votes),
					on.Click(j.Up, on.SetSignal(&j.Upvote.Signal, q.ID)),
				),
			)
		}),
	)
}

// pinView lets a participant add themselves to the room map. The primary path is
// a one-tap "Use my location" (browser geolocation fills the bound coordinate
// signals — no typing); the number inputs remain as a manual/desktop fallback.
func (j *Join) pinView() h.H {
	return h.Details(h.Class("pin-details"),
		h.Summary(h.Text("📍 Add yourself to the map")),
		h.P(h.Class("muted"), h.Small(h.Text("Optional — shows the host roughly where the room is."))),
		h.Button(h.Class("big-tap", "locate-btn"),
			h.Data("on:click", "navigator.geolocation&&navigator.geolocation.getCurrentPosition(function(p){$lng=p.coords.longitude;$lat=p.coords.latitude})"),
			h.Text("📍 Use my location")),
		h.FieldSet(h.Role("group"),
			h.Input(h.Type("number"), h.Step("any"), j.Lng.Bind(), h.Placeholder("lng"),
				h.Aria("label", "Longitude")),
			h.Input(h.Type("number"), h.Step("any"), j.Lat.Bind(), h.Placeholder("lat"),
				h.Aria("label", "Latitude")),
			h.Button(h.Class("contrast", "big-tap"), h.Text("Drop pin"), on.Click(j.DropPin)),
		),
	)
}
