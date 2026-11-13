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
	mu    sync.Mutex
	notes []string
	added *topic.Topic[string] // announces each new note
}

func NewBoard() *Board { return &Board{added: topic.New[string]()} }

func (b *Board) Add(note string) {
	b.mu.Lock()
	b.notes = append(b.notes, note)
	b.mu.Unlock()
	b.added.Publish(note)
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
	w.Notes.Track(ctx, w.Board.added, w.Board.Notes)
	return nil
}

func (w *Wall) View() h.H        { return h.Ul(w.Notes.Each(w.row)) }
func (w *Wall) row(n string) h.H { return h.Li(h.Str(n)) }

// snippet:end
