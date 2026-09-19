// Command poll is a CRUD list with per-row actions. The list re-sorts by vote
// count on every render, and via.OnArg carries each row's id with the click, so a
// vote lands on the option you clicked rather than whatever now sits in that slot.
package main

import (
	"cmp"
	"log"
	"net/http"
	"os"
	"slices"
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
	p.opts = slices.DeleteFunc(p.opts, func(o Option) bool { return o.ID == id })
}

// ranked returns the options sorted by votes (desc) — so the rendered order
// changes as people vote, which is exactly what makes per-row identity tricky.
func (p *Poll) ranked() []Option {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := append([]Option(nil), p.opts...)
	slices.SortStableFunc(out, func(a, b Option) int { return cmp.Compare(b.Votes, a.Votes) })
	return out
}

// PollApp is the page: a plain composition over the shared Poll, plus a
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
// from the action the row carried, so they act on that option regardless of
// where it currently sits in the re-sorted list.
func (a *PollApp) Vote(ctx *via.Ctx, id int)   { a.poll.vote(id) }
func (a *PollApp) Remove(ctx *via.Ctx, id int) { a.poll.remove(id) }

func (a *PollApp) row(o Option) h.H {
	return h.Li(
		h.Span(h.Str(o.Label+" — "), h.Str(o.Votes)),
		h.Button(via.OnArg("click", a.Vote, o.ID), h.Str("vote")),
		h.Button(via.OnArg("click", a.Remove, o.ID), h.Str("remove")),
	)
}

func (a *PollApp) View() h.H {
	return h.Div(
		h.H1(h.Str("poll")),
		h.Ul(via.Each(a.poll.ranked(), a.row)), // rows reorder by votes each render
		h.Form(via.On("submit", a.Add),
			h.Input(a.Draft.Bind(), h.Placeholder("new option")),
			// Disabled client-side while the draft is empty; Add still checks, the
			// client is not trusted to.
			h.Button(h.DataAttr("disabled", a.Draft.Ref().Eq("")), h.Str("add")),
		),
	)
}

func main() {
	poll := &Poll{}
	poll.add("Go")
	poll.add("Rust")
	poll.add("Zig")
	http.Handle("/", via.Handler(PollApp{poll: poll}))
	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
