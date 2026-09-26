package live

import (
	"slices"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
)

// snippet:start track
type Board struct {
	mu      sync.Mutex
	notes   []string
	changed *topic.Topic[[]string]
}

func NewBoard() *Board { return &Board{changed: topic.New[[]string]()} }

func (b *Board) Add(note string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notes = append(b.notes, note)
	b.changed.Publish(slices.Clone(b.notes))
}

func (b *Board) Notes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.notes)
}

type Wall struct {
	Board *Board
	Notes via.List[string]
}

func (w *Wall) OnInit(ctx *via.Ctx) error {
	w.Notes.Track(ctx, w.Board.changed, w.Board.Notes)
	return nil
}

func (w *Wall) View() h.H        { return h.Ul(w.Notes.Each(w.row)) }
func (w *Wall) row(n string) h.H { return h.Li(h.Str(n)) }

// snippet:end
