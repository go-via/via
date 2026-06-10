package ui

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"strings"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/viashowcase/internal/assets"
	"github.com/go-via/viashowcase/internal/auth"
	"github.com/go-via/viashowcase/internal/core"
	"github.com/go-via/viashowcase/internal/store"
)

// Home is the landing page: pitch + (if a host is logged in) their rooms and a
// create-room form.
type Home struct {
	Title   via.SignalStr `via:"title"`
	Kind    via.SignalStr `via:"kind,init=poll"`
	Choices via.SignalStr `via:"choices"`
}

// Create writes a new room owned by the current host and redirects to its
// big-screen view.
func (c *Home) Create(ctx *via.Ctx) error {
	u, ok := auth.Current(ctx)
	if !ok {
		ctx.Redirect("/login")
		return nil
	}
	title := strings.TrimSpace(c.Title.Read(ctx))
	if title == "" {
		title = "Untitled"
	}
	kind := c.Kind.Read(ctx)
	var choices []string
	if kind == "poll" {
		// A poll with no valid choices renders zero vote buttons and rejects
		// every vote — a dead room. Refuse to create it.
		if choices = core.PollChoices(c.Choices.Read(ctx)); len(choices) == 0 {
			ctx.Toast("A poll needs at least one choice.")
			return nil
		}
	}
	code := newRoomCode()
	if err := Deps.DB.CreateRoom(context.Background(), store.Room{
		Code: code, HostID: u.ID, Title: title, Kind: kind, Choices: choices,
	}); err != nil {
		return err
	}
	ctx.Redirect("/host/" + code)
	return nil
}

// newRoomCode returns an UNGUESSABLE room code. Seeding core.Code from a wall
// clock made adjacent rooms share a prefix (enumerable share links); a crypto
// seed makes codes ~63 bits of entropy so they can't be guessed.
func newRoomCode() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return core.Code(time.Now().UnixNano()) // fallback: never block room creation
	}
	return core.Code(int64(binary.BigEndian.Uint64(b[:])))
}

func (c *Home) View(ctx *via.CtxR) h.H {
	u, in := auth.Current(ctx)
	hero := h.Section(h.Class("hero"),
		h.Img(h.Src(assets.Punch), h.Class("hero-mark"), h.Attr("alt", ""), h.Attr("aria-hidden", "true")),
		h.H1(h.Text("Ask your room "), h.Span(h.Class("accent"), h.Text("anything."))),
		h.P(h.Class("lead"), h.Text("Signal turns any audience into a live feed — polls, word clouds, and Q&A that update on the big screen the instant people vote. No app, no JavaScript: just a link.")),
		h.When(!in, func() h.H {
			return h.Div(h.Class("hero-cta"),
				h.A(h.Href("/signup"), h.Role("button"), h.Text("Host a room")),
				h.A(h.Href("/login"), h.Role("button"), h.Class("secondary"), h.Text("Log in")),
			)
		}),
		h.Div(h.Class("feature-row"),
			feature("Live polls", "Fixed-choice voting with bars that grow as the room decides."),
			feature("Word clouds", "Free-text words that swell with every mention."),
			feature("Q&A + pins", "Crowd-ranked questions and a shared map of who's in."),
		),
	)
	if !in {
		return Shell(ctx, "", hero)
	}

	rms, _ := Deps.DB.RoomsByHost(context.Background(), u.ID)
	body := []h.H{
		hero,
		h.H3(h.Text("Your rooms")),
		h.IfElse(len(rms) == 0,
			h.Div(h.Class("empty"),
				h.Strong(h.Text("No rooms yet")),
				h.Text("Create your first room below and share the link."),
			),
			h.Div(h.Class("card-grid"), h.Each(rms, roomCard)),
		),
		c.createForm(ctx),
	}
	return Shell(ctx, "", body...)
}

func feature(title, body string) h.H {
	return h.Div(h.Class("feature"),
		h.H4(h.Text(title)),
		h.P(h.Text(body)),
	)
}

// roomCard renders one owned room with a big-screen link and a copyable share
// link (click selects + copies via the clipboard API, no hand-written JS file).
func roomCard(r store.Room) h.H {
	share := "/r/" + r.Code
	return h.Article(h.Class("room-card"),
		h.Header(
			h.Span(h.Class("room-title"), h.Text(r.Title)),
			h.Span(h.Class("kind-badge"), h.Text(kindLabel(r.Kind))),
		),
		h.Footer(
			h.Div(h.Class("share-field"),
				h.Input(h.Type("text"), h.Attr("readonly"), h.Value(share),
					h.Attr("aria-label", "Audience share link — click to copy"), h.Class("share"),
					h.DataOnClick("evt.target.select(); navigator.clipboard&&navigator.clipboard.writeText(evt.target.value)"),
				),
				h.Button(h.Class("outline", "copy-btn"), h.Attr("aria-label", "Copy share link"),
					h.Text("Copy"),
					h.DataOnClick("var i=evt.target.closest('.share-field').querySelector('input');i.select();navigator.clipboard&&navigator.clipboard.writeText(i.value)"),
				),
			),
			h.A(h.Href("/host/"+r.Code), h.Role("button"), h.Text("Open big screen →")),
		),
	)
}

func kindLabel(k string) string {
	switch k {
	case "poll":
		return "Poll"
	case "cloud":
		return "Word cloud"
	case "qa":
		return "Q&A"
	default:
		return k
	}
}

func (c *Home) createForm(ctx *via.CtxR) h.H {
	kind := c.Kind.Read(ctx)
	return h.Article(h.Class("form-card"),
		h.H3(h.Text("Create a room")),
		h.Form(
			h.Label(h.Text("Title"),
				h.Input(h.Type("text"), c.Title.Bind(), h.Placeholder("e.g. Lunch vote"),
					h.Attr("autocomplete", "off"))),
			h.Label(h.Text("Kind"),
				h.Select(c.Kind.Bind(),
					h.Option(h.Value("poll"), h.Text("Poll")),
					h.Option(h.Value("cloud"), h.Text("Word cloud")),
					h.Option(h.Value("qa"), h.Text("Q&A")),
				),
			),
			// h.Switch on the Kind signal value gives a kind-specific hint.
			h.Switch(kind,
				h.Case("poll", h.Small(h.Class("hint"), h.Text("Voters pick one of your fixed choices."))),
				h.Case("cloud", h.Small(h.Class("hint"), h.Text("Voters type free words; popular ones grow."))),
				h.Case("qa", h.Small(h.Class("hint"), h.Text("Voters ask questions and upvote others."))),
			),
			c.choicesField(),
			h.Button(h.Type("button"), h.Text("Create room"), on.Click(c.Create)),
		),
	)
}

// choicesField is for polls only; data-show keeps it reactive to the bound Kind
// signal so flipping the selector toggles it live without a round-trip.
func (c *Home) choicesField() h.H {
	return h.Label(
		h.DataShow("$%s === 'poll'", c.Kind.Key()),
		h.Text("Choices (comma-separated)"),
		h.Input(h.Type("text"), c.Choices.Bind(), h.Placeholder("Pizza, Sushi, Tacos")),
	)
}
