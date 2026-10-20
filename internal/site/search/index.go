// Package search is the site's full-text search: an index built from the
// rendered pages at startup, and the top bar's search box that queries it.
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

// Label is the page title, then the section heading when there is one.
func (x Hit) Label() string {
	if x.Heading == "" {
		return x.Title
	}
	return x.Title + " › " + x.Heading
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
	var found []scored
	for _, d := range x.docs {
		// A build served under an older version's base is noindex on every
		// page, for crawlers; only a page noindex among indexed ones is hidden.
		if d.NoIndex && x.indexable > 0 {
			continue
		}
		found = append(found, d.match(terms)...)
	}
	slices.SortStableFunc(found, byScore)
	if len(found) > n {
		found = found[:n]
	}
	hits := make([]Hit, len(found))
	for i, f := range found {
		hits[i] = f.hit
	}
	return hits
}

// match scores each of d's sections that holds every term.
func (d entry) match(terms []string) []scored {
	var found []scored
	for i, s := range d.Sections {
		score, ok := d.score(s, d.sections[i], terms)
		if !ok {
			continue
		}
		found = append(found, scored{hit: d.hit(s, terms), score: score})
	}
	return found
}

// score sums, over the terms, the best of each field's score; a term that
// matches nowhere fails the section.
func (d entry) score(s Section, w sectionTokens, terms []string) (int, bool) {
	heading := w.heading
	if s.Heading == "" {
		heading = d.title
	}
	total := 0
	for _, t := range terms {
		ts := field(heading, t, headingExact, headingPrefix) +
			field(d.title, t, titleExact, titlePrefix) +
			field(w.body, t, bodyExact, bodyPrefix)
		if ts == 0 {
			return 0, false
		}
		total += ts
	}
	return total, true
}

func (d entry) hit(s Section, terms []string) Hit {
	href := d.Href
	if s.Anchor != "" {
		href += "#" + s.Anchor
	}
	return Hit{Title: d.Title, Heading: s.Heading, Href: href, Snippet: snippet(s.Text, strings.ToLower(s.Text), terms)}
}

type scored struct {
	hit   Hit
	score int
}

// byScore orders highest score first.
func byScore(a, b scored) int { return b.score - a.score }

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
	for _, c := range strings.FieldsFunc(s, notWordRune) {
		if c = strings.Trim(c, "._"); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func notWordRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_'
}

func isSegmentSep(r rune) bool { return r == '.' || r == '_' }

// tokens is s's words, lower-cased, sorted and de-duplicated: each chunk
// whole, its '.' and '_' segments, and each segment's camelCase parts.
func tokens(s string) []string {
	var out []string
	for _, c := range chunks(s) {
		out = append(out, strings.ToLower(c))
		for _, seg := range strings.FieldsFunc(c, isSegmentSep) {
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
	if len(rt) <= snippetRunes {
		return text
	}
	start, end := window(rt, firstMatch(rl, words))
	out := strings.TrimSpace(string(rt[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(rt) {
		out += "…"
	}
	return out
}

// firstMatch is the rune offset of the earliest word in rl, or -1.
func firstMatch(rl []rune, words []string) int {
	at := -1
	for _, w := range words {
		if i := runeIndex(rl, []rune(w)); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	return at
}

// window is the snippetRunes of rt around the match at, widened at the start
// to whitespace so it does not cut a word.
func window(rt []rune, at int) (start, end int) {
	start = max(0, at-snippetRunes/3)
	end = min(len(rt), start+snippetRunes)
	start = max(0, end-snippetRunes)
	for start > 0 && !unicode.IsSpace(rt[start-1]) {
		start--
	}
	return start, wordEnd(rt, end, at)
}

// wordEnd pulls end in to whitespace so it does not cut a word; it only moves
// forward when pulling it in would drop the match.
func wordEnd(rt []rune, end, at int) int {
	if end >= len(rt) || unicode.IsSpace(rt[end]) {
		return end
	}
	e := end
	for e > at && !unicode.IsSpace(rt[e]) {
		e--
	}
	if e > at {
		return e
	}
	for e = end; e < len(rt) && !unicode.IsSpace(rt[e]); e++ {
	}
	return e
}

func runeIndex(s, sub []rune) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}
