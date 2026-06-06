package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestThemes(t *testing.T) {
	t.Parallel()
	require.Len(t, Themes, 19)
	require.Contains(t, Themes, "amber")
	require.Contains(t, Themes, "zinc")
}

func TestValidTheme(t *testing.T) {
	t.Parallel()
	require.True(t, ValidTheme("amber"))
	require.True(t, ValidTheme("violet"))
	require.False(t, ValidTheme("AMBER"))
	require.False(t, ValidTheme("teal"))
	require.False(t, ValidTheme(""))
}

func TestResolveTheme(t *testing.T) {
	t.Parallel()
	require.Equal(t, "blue", ResolveTheme("blue"))
	require.Equal(t, "amber", ResolveTheme("nope"))
	require.Equal(t, "amber", ResolveTheme(""))
}

func TestValidMode(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"system": "system",
		"dark":   "dark",
		"light":  "light",
		"":       "dark",
		"bogus":  "dark",
		"Dark":   "dark",
	}
	for in, want := range tests {
		require.Equal(t, want, ValidMode(in), "ValidMode(%q)", in)
	}
}
