package snippet

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The repo README is the GitHub landing page; its program must stay the one
// the build compiles.
func TestReadme_showsTheCompiledCounter(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../../README.md")
	require.NoError(t, err)
	_, rest, ok := strings.Cut(string(b), "\n```go\n")
	require.True(t, ok, "README has no ```go block")
	block, _, ok := strings.Cut(rest, "\n```\n")
	require.True(t, ok, "README's ```go block is not closed")

	assert.Equal(t, strings.Join(lookup("counter/main.go").lines, "\n"), block)
}
