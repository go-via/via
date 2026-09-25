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
