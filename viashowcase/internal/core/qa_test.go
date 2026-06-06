package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQAFold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		seed Boards
		ev   QAEvent
		want []Question // raw (unsorted) room slice
		room string
	}{
		{
			"ask into empty", nil,
			QAEvent{Room: "r1", Kind: "ask", ID: "q1", Text: "hi", By: "u"},
			[]Question{{ID: "q1", Text: "hi", By: "u"}}, "r1",
		},
		{
			"ask appends",
			Boards{"r1": {{ID: "q1", Text: "a"}}},
			QAEvent{Room: "r1", Kind: "ask", ID: "q2", Text: "b"},
			[]Question{{ID: "q1", Text: "a"}, {ID: "q2", Text: "b"}}, "r1",
		},
		{
			"up increments matching",
			Boards{"r1": {{ID: "q1", Votes: 0}, {ID: "q2", Votes: 3}}},
			QAEvent{Room: "r1", Kind: "up", ID: "q2"},
			[]Question{{ID: "q1", Votes: 0}, {ID: "q2", Votes: 4}}, "r1",
		},
		{
			"up unknown id no-op",
			Boards{"r1": {{ID: "q1", Votes: 1}}},
			QAEvent{Room: "r1", Kind: "up", ID: "nope"},
			[]Question{{ID: "q1", Votes: 1}}, "r1",
		},
		{
			"unknown kind no-op",
			Boards{"r1": {{ID: "q1"}}},
			QAEvent{Room: "r1", Kind: "delete", ID: "q1"},
			[]Question{{ID: "q1"}}, "r1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := QAEvent{}.Fold(tc.seed, tc.ev)
			require.Equal(t, tc.want, got[tc.room])
		})
	}
}

func TestQAFoldCopyOnWrite(t *testing.T) {
	t.Parallel()
	acc := Boards{"r1": {{ID: "q1", Votes: 1}}}
	out := QAEvent{}.Fold(acc, QAEvent{Room: "r1", Kind: "up", ID: "q1"})
	require.Equal(t, 1, acc["r1"][0].Votes, "original acc must be untouched")
	require.Equal(t, 2, out["r1"][0].Votes)

	out2 := QAEvent{}.Fold(acc, QAEvent{Room: "r1", Kind: "ask", ID: "q2"})
	require.Len(t, acc["r1"], 1, "original slice must not grow")
	require.Len(t, out2["r1"], 2)
}

func TestBoardsFor(t *testing.T) {
	t.Parallel()
	b := Boards{"r1": {
		{ID: "q3", Votes: 1},
		{ID: "q1", Votes: 5},
		{ID: "q2", Votes: 5},
	}}
	got := b.For("r1")
	want := []Question{{ID: "q1", Votes: 5}, {ID: "q2", Votes: 5}, {ID: "q3", Votes: 1}}
	require.Equal(t, want, got, "Votes desc then ID asc")

	require.Empty(t, b.For("missing"))
	// For must not mutate the stored slice order
	require.Equal(t, "q3", b["r1"][0].ID)
}
