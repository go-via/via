package examples_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The site module cannot embed files outside its own directory, so the
// examples page shows copies of internal/example. This keeps each copy equal
// to its original once the snippet markers are dropped, and with them the
// blank line gofmt puts before an end marker.
const original = "../../../../example"

var copied = []string{"counter", "chat", "forum"}

func TestCopies_matchTheirOriginalsBarMarkers(t *testing.T) {
	t.Parallel()
	for _, name := range copied {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := goFiles(t, filepath.Join(original, name))
			got := goFiles(t, name)
			require.NotEmpty(t, want)
			require.ElementsMatch(t, keys(want), keys(got), "copy the whole package, so it compiles as the original does")
			for f, src := range want {
				assert.Equal(t, src, dropMarkers(got[f]), "%s/%s drifted from internal/example", name, f)
			}
		})
	}
}

func goFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		b, err := os.ReadFile(p)
		out[filepath.Base(p)] = string(b)
		return err
	})
	if !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	return out
}

func dropMarkers(src string) string {
	var kept []string
	for _, l := range strings.SplitAfter(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "// snippet:") {
			continue
		}
		if strings.TrimSpace(l) == "" && len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "")
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
