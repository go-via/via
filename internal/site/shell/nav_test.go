package shell_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/shell"
)

func TestNext_skipsExternalEntries(t *testing.T) {
	t.Parallel()

	pages := shell.Pages()
	last := pages[len(pages)-1]
	_, ok := shell.Next(last.Path)
	assert.False(t, ok, "the Changelog entry after %s leaves the site, so there is no next page", last.Path)

	for _, p := range pages {
		assert.Empty(t, p.URL, "Pages lists only entries the site serves")
	}
}

func TestNext_followsTheNavAcrossGroups(t *testing.T) {
	t.Parallel()

	next, ok := shell.Next("/start")
	require.True(t, ok)
	assert.Equal(t, "/actions", next.Path)
	assert.Equal(t, "Build", next.Group)
}

func TestNavFor_panicsOnAPathOutsideTheNav(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() { shell.NavFor("/platform") })
	assert.Equal(t, "Run", shell.NavFor("/security").Group)
}
