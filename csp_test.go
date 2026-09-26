package via_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"regexp"
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

var nonceRe = regexp.MustCompile(`'nonce-[^']+'|data-nonce="[^"]+"`)

// withoutNonce masks a document's per-response nonce, leaving what must be a
// pure function of the config: the policy's shape and the page's bytes.
func withoutNonce(s string) string { return nonceRe.ReplaceAllString(s, "<nonce>") }

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
		"script-src 'self' 'nonce-",
		"style-src 'self';",
	} {
		assert.Contains(t, csp, want)
	}
}

func TestPage_cspForbidsEval(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	resp, page := do(t, srv, http.MethodGet, "/", "")
	patch, _ := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")
	for name, csp := range map[string]string{
		"document": resp.Header.Get("Content-Security-Policy"),
		"patch":    patch.Header.Get("Content-Security-Policy"),
	} {
		require.NotEmpty(t, csp, name)
		assert.NotContains(t, csp, "'unsafe-eval'", "Datastar compiles expressions under the page's nonce, so the %s policy needs no eval", name)
	}
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

var htmlNonceRe = regexp.MustCompile(`<html[^>]* data-nonce="([^"]+)"`)

func TestPage_everyDocumentCarriesAFreshNonceItsPolicyAdmits(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	seen := map[string]bool{}
	for range 2 {
		resp, body := do(t, srv, http.MethodGet, "/", "")
		m := htmlNonceRe.FindStringSubmatch(body)
		require.NotNil(t, m, "Datastar reads its nonce off <html data-nonce>")
		assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "'nonce-"+m[1]+"'",
			"the nonce Datastar compiles expressions under must be the one the policy admits")
		assert.GreaterOrEqual(t, len(m[1]), 22, "a nonce is 128 bits or more")
		assert.False(t, seen[m[1]], "a nonce reused across documents is a token an injection can learn once and replay")
		seen[m[1]] = true
		assert.NotContains(t, body, "<script nonce=", "via's own inline scripts stay hash-admitted")
	}
}

func TestPage_langAndNonceShareTheHTMLTag(t *testing.T) {
	t.Parallel()
	_, body := headResp(t, via.Head{Lang: "pt-PT"})
	assert.Regexp(t, `<html lang="pt-PT" data-nonce="[^"]+">`, body)
}

func TestActionPatch_carriesNoNonce(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, _ := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")
	assert.NotContains(t, resp.Header.Get("Content-Security-Policy"), "'nonce-",
		"a fragment's header governs nothing; the document's nonce stays the document's")
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
