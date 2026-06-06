package core

import "sort"

// QAEvent drives the Q&A board. Kind is "ask" | "up".
type QAEvent struct{ Room, Kind, ID, Text, By string }

type Question struct {
	ID, Text, By string
	Votes        int
}

// Boards maps room code -> questions.
type Boards map[string][]Question

// Fold returns a copy of acc with ev applied. "ask" appends a question;
// "up" increments Votes for the matching ID. Unknown Kind is a no-op.
func (QAEvent) Fold(acc Boards, ev QAEvent) Boards {
	out := make(Boards, len(acc)+1)
	for room, qs := range acc {
		out[room] = append([]Question(nil), qs...)
	}
	switch ev.Kind {
	case "ask":
		out[ev.Room] = append(out[ev.Room], Question{ID: ev.ID, Text: ev.Text, By: ev.By})
	case "up":
		for i := range out[ev.Room] {
			if out[ev.Room][i].ID == ev.ID {
				out[ev.Room][i].Votes++
				break
			}
		}
	}
	return out
}

// For returns a room's questions sorted: Votes desc, then ID asc.
func (b Boards) For(code string) []Question {
	qs := append([]Question(nil), b[code]...)
	sort.Slice(qs, func(i, j int) bool {
		if qs[i].Votes != qs[j].Votes {
			return qs[i].Votes > qs[j].Votes
		}
		return qs[i].ID < qs[j].ID
	})
	return qs
}
