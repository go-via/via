package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChromaCSS_matchesTheCommittedStylesheet(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile(filepath.Join("..", "..", "static", "chroma.css"))
	require.NoError(t, err)

	assert.Equal(t, chromaCSS(), string(committed),
		"static/chroma.css is stale — run `go generate ./snippet` and commit the result")
}
