package content

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go-via.dev/site/shell"
)

func TestGlossary_listsTermsAlphabetically(t *testing.T) {
	t.Parallel()
	var names []string
	for _, term := range glossaryTerms() {
		names = append(names, term.name)
	}
	assert.True(t, sort.SliceIsSorted(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	}), names)
}

// Other pages link /glossary#<slug>, so two terms sharing a slug would leave
// one of those links pointing at the wrong entry.
func TestGlossary_givesEveryTermItsOwnAnchor(t *testing.T) {
	t.Parallel()
	seen := map[string]string{}
	for _, term := range glossaryTerms() {
		id := shell.Slug(term.name)
		assert.NotContains(t, seen, id, "%q and %q", seen[id], term.name)
		seen[id] = term.name
		assert.NotEmpty(t, term.see, term.name)
	}
}

// A bare page link makes the reader hunt for the section that teaches the term.
func TestGlossary_pointsEverySeeLinkAtASection(t *testing.T) {
	t.Parallel()
	for _, term := range glossaryTerms() {
		for _, l := range term.see {
			assert.Contains(t, l.href, "#", "%s: %s", term.name, l.label)
		}
	}
}
