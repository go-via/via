package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVoteFold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		seed Tallies
		ev   Vote
		want Tally // expected tally for ev.Room
		room string
	}{
		{"empty acc", nil, Vote{"r1", "a", "u"}, Tally{"a": 1}, "r1"},
		{"new choice", Tallies{"r1": {"a": 2}}, Vote{"r1", "b", "u"}, Tally{"a": 2, "b": 1}, "r1"},
		{"existing choice", Tallies{"r1": {"a": 2}}, Vote{"r1", "a", "u"}, Tally{"a": 3}, "r1"},
		{"new room", Tallies{"r1": {"a": 1}}, Vote{"r2", "x", "u"}, Tally{"x": 1}, "r2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Vote{}.Fold(tc.seed, tc.ev)
			require.Equal(t, tc.want, got.For(tc.room))
		})
	}
}

func TestVoteFoldCopyOnWrite(t *testing.T) {
	t.Parallel()
	acc := Tallies{"r1": {"a": 1}}
	out := Vote{}.Fold(acc, Vote{"r1", "a", "u"})
	require.Equal(t, 1, acc["r1"]["a"], "original acc must be untouched")
	require.Equal(t, 2, out["r1"]["a"])
	// untouched rooms keep their own backing map
	out2 := Vote{}.Fold(acc, Vote{"r2", "z", "u"})
	_, ok := acc["r2"]
	require.False(t, ok, "original must not gain new room")
	require.Equal(t, 1, out2["r2"]["z"])
}

func TestTalliesForNilSafe(t *testing.T) {
	t.Parallel()
	var ts Tallies
	require.Nil(t, ts.For("missing"))
	require.Nil(t, Tallies{"r": {"a": 1}}.For("missing"))
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
