package chromagen_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/snippet/gen/chromagen"
)

func TestCSS_matchesTheCommittedStylesheet(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile(chromagen.Out())
	require.NoError(t, err)

	assert.Equal(t, chromagen.CSS(), string(committed),
		"static/chroma.css is stale — run `go generate ./snippet` and commit the result")
}
