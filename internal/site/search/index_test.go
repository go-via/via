package search_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/search"
)

func testIndex() *search.Index {
	idx := &search.Index{}
	idx.Add(search.Doc{Title: "Deploy", Href: "/deploy", Sections: []search.Section{
		{Text: "A via process holds two kinds of state."},
		{Heading: "Shutdown order", Anchor: "shutdown-order", Text: "Close the router first, then Shutdown."},
		{Heading: "Restarts", Anchor: "restarts", Text: "The cookie is signed with the session key."},
	}})
	idx.Add(search.Doc{Title: "Sessions", Href: "/security", Sections: []search.Section{
		{Heading: "Auth", Anchor: "auth", Text: "OnInit reads the session and the View branches on it."},
	}})
	return idx
}

func TestIndex_ranksHeadingAboveText(t *testing.T) {
	t.Parallel()

	hits := testIndex().Find("session", 8)
	require.Len(t, hits, 2)
	assert.Equal(t, "/security#auth", hits[0].Href, "title match outranks a text-only match")
	assert.Equal(t, "/deploy#restarts", hits[1].Href)
}

func TestIndex_matchesOnlySectionsWithEveryWord(t *testing.T) {
	t.Parallel()

	hits := testIndex().Find("router shutdown", 8)
	require.Len(t, hits, 1)
	assert.Equal(t, "Shutdown order", hits[0].Heading)
	assert.Equal(t, "/deploy#shutdown-order", hits[0].Href)

	assert.Empty(t, testIndex().Find("router restarts", 8))
}

func TestIndex_linksTheIntroToThePageItself(t *testing.T) {
	t.Parallel()

	hits := testIndex().Find("kinds", 8)
	require.Len(t, hits, 1)
	assert.Equal(t, "/deploy", hits[0].Href)
}

func TestIndex_ignoresAOneCharacterQuery(t *testing.T) {
	t.Parallel()

	assert.Nil(t, testIndex().Find("s", 8))
}

func TestIndex_cutsToN(t *testing.T) {
	t.Parallel()

	assert.Len(t, testIndex().Find("the", 1), 1)
}

func TestIndex_snippetCutsOnWordBoundaries(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("alpha bravo charlie delta echo foxtrot golf hotel ", 6) +
		"the readiness probe answers " +
		strings.Repeat("india juliett kilo lima mike november oscar papa ", 6)
	idx := &search.Index{}
	idx.Add(search.Doc{Title: "Deploy", Href: "/deploy", Sections: []search.Section{{Text: text}}})

	hits := idx.Find("readiness", 8)
	require.Len(t, hits, 1)
	s := hits[0].Snippet
	assert.Contains(t, s, "readiness")
	require.True(t, strings.HasPrefix(s, "…") && strings.HasSuffix(s, "…"), s)
	body := strings.TrimSuffix(strings.TrimPrefix(s, "…"), "…")
	assert.InDelta(t, 120, utf8.RuneCountInString(body), 20)
	whole := strings.Fields(text)
	for _, w := range strings.Fields(body) {
		assert.Contains(t, whole, w, "snippet word %q is cut", w)
	}
}
