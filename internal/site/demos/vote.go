package demos

import (
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// VoteRow is one poll row. Option is the row's identity — it rides with the
// click, so a vote lands on the option clicked and not on a slot.
type VoteRow struct {
	Option int
	Label  string
	Count  int
}

var voteOptions = []string{"Go", "Rust", "Zig", "Elixir"}

// votes is shared by every visitor, which is why Vote is rate limited.
var votes = struct {
	mu     sync.Mutex
	counts []int
}{counts: make([]int, len(voteOptions))}

// The tallies have no topic: this demo is deliberately not live, so an open
// tab redraws on its next vote or reload rather than at the moment of reset.
func resetVotes() {
	votes.mu.Lock()
	clear(votes.counts)
	votes.mu.Unlock()
}

func voteRows() []VoteRow {
	votes.mu.Lock()
	defer votes.mu.Unlock()
	rows := make([]VoteRow, len(voteOptions))
	for i, label := range voteOptions {
		rows[i] = VoteRow{Option: i, Label: label, Count: votes.counts[i]}
	}
	return rows
}

// Vote is a per-row action: each button carries its own option index, and the
// handler receives it as a typed parameter.
type Vote struct {
	// Limiter: see shared_contract.go.
	Lim    Limiter
	Rows   via.List[VoteRow]
	Notice via.State[string]
}

// NewVote takes the limiter the page owns; a nil one is a wiring mistake, so
// it fails here rather than on the first render.
func NewVote(lim Limiter) Vote {
	if lim == nil {
		panic("demos: NewVote: Vote.Lim must not be nil")
	}
	return Vote{Lim: lim}
}

func (v *Vote) OnInit(ctx *via.Ctx) error {
	v.Rows.Set(voteRows())
	return nil
}

func (v *Vote) Cast(ctx *via.Ctx, option int) {
	if !v.Lim.Allow(ctx) {
		v.Notice.Set("Slow down — the tallies are shared, so this demo takes 30 votes a minute.")
		return
	}
	votes.mu.Lock()
	// Indexing on a client value is safe: only an arg this connection's own
	// render bound is dispatchable, so option is one of the rendered rows.
	votes.counts[option]++
	votes.mu.Unlock()

	v.Notice.Set("")
	v.Rows.Set(voteRows())
}

func (v *Vote) row(r VoteRow) h.H {
	return h.Li(h.Class("row"),
		h.Button(via.OnArg("click", v.Cast, r.Option), h.Str("vote")),
		h.Span(h.Str(r.Label)),
		h.Output(h.Str(r.Count)),
	)
}

func (v *Vote) View() h.H {
	return h.Div(
		h.Ul(h.Class("tally"), v.Rows.Each(v.row)),
		h.P(h.Class("notice"), v.Notice.Display()),
	)
}
