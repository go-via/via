package via_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hashSource returns the CSP source expression that admits js as an inline
// script — the same digest the browser computes. CSP hashes are standard base64,
// not the URL-safe alphabet via uses for tokens.
func hashSource(js string) string {
	sum := sha256.Sum256([]byte(js))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// inlineScripts returns the text content of every inline <script> in a page. A
// script with a src is not inline: it is governed by 'self', not by a hash.
func inlineScripts(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	rest := body
	for {
		i := strings.Index(rest, "<script")
		if i < 0 {
			return out
		}
		rest = rest[i:]
		open := strings.IndexByte(rest, '>')
		require.GreaterOrEqual(t, open, 0, "unterminated <script tag")
		tag, after := rest[:open+1], rest[open+1:]
		end := strings.Index(after, "</script>")
		require.GreaterOrEqual(t, end, 0, "unterminated script element")
		if !strings.Contains(tag, " src=") {
			out = append(out, after[:end])
		}
		rest = after[end:]
	}
}

func TestPage_setsContentTypeAndNosniff(t *testing.T) {
	t.Parallel()
	resp, _ := do(t, newCounter(t), http.MethodGet, "/", "")
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
}

func TestPage_shipsStrictCSPDirectives(t *testing.T) {
	t.Parallel()
	resp, _ := do(t, newCounter(t), http.MethodGet, "/", "")
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'self'",
		"script-src 'self' 'unsafe-eval' 'sha256-",
		"style-src 'self';",
	} {
		assert.Contains(t, csp, want)
	}
}

func TestPage_cspAllowsDatastarFunctionEval(t *testing.T) {
	t.Parallel()
	resp, _ := do(t, newCounter(t), http.MethodGet, "/", "")
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "'unsafe-eval'")
}

func TestPage_everyInlineScriptIsAdmittedByItsHash(t *testing.T) {
	t.Parallel()
	// newCounter is plain and ships no inline script at all — a streaming page
	// is required so the loop below actually has bytes to check.
	resp, body := do(t, newPulse(t), http.MethodGet, "/", "")
	csp := resp.Header.Get("Content-Security-Policy")
	scripts := inlineScripts(t, body)
	require.NotEmpty(t, scripts, "a streaming page must ship at least one inline script (the reconnect manager)")
	for _, js := range scripts {
		assert.Contains(t, csp, hashSource(js),
			"an inline script the page served is not admitted by its own policy")
	}
}

func TestPage_cspCarriesNoNonce(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/", "")
	csp := resp.Header.Get("Content-Security-Policy")
	assert.NotContains(t, csp, "'nonce-", "a hash-based policy must mint no nonce")
	assert.NotContains(t, body, "nonce=", "no script tag may carry a nonce attribute")
	assert.Contains(t, csp, "'sha256-", "via's inline scripts are admitted by hash")
}

func TestActionPatch_carriesSecurityHeaders(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, _ := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'self'")
}

func TestPage_inlineAssetHashMatchesTheParsedSource(t *testing.T) {
	t.Parallel()
	resp, _ := headResp(t, via.Head{Assets: via.Assets{
		Scripts: []via.Script{{Inline: "a()\r\nb()\rc('\x00')"}},
		Styles:  []via.Style{{Inline: "a{}\r\nb{}\rc{content:'\x00'}"}},
	}})
	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, hashSource("a()\nb()\nc('\uFFFD')"),
		"the browser hashes the script after newline normalization and NUL replacement")
	assert.Contains(t, csp, hashSource("a{}\nb{}\nc{content:'\uFFFD'}"),
		"the browser hashes the style after newline normalization and NUL replacement")
}
