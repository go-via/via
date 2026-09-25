// Package search is the site's full-text search: an index built from the
// rendered pages at startup, and the sidebar box that queries it.
package search

import (
	"slices"
	"strings"
	"unicode"
)

// Section is one heading's run of text on a page. The text before a page's
// first heading is a section with an empty Heading and Anchor.
type Section struct {
	Heading string
	Anchor  string
	Text    string
}

// Doc is one page as the index sees it.
type Doc struct {
	Title    string
	Href     string
	Sections []Section
}

// Hit is one matching section.
type Hit struct {
	Title   string
	Heading string
	Href    string
	Snippet string
}

// Index is filled before the site serves and only read after, so it takes no
// lock.
type Index struct {
	docs []Doc
}

// Add appends a page. Pages added first win ties.
func (x *Index) Add(d Doc) { x.docs = append(x.docs, d) }

const snippetRunes = 120

// Find returns up to n sections containing every word of q, best first: a
// word in the heading counts 3, in the page title 2, in the text 1. A query
// under two characters finds nothing.
func (x *Index) Find(q string, n int) []Hit {
	q = strings.ToLower(strings.TrimSpace(q))
	if len([]rune(q)) < 2 {
		return nil
	}
	words := strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' })
	if len(words) == 0 {
		return nil
	}
	type scored struct {
		hit   Hit
		score int
	}
	var found []scored
	for _, d := range x.docs {
		title := strings.ToLower(d.Title)
		for _, s := range d.Sections {
			heading, text := strings.ToLower(s.Heading), strings.ToLower(s.Text)
			score, all := 0, true
			for _, w := range words {
				in := false
				if strings.Contains(heading, w) {
					score += 3
					in = true
				}
				if strings.Contains(title, w) {
					score += 2
					in = true
				}
				if strings.Contains(text, w) {
					score++
					in = true
				}
				if !in {
					all = false
					break
				}
			}
			if !all {
				continue
			}
			href := d.Href
			if s.Anchor != "" {
				href += "#" + s.Anchor
			}
			found = append(found, scored{
				hit:   Hit{Title: d.Title, Heading: s.Heading, Href: href, Snippet: snippet(s.Text, text, words)},
				score: score,
			})
		}
	}
	slices.SortStableFunc(found, func(a, b scored) int { return b.score - a.score })
	if len(found) > n {
		found = found[:n]
	}
	hits := make([]Hit, len(found))
	for i, f := range found {
		hits[i] = f.hit
	}
	return hits
}

// snippet is about snippetRunes of text around the first word it contains,
// cut on whitespace. lower is text lower-cased; offsets are in runes because
// lower-casing can change a string's byte length.
func snippet(text, lower string, words []string) string {
	rt, rl := []rune(text), []rune(lower)
	if len(rt) != len(rl) {
		rl = rt
	}
	at := -1
	for _, w := range words {
		if i := runeIndex(rl, []rune(w)); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if len(rt) <= snippetRunes {
		return text
	}
	start := max(0, at-snippetRunes/3)
	end := min(len(rt), start+snippetRunes)
	start = max(0, end-snippetRunes)
	// Widen the start and pull in the end to whitespace, so neither cuts a
	// word; the end only moves forward when pulling it in would drop the match.
	for start > 0 && !unicode.IsSpace(rt[start-1]) {
		start--
	}
	if end < len(rt) && !unicode.IsSpace(rt[end]) {
		e := end
		for e > at && !unicode.IsSpace(rt[e]) {
			e--
		}
		if e <= at {
			for e = end; e < len(rt) && !unicode.IsSpace(rt[e]); e++ {
			}
		}
		end = e
	}
	out := strings.TrimSpace(string(rt[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(rt) {
		out += "…"
	}
	return out
}

func runeIndex(s, sub []rune) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}
