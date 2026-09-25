package content

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The blocks are cut from the served file, so an edit to islands.js that
// drops what the prose points at must fail here, not on the page.
func TestIslandsJS_cutsWhatTheProseNames(t *testing.T) {
	t.Parallel()
	assert.True(t, strings.HasPrefix(jsMap, "window.viaMap = "))
	assert.Contains(t, jsMap, "if (!el._map)")
	assert.Contains(t, jsMap, "jumpTo")
	assert.True(t, strings.HasSuffix(jsMap, "};\n"))
	assert.Contains(t, jsTeardown, "e.detail.el")
	assert.Contains(t, jsTeardown, "_map.remove()")
}

func TestGaugeCSP_addsOnlyTheCDNOrigin(t *testing.T) {
	t.Parallel()
	assert.Contains(t, cdnCSP, "https://cdn.jsdelivr.net;")
	assert.Contains(t, cdnCSP, "default-src 'self';\n")
	assert.Equal(t, 3, strings.Count(cdnCSP, "'sha256-"))
	assert.NotContains(t, cdnCSP, "img-src")
	assert.NotContains(t, cdnCSP, "font-src")
}
