package snippet

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../../" + name)
	require.NoError(t, err)
	return string(b)
}

func TestReadme_showsTheCompiledLiveCounter(t *testing.T) {
	t.Parallel()
	_, rest, ok := strings.Cut(readRepoFile(t, "README.md"), "\n```go\n")
	require.True(t, ok, "README has no ```go block")
	block, _, ok := strings.Cut(rest, "\n```\n")
	require.True(t, ok, "README's ```go block is not closed")

	assert.Equal(t, strings.Join(lookup("start/main.go").lines, "\n"), block)
}

func TestReadme_statesTheGoVersionGoModRequires(t *testing.T) {
	t.Parallel()
	var version string
	for line := range strings.Lines(readRepoFile(t, "go.mod")) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
			version = v
			break
		}
	}
	require.NotEmpty(t, version, "go.mod has no go directive")
	m := regexp.MustCompile(`via needs Go (\S+) or newer`).FindStringSubmatch(readRepoFile(t, "README.md"))
	require.NotNil(t, m, `README no longer says "via needs Go … or newer"`)

	assert.Equal(t, version, m[1], "the README's minimum Go version must match go.mod")
}
