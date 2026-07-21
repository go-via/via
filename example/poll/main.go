// Command poll is a CRUD list with per-row actions: add an option, vote for one,
// remove one. The list re-sorts by vote count on every render, so rows reorder
// constantly — yet each button carries its option's id (via.OnClickArg), so a
// vote always lands on the option you clicked, not whatever now sits at that
// position. That's the point of value-carrying actions: identity rides with the
// click, so a list that grows, shrinks, and reorders never misroutes — no stable
// action-slot scheme needed. Zero '&', no identifier strings, no closures at any
// call site.
package main

import (
	"net/http"
	"sort"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Option is one poll choice. Plain app data; ID is its natural key.
type Option struct {
	ID    int
	Label string
	Votes int
}

// Poll is the shared, server-side store — app-land, not framework.
type Poll struct {
	mu   sync.Mutex
	opts []Option
	seq  int
}

func (p *Poll) add(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	p.opts = append(p.opts, Option{ID: p.seq, Label: label})
}

func (p *Poll) vote(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.opts {
		if p.opts[i].ID == id {
			p.opts[i].Votes++
		}
	}
}

func (p *Poll) remove(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.opts[:0]
	for _, o := range p.opts {
		if o.ID != id {
			out = append(out, o)
		}
	}
	p.opts = out
}

// ranked returns the options sorted by votes (desc) — so the rendered order
// changes as people vote, which is exactly what makes per-row identity tricky.
func (p *Poll) ranked() []Option {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := append([]Option(nil), p.opts...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Votes > out[j].Votes })
	return out
}

// PollApp is the page: a stateless composition over the shared Poll, plus a
// client signal for the new-option input.
type PollApp struct {
	poll  *Poll
	Draft via.Signal[string]
}

func (a *PollApp) Add(ctx *via.Ctx) {
	if a.Draft.Get() == "" {
		return
	}
	a.poll.add(a.Draft.Get())
	a.Draft.Set("") // clear the composer
}

// Vote and Remove take the option's id as a typed parameter — via decodes it
// from the action the row carried, so they act on THAT option regardless of
// where it currently sits in the re-sorted list.
func (a *PollApp) Vote(ctx *via.Ctx, id int)   { a.poll.vote(id) }
func (a *PollApp) Remove(ctx *via.Ctx, id int) { a.poll.remove(id) }

func (a *PollApp) row(o Option) h.H {
	return h.Li(
		h.Span(h.Str(o.Label+" — "), h.Str(o.Votes)),
		h.Button(via.OnClickArg(a.Vote, o.ID), h.Str("vote")),     // carries o.ID
		h.Button(via.OnClickArg(a.Remove, o.ID), h.Str("remove")), // carries o.ID
	)
}

func (a *PollApp) View() h.H {
	return h.Div(
		h.H1(h.Str("poll")),
		h.Ul(via.Each(a.poll.ranked(), a.row)), // rows reorder by votes each render
		h.Form(via.OnSubmit(a.Add),
			h.Input(a.Draft.Bind(), h.RawAttr("placeholder", "new option")),
			h.Button(h.Str("add")),
		),
	)
}

func main() {
	poll := &Poll{}
	poll.add("Go")
	poll.add("Rust")
	poll.add("Zig")
	http.Handle("/", via.Register(PollApp{poll: poll}))
	http.ListenAndServe(":8080", nil)
}
