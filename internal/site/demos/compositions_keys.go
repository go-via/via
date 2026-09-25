package demos

import (
	"sync"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// dismissals is which visitors closed a banner, keyed by session id. It stands
// in for any store that changes while a page is open.
type dismissals struct {
	mu sync.Mutex
	by map[string]bool
}

// maxDismissals bounds the map: a visitor forgotten by it sees the banner again.
const maxDismissals = 10000

func (d *dismissals) get(sid string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.by[sid]
}

func (d *dismissals) set(sid string, v bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.by == nil || len(d.by) >= maxDismissals {
		d.by = map[string]bool{}
	}
	if v {
		d.by[sid] = true
	} else {
		delete(d.by, sid)
	}
}

// Banner hides itself once dismissed, so it is safe to embed unconditionally.
type Banner struct {
	Seen   *dismissals
	hidden bool
}

func (b *Banner) OnInit(ctx *via.Ctx) error {
	b.hidden = b.Seen.get(ctx.Session().ID())
	return nil
}

func (b *Banner) Dismiss(ctx *via.Ctx) {
	b.Seen.set(ctx.Session().Ensure(), true)
	b.hidden = true
}

func (b *Banner) View() h.H { return via.When(!b.hidden, b.body) }

func (b *Banner) body() h.H {
	return h.P(h.Class("panel", "row"),
		h.Span(h.Str("A banner above the clock.")),
		h.Button(on.Click(b.Dismiss), h.Str("dismiss")),
	)
}

// Stamp is plain: each click is a fresh request, addressed by Stamp's key.
type Stamp struct{ At via.Signal[string] }

func (s *Stamp) Read(ctx *via.Ctx) { s.At.Set(time.Now().Format("15:04:05")) }

func (s *Stamp) View() h.H {
	return h.Div(h.Class("row"),
		h.Button(on.Click(s.Read), h.Str("read the clock")),
		h.Output(s.At.Display()),
	)
}

// snippet:start broken
// ShiftBroken wraps the Child in a When. Dismissing drops Banner from the
// server's tree, so Stamp's key moves from 1 to 0, but the page still holds
// Stamp's button with key 1 in its URL.
type ShiftBroken struct {
	Banner Banner
	Stamp  Stamp
	hidden bool
}

func (p *ShiftBroken) OnInit(ctx *via.Ctx) error {
	p.hidden = p.Banner.Seen.get(ctx.Session().ID())
	return nil
}

func (p *ShiftBroken) View() h.H {
	return h.Div(
		h.P(h.Class("note"), h.Str("When around the Child")),
		via.When(!p.hidden, p.banner),
		via.Child(p.Stamp),
		h.Button(on.Click(p.Restore), h.Str("bring the banner back")),
	)
}

func (p *ShiftBroken) banner() h.H { return via.Child(p.Banner) }

// snippet:end

func (p *ShiftBroken) Restore(ctx *via.Ctx) {
	p.Banner.Seen.set(ctx.Session().ID(), false)
	p.hidden = false
}

// snippet:start fixed
// ShiftFixed always calls Child; Banner decides inside its own View whether
// to draw anything. Every key stays put.
type ShiftFixed struct {
	Banner Banner
	Stamp  Stamp
}

func (p *ShiftFixed) View() h.H {
	return h.Div(
		h.P(h.Class("note"), h.Str("When inside the child")),
		via.Child(p.Banner),
		via.Child(p.Stamp),
		h.Button(on.Click(p.Restore), h.Str("bring the banner back")),
	)
}

// snippet:end

func (p *ShiftFixed) Restore(ctx *via.Ctx) { p.Banner.Seen.set(ctx.Session().ID(), false) }

// ShiftPair holds both variants under one parent, so one Wire pane follows
// the two halves' traffic. Each half gets its own store, so dismissing one
// banner leaves the other alone.
type ShiftPair struct {
	Broken ShiftBroken
	Fixed  ShiftFixed
}

func NewShiftPair() ShiftPair {
	return ShiftPair{
		Broken: ShiftBroken{Banner: Banner{Seen: new(dismissals)}},
		Fixed:  ShiftFixed{Banner: Banner{Seen: new(dismissals)}},
	}
}

func (p *ShiftPair) View() h.H {
	return h.Div(h.Class("pair"), via.Child(p.Broken), via.Child(p.Fixed))
}
