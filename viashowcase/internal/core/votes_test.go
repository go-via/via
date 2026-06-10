package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// poll is a one-per-voter ballot vote; cloud is a free word submission.
func poll(room, choice, by string) Vote  { return Vote{Room: room, Choice: choice, By: by, Single: true} }
func cloud(room, choice, by string) Vote { return Vote{Room: room, Choice: choice, By: by} }

// Word-cloud submissions are not deduped: the same participant contributing the
// same or different words must keep adding to the cloud.
func TestCloudVotesAlwaysAccumulate(t *testing.T) {
	t.Parallel()
	b := Vote{}.Fold(Tallies{}, cloud("r", "go", "u"))
	b = Vote{}.Fold(b, cloud("r", "go", "u"))   // same word, same person
	b = Vote{}.Fold(b, cloud("r", "rust", "u")) // different word, same person
	assert.Equal(t, Tally{"go": 2, "rust": 1}, b.For("r"))
}

// A poll voter's first choice is counted once.
func TestPollFirstVoteCounts(t *testing.T) {
	t.Parallel()
	b := Vote{}.Fold(Tallies{}, poll("r", "pizza", "alice"))
	assert.Equal(t, Tally{"pizza": 1}, b.For("r"))
}

// Re-casting the SAME choice is a no-op — a poll voter can't inflate a choice by
// tapping it repeatedly.
func TestPollReVotingSameChoiceIsNoOp(t *testing.T) {
	t.Parallel()
	b := Vote{}.Fold(Tallies{}, poll("r", "pizza", "alice"))
	b = Vote{}.Fold(b, poll("r", "pizza", "alice"))
	assert.Equal(t, Tally{"pizza": 1}, b.For("r"))
	assert.Equal(t, 1, b.For("r").Total())
}

// Switching choice MOVES the voter's single vote: the old choice drops (and
// disappears at zero), the new one gains, and the voter's total stays one.
func TestPollSwitchingChoiceMovesTheVote(t *testing.T) {
	t.Parallel()
	b := Vote{}.Fold(Tallies{}, poll("r", "pizza", "alice"))
	b = Vote{}.Fold(b, poll("r", "sushi", "alice"))
	got := b.For("r")
	assert.Equal(t, 1, got["sushi"])
	_, stillHasPizza := got["pizza"]
	assert.False(t, stillHasPizza, "the abandoned choice must be removed at zero, not linger as pizza:0")
	assert.Equal(t, 1, got.Total(), "a voter still counts exactly once after switching")
}

// Distinct poll voters each contribute one vote.
func TestPollDistinctVotersEachCount(t *testing.T) {
	t.Parallel()
	b := Vote{}.Fold(Tallies{}, poll("r", "pizza", "alice"))
	b = Vote{}.Fold(b, poll("r", "pizza", "bob"))
	assert.Equal(t, Tally{"pizza": 2}, b.For("r"))
}

// When one of several voters on a choice switches away, the choice's count
// drops by one but survives — it must only disappear when the LAST voter
// leaves it (guards the decrement against deleting a still-supported choice).
func TestPollSwitchKeepsChoiceWithRemainingVoters(t *testing.T) {
	t.Parallel()
	b := Vote{}.Fold(Tallies{}, poll("r", "pizza", "alice"))
	b = Vote{}.Fold(b, poll("r", "pizza", "bob"))
	b = Vote{}.Fold(b, poll("r", "sushi", "alice")) // alice leaves pizza; bob remains
	assert.Equal(t, Tally{"pizza": 1, "sushi": 1}, b.For("r"))
}

// Votes in different rooms never bleed into each other.
func TestVotesAreIsolatedPerRoom(t *testing.T) {
	t.Parallel()
	b := Vote{}.Fold(Tallies{}, poll("r1", "pizza", "alice"))
	b = Vote{}.Fold(b, poll("r2", "tacos", "alice"))
	assert.Equal(t, Tally{"pizza": 1}, b.For("r1"))
	assert.Equal(t, Tally{"tacos": 1}, b.For("r2"))
}

// Folding must never mutate the accumulator — peers share the prior projection.
// The vote-move path is the dangerous one (it both decrements a count and
// rewrites the voter map), so exercise it.
func TestVoteFoldDoesNotMutateAccumulator(t *testing.T) {
	t.Parallel()
	acc := Vote{}.Fold(Tallies{}, poll("r", "pizza", "alice"))

	_ = Vote{}.Fold(acc, poll("r", "sushi", "alice")) // switch
	assert.Equal(t, Tally{"pizza": 1}, acc.For("r"), "original counts must be untouched by a switch")

	_ = Vote{}.Fold(acc, poll("r", "pizza", "bob")) // new voter
	assert.Equal(t, 1, acc.For("r").Total(), "original must not gain bob's vote")
}

// The per-voter Voted map must be deep-copied, not aliased: if a switch folded
// into a peer's projection mutated the shared voter map, a later vote folded
// into the ORIGINAL would mis-read the voter's prior choice and double-count.
// This catches a shallow Voted copy that the Counts-only mutation test misses.
func TestVoteFoldDoesNotAliasVotedMap(t *testing.T) {
	t.Parallel()
	acc := Vote{}.Fold(Tallies{}, poll("r", "pizza", "alice")) // acc: alice -> pizza
	_ = Vote{}.Fold(acc, poll("r", "sushi", "alice"))          // switch folded elsewhere

	// acc still records alice as "pizza"; re-voting pizza into acc is a no-op.
	// A corrupted (aliased) acc would think alice is "sushi" and double-count.
	check := Vote{}.Fold(acc, poll("r", "pizza", "alice"))
	assert.Equal(t, Tally{"pizza": 1}, check.For("r"))
	assert.Equal(t, 1, check.For("r").Total())
}

// Switching away and back leaves a single, correct vote (the abandoned middle
// choice must not linger).
func TestPollSwitchThenRevert(t *testing.T) {
	t.Parallel()
	b := Vote{}.Fold(Tallies{}, poll("r", "pizza", "alice"))
	b = Vote{}.Fold(b, poll("r", "sushi", "alice"))
	b = Vote{}.Fold(b, poll("r", "pizza", "alice"))
	assert.Equal(t, Tally{"pizza": 1}, b.For("r"))
}

func TestTalliesForNilSafe(t *testing.T) {
	t.Parallel()
	var ts Tallies
	require.Nil(t, ts.For("missing"))
	require.Nil(t, Vote{}.Fold(Tallies{}, poll("r", "a", "u")).For("missing"))
}

func TestTallyTotal(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0, Tally(nil).Total())
	require.Equal(t, 6, Tally{"a": 1, "b": 2, "c": 3}.Total())
}

func TestTallyRanked(t *testing.T) {
	t.Parallel()
	got := Tally{"a": 2, "b": 5, "c": 2, "d": 5}.Ranked()
	want := []Pair{{"b", 5}, {"d", 5}, {"a", 2}, {"c", 2}}
	require.Equal(t, want, got, "desc count then asc choice")

	require.Empty(t, Tally(nil).Ranked())
}
