package via_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-via/via/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WithTheme injects a classless stylesheet so semantic markup looks intentional
// with no class soup in the View. The <style> is nonce'd and the CSP must admit
// it (style-src with the same nonce), or the strict policy blocks it and the
// page renders unstyled.
func TestWithTheme_injectsNoncedStylesheetAdmittedByCSP(t *testing.T) {
	t.Parallel()
	resp, body := do(t, serve(t, via.Register(counter{count: &store{}}, via.WithTheme())), http.MethodGet, "/", "")

	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "style-src 'self' 'nonce-", "CSP must admit the nonce'd inline style")

	i := strings.Index(body, "<style nonce=\"")
	require.GreaterOrEqual(t, i, 0, "themed page must inject a nonced <style>")
	rest := body[i+len("<style nonce=\""):]
	styleNonce := rest[:strings.IndexByte(rest, '"')]

	headerNonce := scriptSrcNonce(t, csp) // script + style share the per-render nonce
	assert.Equal(t, headerNonce, styleNonce, "style nonce must match the CSP nonce")
}

// Without the option there is no injected stylesheet — opting out is just
// omitting WithTheme().
func TestWithoutTheme_shipsNoStylesheet(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(counter{count: &store{}})), http.MethodGet, "/", "")
	assert.NotContains(t, body, "<style")
}
