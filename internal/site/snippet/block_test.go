package snippet_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go-via.dev/site/snippet"
)

func TestText_labelsAPlainBlock(t *testing.T) {
	t.Parallel()

	body := render(t, snippet.Text("Caddyfile", "go-via.dev {\n\treverse_proxy 127.0.0.1:8080\n}\n"))

	assert.Contains(t, body, `<div class="code code-file"><div class="code-name">Caddyfile</div><pre><code>go-via.dev {`)
	assert.Contains(t, body, `</pre><button class="copy"`)
}

func TestText_withoutALabelIsABareBlock(t *testing.T) {
	t.Parallel()

	body := render(t, snippet.Text("", "go get github.com/go-via/via"))

	assert.Contains(t, body, `<div class="code"><pre><code>go get github.com/go-via/via</code></pre><button class="copy"`)
}

func TestHighlight_rendersAnUnlabelledGoBlock(t *testing.T) {
	t.Parallel()

	body := render(t, snippet.Highlight("x := 1\n"))

	assert.Contains(t, body, `<div class="code"><pre class="c-chroma"><code>`)
}

func TestShow_putsTheCopyButtonAfterThePre(t *testing.T) {
	t.Parallel()

	body := render(t, snippet.Show("counter/main.go"))

	assert.Equal(t, 1, strings.Count(body, `</pre><button class="copy" type="button" aria-label="Copy code"`))
}
