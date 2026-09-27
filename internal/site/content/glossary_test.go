package content_test

import (
	"html"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/shell"
)

var (
	glossaryH3   = regexp.MustCompile(`<h3 `)
	glossaryTerm = regexp.MustCompile(`(?s)<h3 id="([^"]+)"><a class="anchor"[^>]*>#</a>([^<]*)</h3><p>.*?</p><p>See: (.*?)</p>`)
	glossaryLink = regexp.MustCompile(`<a href="([^"]*)">(.*?)</a>`)
)

type glossaryEntry struct {
	id, name string
	see      []string
}

// Read off the served page, so a term whose markup drifts from the pattern
// fails the count.
func glossaryEntries(t *testing.T) []glossaryEntry {
	t.Helper()
	page := served(t, "/glossary")
	ms := glossaryTerm.FindAllStringSubmatch(page, -1)
	require.NotEmpty(t, ms)
	require.Len(t, ms, len(glossaryH3.FindAllString(page, -1)), "every <h3> is a term with a See: line")
	var out []glossaryEntry
	for _, m := range ms {
		e := glossaryEntry{id: m[1], name: html.UnescapeString(m[2])}
		for _, l := range glossaryLink.FindAllStringSubmatch(m[3], -1) {
			// A link wrapping <code> is an API row, not a section.
			if !strings.Contains(l[2], "<code>") {
				e.see = append(e.see, html.UnescapeString(l[1]))
			}
		}
		out = append(out, e)
	}
	return out
}

func TestGlossary_listsTermsAlphabetically(t *testing.T) {
	t.Parallel()
	var names []string
	for _, e := range glossaryEntries(t) {
		names = append(names, e.name)
	}
	assert.True(t, sort.SliceIsSorted(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	}), names)
}

func TestGlossary_givesEveryTermItsOwnAnchor(t *testing.T) {
	t.Parallel()
	for _, e := range glossaryEntries(t) {
		assert.Equal(t, shell.Slug(e.name), e.id, "%s: a repeated slug gets a -2 suffix", e.name)
		assert.NotEmpty(t, e.see, e.name)
	}
}

func TestGlossary_pointsEverySeeLinkAtASection(t *testing.T) {
	t.Parallel()
	for _, e := range glossaryEntries(t) {
		for _, href := range e.see {
			assert.Contains(t, href, "#", "%s: %s", e.name, href)
		}
	}
}
