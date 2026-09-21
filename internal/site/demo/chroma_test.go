package demo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demo"
)

func TestChromaCSS_matchesTheCommittedStylesheet(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile(filepath.Join("..", "static", "chroma.css"))
	require.NoError(t, err)

	assert.Equal(t, demo.ChromaCSS(), string(committed),
		"static/chroma.css is stale — run `go generate ./demo` and commit the result")
}
