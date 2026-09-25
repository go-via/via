package search_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/search"
)

const page = `<html><body><aside>sidebar text</aside><main id="main">` +
	`<h1>Deploy</h1><p>Intro &amp; more.</p>` +
	`<h2 id="shutdown-order"><a class="anchor" href="#shutdown-order">#</a>Shutdown order</h2>` +
	`<p>Close first.</p><pre><code>r.Close()</code></pre><button>Copy</button>` +
	`<details><summary>Source</summary>hidden</details>` +
	`<h3 id="note">Note</h3><p>Last.</p></main></body></html>`

func TestExtract_splitsTheMainElementAtHeadings(t *testing.T) {
	t.Parallel()

	d := search.Extract("Deploy", "/deploy", page)
	require.Len(t, d.Sections, 3)
	assert.Equal(t, search.Section{Text: "Deploy Intro & more."}, d.Sections[0])
	assert.Equal(t, search.Section{Heading: "Shutdown order", Anchor: "shutdown-order", Text: "Close first."}, d.Sections[1])
	assert.Equal(t, search.Section{Heading: "Note", Anchor: "note", Text: "Last."}, d.Sections[2])
}

func TestExtract_dropsCodeButtonsAndFoldedPanes(t *testing.T) {
	t.Parallel()

	d := search.Extract("Deploy", "/deploy", page)
	for _, s := range d.Sections {
		assert.NotContains(t, s.Text, "r.Close()")
		assert.NotContains(t, s.Text, "Copy")
		assert.NotContains(t, s.Text, "hidden")
		assert.NotContains(t, s.Text, "sidebar")
	}
}

const refPage = `<html><head><title>API reference</title></head><body><main>` +
	`<h2 id="router-options">Router options</h2><p>Router-wide policy.</p>` +
	`<table class="ref-table"><tr><th>Call</th><th>Does</th></tr>` +
	`<tr id="via.WithSessionTTL"><td><code>via.WithSessionTTL(d)</code></td><td>How long an idle session lives.</td></tr>` +
	`<tr><td><code>via.WithSessionKey(key)</code></td><td>Signs the cookie.</td></tr>` +
	`<tr><td>plain</td><td>A row with no code stays text.</td></tr>` +
	`</table><nav class="pager"><a href="/next">Next: Deploy</a></nav></main>` +
	`<footer>footer text</footer></body></html>`

func TestExtract_makesEachCodeRowItsOwnSection(t *testing.T) {
	t.Parallel()

	d := search.Extract("API reference", "/reference", refPage)
	require.Len(t, d.Sections, 3)
	assert.Equal(t, "Router options", d.Sections[0].Heading)
	assert.Contains(t, d.Sections[0].Text, "A row with no code stays text.")
	assert.NotContains(t, d.Sections[0].Text, "idle session")
	assert.Equal(t, search.Section{Heading: "via.WithSessionTTL(d)", Anchor: "via.WithSessionTTL", Text: "How long an idle session lives."}, d.Sections[1])
	assert.Equal(t, search.Section{Heading: "via.WithSessionKey(key)", Anchor: "router-options", Text: "Signs the cookie."}, d.Sections[2],
		"a row without an id links to its heading")
}

func TestExtract_dropsThePagerAndFooter(t *testing.T) {
	t.Parallel()

	d := search.Extract("API reference", "/reference", refPage)
	for _, s := range d.Sections {
		assert.NotContains(t, s.Text, "Next")
		assert.NotContains(t, s.Text, "footer")
	}
}

func TestExtract_marksANoIndexPage(t *testing.T) {
	t.Parallel()

	assert.False(t, search.Extract("Deploy", "/deploy", page).NoIndex)
	stub := `<html><head><meta name="robots" content="noindex"></head><body><main><p>Coming.</p></main></body></html>`
	assert.True(t, search.Extract("Glossary", "/glossary", stub).NoIndex)
}
