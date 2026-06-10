package core

import "sort"

// Vote covers both poll and word-cloud; choices are free strings. Single marks
// a poll vote (one per voter, latest choice wins); word-cloud votes leave it
// false so every word a participant submits keeps counting.
type Vote struct {
	Room, Choice, By string
	Single           bool
}

// Tally maps a choice to its count.
type Tally map[string]int

// Tallies is the votes projection. Counts feeds the render path; Voted records,
// for Single (poll) votes only, each voter's current choice so a re-vote moves
// their single tally instead of adding a new one. Both fields are exported so
// the projection survives the JSON snapshot — an unexported Voted would be
// dropped and dedup would silently break on cold start / cross-pod reseed.
type Tallies struct {
	Counts map[string]Tally
	Voted  map[string]map[string]string
}

// Pair is a ranked (choice, count) entry.
type Pair struct {
	Choice string
	Count  int
}

// Fold returns a copy of acc with ev applied. A word-cloud vote (!Single) just
// increments its choice. A poll vote (Single) keeps one vote per voter: the
// first counts, re-voting the same choice is a no-op, and switching moves the
// vote (the abandoned choice is removed at zero).
func (Vote) Fold(acc Tallies, ev Vote) Tallies {
	out := Tallies{
		Counts: make(map[string]Tally, len(acc.Counts)+1),
		Voted:  make(map[string]map[string]string, len(acc.Voted)+1),
	}
	for room, t := range acc.Counts {
		nt := make(Tally, len(t))
		for c, n := range t {
			nt[c] = n
		}
		out.Counts[room] = nt
	}
	for room, v := range acc.Voted {
		nv := make(map[string]string, len(v))
		for by, choice := range v {
			nv[by] = choice
		}
		out.Voted[room] = nv
	}
	if out.Counts[ev.Room] == nil {
		out.Counts[ev.Room] = Tally{}
	}
	if !ev.Single {
		out.Counts[ev.Room][ev.Choice]++
		return out
	}
	if out.Voted[ev.Room] == nil {
		out.Voted[ev.Room] = map[string]string{}
	}
	if prev, had := out.Voted[ev.Room][ev.By]; had {
		if prev == ev.Choice {
			return out
		}
		if out.Counts[ev.Room][prev]--; out.Counts[ev.Room][prev] <= 0 {
			delete(out.Counts[ev.Room], prev)
		}
	}
	out.Counts[ev.Room][ev.Choice]++
	out.Voted[ev.Room][ev.By] = ev.Choice
	return out
}

// For is a nil-safe read of one room's tally.
func (t Tallies) For(code string) Tally { return t.Counts[code] }

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
