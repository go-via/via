package core

import (
	"slices"
	"sort"
)

// QAEvent drives the Q&A board. Kind is "ask" | "up".
type QAEvent struct{ Room, Kind, ID, Text, By string }

type Question struct {
	ID, Text, By string
	Votes        int
	// Voters is the set of participant identities that have upvoted, so each
	// counts at most once. It is exported because the Boards projection is
	// JSON-marshalled for snapshots — an unexported field would be dropped and
	// dedup would silently break on cold start / cross-pod reseed. Votes is
	// kept equal to len(Voters).
	Voters []string
}

// Boards maps room code -> questions.
type Boards map[string][]Question

// Fold returns a copy of acc with ev applied. "ask" appends a question; "up"
// records ev.By as an upvoter of the matching ID, at most once per participant
// (a repeat upvote is a no-op). Unknown Kind, or "up" for an unknown ID, is a
// no-op.
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
			q := out[ev.Room][i]
			if q.ID != ev.ID || slices.Contains(q.Voters, ev.By) {
				continue
			}
			// Copy before append so a shared backing array is never mutated.
			q.Voters = append(append([]string(nil), q.Voters...), ev.By)
			q.Votes = len(q.Voters)
			out[ev.Room][i] = q
			break
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
