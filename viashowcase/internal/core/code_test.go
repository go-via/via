package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{31, "Z"},
		{32, "10"},
		{33, "11"},
		{-32, "10"}, // abs value
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, Code(tc.n), "Code(%d)", tc.n)
	}
}

func TestCodeDeterministicAndUnique(t *testing.T) {
	t.Parallel()
	seen := map[string]int64{}
	for n := int64(0); n < 5000; n++ {
		c := Code(n)
		require.Equal(t, c, Code(n), "deterministic")
		require.NotEmpty(t, c)
		prev, dup := seen[c]
		require.Falsef(t, dup, "collision Code(%d)==Code(%d)==%q", n, prev, c)
		seen[c] = n
		for _, r := range c {
			require.Contains(t, codeAlphabet, string(r))
		}
	}
}
