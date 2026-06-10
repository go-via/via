package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upBy returns the room's questions after applying a single "up" event by voter
// `by` to question `id`, starting from seed.
func upBy(seed Boards, room, id, by string) []Question {
	return QAEvent{}.Fold(seed, QAEvent{Room: room, Kind: "up", ID: id, By: by}).For(room)
}

func find(qs []Question, id string) (Question, bool) {
	for _, q := range qs {
		if q.ID == id {
			return q, true
		}
	}
	return Question{}, false
}

// Asking appends a question with no votes yet.
func TestQuestionStartsWithNoVotes(t *testing.T) {
	t.Parallel()
	got := QAEvent{}.Fold(nil, QAEvent{Room: "r1", Kind: "ask", ID: "q1", Text: "hi", By: "u"}).For("r1")
	q, ok := find(got, "q1")
	require.True(t, ok)
	assert.Equal(t, 0, q.Votes)
	assert.Empty(t, q.Voters)
	assert.Equal(t, "hi", q.Text)
}

// A single participant upvoting counts once.
func TestOneParticipantUpvoteCountsOnce(t *testing.T) {
	t.Parallel()
	got := upBy(Boards{"r1": {{ID: "q1"}}}, "r1", "q1", "alice")
	q, _ := find(got, "q1")
	assert.Equal(t, 1, q.Votes)
	assert.Equal(t, []string{"alice"}, q.Voters)
}

// The same participant upvoting twice must not inflate the count — the whole
// point of dedup is that a tap-spammer can't run up a question's score.
func TestSameParticipantUpvotingTwiceCountsOnce(t *testing.T) {
	t.Parallel()
	once := QAEvent{}.Fold(Boards{"r1": {{ID: "q1"}}}, QAEvent{Room: "r1", Kind: "up", ID: "q1", By: "alice"})
	twice := upBy(once, "r1", "q1", "alice")
	q, _ := find(twice, "q1")
	assert.Equal(t, 1, q.Votes, "a repeat upvote from the same participant is a no-op")
	assert.Equal(t, []string{"alice"}, q.Voters)
}

// Distinct participants each add a vote.
func TestDistinctParticipantsEachAddAVote(t *testing.T) {
	t.Parallel()
	once := QAEvent{}.Fold(Boards{"r1": {{ID: "q1"}}}, QAEvent{Room: "r1", Kind: "up", ID: "q1", By: "alice"})
	got := upBy(once, "r1", "q1", "bob")
	q, _ := find(got, "q1")
	assert.Equal(t, 2, q.Votes)
	assert.Equal(t, []string{"alice", "bob"}, q.Voters)
}

// Votes must always equal the number of distinct voters.
func TestVotesEqualsDistinctVoterCount(t *testing.T) {
	t.Parallel()
	b := Boards{"r1": {{ID: "q1"}}}
	for _, by := range []string{"a", "b", "a", "c", "b"} {
		b = QAEvent{}.Fold(b, QAEvent{Room: "r1", Kind: "up", ID: "q1", By: by})
	}
	q, _ := find(b.For("r1"), "q1")
	assert.Equal(t, 3, q.Votes)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, q.Voters)
}

// An upvote for a question that doesn't exist changes nothing.
func TestUpvoteForUnknownQuestionIsNoOp(t *testing.T) {
	t.Parallel()
	got := upBy(Boards{"r1": {{ID: "q1", Votes: 1, Voters: []string{"x"}}}}, "r1", "nope", "alice")
	q, _ := find(got, "q1")
	assert.Equal(t, 1, q.Votes)
	assert.Equal(t, []string{"x"}, q.Voters)
}

// Asking appends to a room's existing questions.
func TestAskAppendsToExistingQuestions(t *testing.T) {
	t.Parallel()
	got := QAEvent{}.Fold(Boards{"r1": {{ID: "q1", Text: "a"}}},
		QAEvent{Room: "r1", Kind: "ask", ID: "q2", Text: "b"}).For("r1")
	require.Len(t, got, 2)
	_, ok1 := find(got, "q1")
	_, ok2 := find(got, "q2")
	assert.True(t, ok1 && ok2)
}

// Folding must never mutate the accumulator — peers share the prior projection.
func TestQAFoldDoesNotMutateAccumulator(t *testing.T) {
	t.Parallel()
	acc := Boards{"r1": {{ID: "q1", Votes: 1, Voters: []string{"alice"}}}}

	_ = QAEvent{}.Fold(acc, QAEvent{Room: "r1", Kind: "up", ID: "q1", By: "bob"})
	assert.Equal(t, 1, acc["r1"][0].Votes, "original Votes untouched")
	assert.Equal(t, []string{"alice"}, acc["r1"][0].Voters, "original Voters untouched")

	_ = QAEvent{}.Fold(acc, QAEvent{Room: "r1", Kind: "ask", ID: "q2"})
	assert.Len(t, acc["r1"], 1, "original slice must not grow")
}

// A Voters slice with spare capacity must not be mutated in place: appending a
// new voter has to copy, or a shared backing array would corrupt the peer's
// projection (the no-spare-capacity seed above can't catch an aliasing append).
func TestQAFoldDoesNotMutateVotersBackingArray(t *testing.T) {
	t.Parallel()
	voters := make([]string, 1, 8) // spare capacity: an in-place append would not reallocate
	voters[0] = "alice"
	acc := Boards{"r1": {{ID: "q1", Votes: 1, Voters: voters}}}

	_ = QAEvent{}.Fold(acc, QAEvent{Room: "r1", Kind: "up", ID: "q1", By: "bob"})

	assert.Equal(t, []string{"alice"}, acc["r1"][0].Voters,
		"appending a voter must copy, never write into the shared backing array")
}

// Upvotes in one room must never leak into another room's questions.
func TestUpvotesAreIsolatedPerRoom(t *testing.T) {
	t.Parallel()
	acc := Boards{"r1": {{ID: "q1"}}, "r2": {{ID: "q1"}}}
	out := QAEvent{}.Fold(acc, QAEvent{Room: "r1", Kind: "up", ID: "q1", By: "alice"})

	r1q, _ := find(out.For("r1"), "q1")
	r2q, _ := find(out.For("r2"), "q1")
	assert.Equal(t, 1, r1q.Votes)
	assert.Equal(t, 0, r2q.Votes, "an upvote in r1 must not touch r2's same-id question")
}

// Anonymous upvotes (empty By) dedup together — they can't each be a fresh
// vote, or anonymity would defeat the one-per-participant rule.
func TestAnonymousUpvotesDedupTogether(t *testing.T) {
	t.Parallel()
	b := Boards{"r1": {{ID: "q1"}}}
	b = QAEvent{}.Fold(b, QAEvent{Room: "r1", Kind: "up", ID: "q1", By: ""})
	b = QAEvent{}.Fold(b, QAEvent{Room: "r1", Kind: "up", ID: "q1", By: ""})
	q, _ := find(b.For("r1"), "q1")
	assert.Equal(t, 1, q.Votes)
}

// For sorts by Votes desc, then ID asc, without mutating stored order.
func TestBoardsForSortsByVotesThenID(t *testing.T) {
	t.Parallel()
	b := Boards{"r1": {
		{ID: "q3", Votes: 1},
		{ID: "q1", Votes: 5},
		{ID: "q2", Votes: 5},
	}}
	got := b.For("r1")
	require.Len(t, got, 3)
	assert.Equal(t, []string{"q1", "q2", "q3"}, []string{got[0].ID, got[1].ID, got[2].ID})
	assert.Empty(t, b.For("missing"))
	assert.Equal(t, "q3", b["r1"][0].ID, "For must not reorder the stored slice")
}
