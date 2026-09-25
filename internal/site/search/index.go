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

// Doc is one page as the index sees it. NoIndex is set from the page's
// robots meta.
type Doc struct {
	Title    string
	Href     string
	Sections []Section
	NoIndex  bool
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
	docs      []entry
	indexable int
}

type entry struct {
	Doc
	title    []string
	sections []sectionTokens
}

type sectionTokens struct{ heading, body []string }

// Add appends a page. Pages added first win ties.
func (x *Index) Add(d Doc) {
	e := entry{Doc: d, title: tokens(d.Title)}
	for _, s := range d.Sections {
		e.sections = append(e.sections, sectionTokens{heading: tokens(s.Heading), body: tokens(s.Text)})
	}
	x.docs = append(x.docs, e)
	if !d.NoIndex {
		x.indexable++
	}
}

const snippetRunes = 120

// Per field, an exact word match and a word-prefix match. A section before
// the page's first heading takes the title as its heading.
const (
	headingExact, headingPrefix = 8, 4
	titleExact, titlePrefix     = 4, 2
	bodyExact, bodyPrefix       = 2, 1
)

// Find returns up to n sections in which every term of q starts a word, best
// first. Words are split on '.', '_' and camelCase as well as spaces, and the
// whole identifier is a word too, so "ttl", "withsessionttl" and
// "via.withsessionttl" all find via.WithSessionTTL. A query under two
// characters finds nothing.
func (x *Index) Find(q string, n int) []Hit {
	q = strings.ToLower(strings.TrimSpace(q))
	if len([]rune(q)) < 2 {
		return nil
	}
	terms := chunks(q)
	if len(terms) == 0 {
		return nil
	}
	type scored struct {
		hit   Hit
		score int
	}
	var found []scored
	for _, d := range x.docs {
		// A build served under an older version's base is noindex on every
		// page, for crawlers; only a page noindex among indexed ones is hidden.
		if d.NoIndex && x.indexable > 0 {
			continue
		}
		for i, s := range d.Sections {
			w := d.sections[i]
			heading := w.heading
			if s.Heading == "" {
				heading = d.title
			}
			score, all := 0, true
			for _, t := range terms {
				ts := field(heading, t, headingExact, headingPrefix) +
					field(d.title, t, titleExact, titlePrefix) +
					field(w.body, t, bodyExact, bodyPrefix)
				if ts == 0 {
					all = false
					break
				}
				score += ts
			}
			if !all {
				continue
			}
			href := d.Href
			if s.Anchor != "" {
				href += "#" + s.Anchor
			}
			found = append(found, scored{
				hit:   Hit{Title: d.Title, Heading: s.Heading, Href: href, Snippet: snippet(s.Text, strings.ToLower(s.Text), terms)},
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

// field scores t against sorted, de-duplicated words. A trailing "s" on the
// word still counts as exact, so "signal" finds the Signals page first.
func field(words []string, t string, exact, prefix int) int {
	i, ok := slices.BinarySearch(words, t)
	if ok {
		return exact
	}
	if _, ok := slices.BinarySearch(words, t+"s"); ok {
		return exact
	}
	if i < len(words) && strings.HasPrefix(words[i], t) {
		return prefix
	}
	return 0
}

// chunks splits s into runs of letters, digits, '.' and '_', trimmed of
// leading and trailing '.' and '_' so a sentence's full stop is not part of
// its last word.
func chunks(s string) []string {
	var out []string
	for _, c := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_'
	}) {
		if c = strings.Trim(c, "._"); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// tokens is s's words, lower-cased, sorted and de-duplicated: each chunk
// whole, its '.' and '_' segments, and each segment's camelCase parts.
func tokens(s string) []string {
	var out []string
	for _, c := range chunks(s) {
		out = append(out, strings.ToLower(c))
		for _, seg := range strings.FieldsFunc(c, func(r rune) bool { return r == '.' || r == '_' }) {
			out = append(out, strings.ToLower(seg))
			for _, p := range camel(seg) {
				out = append(out, strings.ToLower(p))
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// camel cuts before an upper-case letter that follows a lower-case one or
// starts a word after an acronym: WithSessionTTL → With Session TTL,
// HTTPServer → HTTP Server.
func camel(s string) []string {
	r := []rune(s)
	var out []string
	start := 0
	for i := 1; i < len(r); i++ {
		if !unicode.IsUpper(r[i]) {
			continue
		}
		if unicode.IsLower(r[i-1]) || unicode.IsDigit(r[i-1]) ||
			(unicode.IsUpper(r[i-1]) && i+1 < len(r) && unicode.IsLower(r[i+1])) {
			out = append(out, string(r[start:i]))
			start = i
		}
	}
	return append(out, string(r[start:]))
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
