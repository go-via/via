package shell_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/shell"
)

func TestSite_nextSkipsExternalEntries(t *testing.T) {
	t.Parallel()

	s := &shell.Site{}
	pages := shell.Pages()
	last := pages[len(pages)-1]
	_, ok := s.Next(last.Path)
	assert.False(t, ok, "%s is the last page, so there is no next page", last.Path)

	for _, p := range pages {
		assert.Empty(t, p.URL, "Pages lists only entries the site serves")
	}
}

func TestSite_nextFollowsTheNavAcrossGroups(t *testing.T) {
	t.Parallel()

	next, ok := (&shell.Site{}).Next("/compositions")
	require.True(t, ok)
	assert.Equal(t, "/islands", next.Path)
	assert.Equal(t, "Guides", next.Group)
}

func TestNavItem_sourceFileNamesARealContentFile(t *testing.T) {
	t.Parallel()

	for _, p := range shell.Pages() {
		_, err := os.Stat(filepath.Join("..", "content", p.SourceFile()))
		assert.NoError(t, err, "%s's edit link points at a missing file", p.Path)
	}
	assert.Equal(t, "landing.go", shell.NavFor("/").SourceFile())
	assert.Equal(t, "signals.go", shell.NavFor("/signals").SourceFile())
}

func TestNavFor_panicsOnAPathOutsideTheNav(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() { shell.NavFor("/platform") })
	assert.Equal(t, "Guides", shell.NavFor("/security").Group)
	assert.Empty(t, shell.NavFor("/why").Group, "an ungrouped entry has no group")
}

func TestSuggest_findsTheClosestPage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		path string
		want string
	}{
		{"one letter dropped", "/refrence", "/reference"},
		{"trailing junk", "/signalz", "/signals"},
		{"case", "/Deploy", "/deploy"},
		{"nested under a page", "/security/cookies", "/security"},
		{"by title", "/getting-started", "/start"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := shell.Suggest(tt.path)
			require.True(t, ok)
			assert.Equal(t, tt.want, got.Path)
		})
	}

	_, ok := shell.Suggest("/zzzzzzzzzzzzzzzzzzzz")
	assert.False(t, ok, "nothing close is no suggestion")
}
