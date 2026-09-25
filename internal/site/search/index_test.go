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

func rankIndex() *search.Index {
	idx := &search.Index{}
	idx.Add(search.Doc{Title: "Actions", Href: "/actions", Sections: []search.Section{
		{Text: "Actions A click POSTs to a method on your page type, and via renders the page again."},
		{Heading: "Split views", Anchor: "split-views", Text: "Split the page into panes; each one throttles its own updates."},
	}})
	idx.Add(search.Doc{Title: "API reference", Href: "/reference", Sections: []search.Section{
		{Heading: "Events", Anchor: "events", Text: "Package on names each DOM event as a function."},
		{Heading: "on.Click, on.DblClick, on.Input, on.Submit", Anchor: "on.Click", Text: "Post a method on that event."},
		{Heading: "on.ClickCS(e, opts…), on.EventCS(name, e, opts…)", Anchor: "on.ClickCS", Text: "Run e in the browser on the event."},
		{Heading: "via.Signal[T], via.SignalCS[T]", Anchor: "via.Signal", Text: "Client-resident state that round-trips per request."},
		{Heading: "via.WithTrustedOrigin(origin)", Anchor: "via.WithTrustedOrigin", Text: "Turns on origin enforcement for the action endpoint."},
		{Heading: "via.WithSessionTTL(d)", Anchor: "via.WithSessionTTL", Text: "How long an idle session lives."},
		{Heading: "expr.Val(v), expr.Lit(v)", Anchor: "expr.Val", Text: "Encodes v as a JavaScript literal. Lit is the deprecated alias."},
	}})
	idx.Add(search.Doc{Title: "Signals", Href: "/signals", Sections: []search.Section{
		{Text: "Signals A Signal lives in the browser, and its wire name is its field name."},
		{Heading: "Computed", Anchor: "computed", Text: "A computed signal derives from other signals."},
	}})
	idx.Add(search.Doc{Title: "Sessions & security", Href: "/security", Sections: []search.Section{
		{Text: "Every action checks the request's Origin against the trusted list; a click from another origin gets 403."},
	}})
	return idx
}

func TestIndex_ranksIdentifiersAndHeadingsFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, q, want string
	}{
		{"camel-case part of an identifier", "ttl", "/reference#via.WithSessionTTL"},
		{"dotted segment, not a substring of split", "lit", "/reference#expr.Val"},
		{"page title over a reference row", "signal", "/signals"},
		{"identifier over body text", "origin", "/reference#via.WithTrustedOrigin"},
		{"every term on one row", "on click", "/reference#on.Click"},
		{"the full identifier", "on.Click", "/reference#on.Click"},
		{"a whole identifier word", "withsessionttl", "/reference#via.WithSessionTTL"},
	}
	idx := rankIndex()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hits := idx.Find(tt.q, 8)
			require.NotEmpty(t, hits)
			assert.Equal(t, tt.want, hits[0].Href)
		})
	}
}

func TestIndex_matchesWordPrefixesNotSubstrings(t *testing.T) {
	t.Parallel()

	for _, h := range rankIndex().Find("lit", 8) {
		assert.NotEqual(t, "/actions#split-views", h.Href, "lit is inside split, not at a word start")
	}
	for _, h := range rankIndex().Find("ttl", 8) {
		assert.NotEqual(t, "/actions#split-views", h.Href, "ttl is inside throttles")
	}
	hits := rankIndex().Find("thro", 8)
	require.Len(t, hits, 1)
	assert.Equal(t, "/actions#split-views", hits[0].Href)
}

func TestIndex_skipsNoIndexDocs(t *testing.T) {
	t.Parallel()

	idx := rankIndex()
	idx.Add(search.Doc{Title: "Glossary", Href: "/glossary", NoIndex: true, Sections: []search.Section{{Text: "Glossary signal"}}})
	for _, h := range idx.Find("glossary", 8) {
		assert.NotEqual(t, "/glossary", h.Href)
	}
}

func TestIndex_keepsNoIndexDocsWhenEveryDocIsNoIndex(t *testing.T) {
	t.Parallel()

	// An older version's build marks every page noindex for crawlers; its own
	// search still has to work.
	idx := &search.Index{}
	idx.Add(search.Doc{Title: "Deploy", Href: "/v0.8/deploy", NoIndex: true, Sections: []search.Section{{Text: "Shutdown order"}}})
	assert.Len(t, idx.Find("shutdown", 8), 1)
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
