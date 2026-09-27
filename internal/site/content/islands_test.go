package content_test

import (
	"html"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var codeBlock = regexp.MustCompile(`(?s)<div class="code-name">([^<]*)</div><pre><code>(.*?)</code></pre>`)

func islandsBlocks(t *testing.T, label string) []string {
	t.Helper()
	var out []string
	for _, m := range codeBlock.FindAllStringSubmatch(served(t, "/islands"), -1) {
		if html.UnescapeString(m[1]) == label && !strings.Contains(m[2], "<") {
			out = append(out, html.UnescapeString(m[2]))
		}
	}
	return out
}

func TestIslandsJS_cutsWhatTheProseNames(t *testing.T) {
	t.Parallel()
	blocks := islandsBlocks(t, "static/islands.js")
	require.Len(t, blocks, 2)
	jsMap, jsTeardown := blocks[0], blocks[1]
	assert.True(t, strings.HasPrefix(jsMap, "window.viaMap = "))
	assert.Contains(t, jsMap, "if (!el._map)")
	assert.Contains(t, jsMap, "jumpTo")
	assert.True(t, strings.HasSuffix(jsMap, "};\n"))
	assert.Contains(t, jsTeardown, "e.detail.el")
	assert.Contains(t, jsTeardown, "_map.remove()")
}

func TestGaugeCSP_addsOnlyTheCDNOrigin(t *testing.T) {
	t.Parallel()
	blocks := islandsBlocks(t, "Content-Security-Policy")
	require.Len(t, blocks, 1)
	cdnCSP := blocks[0]
	assert.Contains(t, cdnCSP, "https://cdn.jsdelivr.net;")
	assert.Contains(t, cdnCSP, "default-src 'self';\n")
	assert.Equal(t, 3, strings.Count(cdnCSP, "'sha256-"))
	assert.NotContains(t, cdnCSP, "img-src")
	assert.NotContains(t, cdnCSP, "font-src")
}
