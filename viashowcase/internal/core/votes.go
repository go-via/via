package core

import "sort"

// Vote covers both poll and word-cloud; choices are free strings.
type Vote struct{ Room, Choice, By string }

type (
	Tally   map[string]int   // choice -> count
	Tallies map[string]Tally // room code -> tally
)

// Pair is a ranked (choice, count) entry.
type Pair struct {
	Choice string
	Count  int
}

// Fold returns a copy of acc with ev applied (acc[ev.Room][ev.Choice]++).
func (Vote) Fold(acc Tallies, ev Vote) Tallies {
	out := make(Tallies, len(acc)+1)
	for room, t := range acc {
		nt := make(Tally, len(t))
		for c, n := range t {
			nt[c] = n
		}
		out[room] = nt
	}
	if out[ev.Room] == nil {
		out[ev.Room] = Tally{}
	}
	out[ev.Room][ev.Choice]++
	return out
}

// For is a nil-safe read of one room's tally.
func (t Tallies) For(code string) Tally { return t[code] }

func (t Tally) Total() int {
	n := 0
	for _, c := range t {
		n += c
	}
	return n
}

// Ranked sorts desc by count, then Choice asc (deterministic).
func (t Tally) Ranked() []Pair {
	out := make([]Pair, 0, len(t))
	for c, n := range t {
		out = append(out, Pair{c, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Choice < out[j].Choice
	})
	return out
}
