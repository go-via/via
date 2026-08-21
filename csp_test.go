package via_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hashSource returns the CSP source expression that admits js as an inline
// script — the same digest the browser computes. CSP hashes are STANDARD base64,
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

// The served document must set the default hardening headers it ships without
// today: nosniff (stop MIME-confusion script execution) and a charset so the
// browser does not sniff the encoding.
func TestPage_setsContentTypeAndNosniff(t *testing.T) {
	t.Parallel()
	resp, _ := do(t, newCounter(t), http.MethodGet, "/", "")
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
}

// The page must ship a strict CSP: it is iframeable (clickjacking) and any
// injection escalates to full script exec without one. Pin the load-bearing
// directives.
func TestPage_shipsStrictCSPDirectives(t *testing.T) {
	t.Parallel()
	_, _ = do(t, newCounter(t), http.MethodGet, "/", "")
	resp, _ := do(t, newCounter(t), http.MethodGet, "/", "")
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"frame-ancestors 'self'",
		"script-src 'self' 'unsafe-eval' 'sha256-",
		"style-src 'self';",
	} {
		assert.Contains(t, csp, want)
	}
}

// 'unsafe-eval' MUST be present: the bundled Datastar client compiles every
// data-* expression (including @post(...) on a click) with the Function
// constructor, which CSP gates behind 'unsafe-eval'. Without it the page passes
// every server test yet every action is silently dead in the browser — the
// exact class of bug the design fears. This guard makes that regression loud.
func TestPage_cspAllowsDatastarFunctionEval(t *testing.T) {
	t.Parallel()
	resp, _ := do(t, newCounter(t), http.MethodGet, "/", "")
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "'unsafe-eval'")
}

// via admits its own inline scripts by SHA-256 hash, which makes the emitted
// bytes load-bearing: a single stray space between the policy's digest and the
// script's text and the browser silently drops the script. Nothing else in the
// suite would notice — the server-side render still looks perfect. So compute
// the digest of every inline script the page actually served and require the
// policy to admit it.
func TestPage_everyInlineScriptIsAdmittedByItsHash(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/", "")
	csp := resp.Header.Get("Content-Security-Policy")
	scripts := inlineScripts(t, body)
	for _, js := range scripts {
		assert.Contains(t, csp, hashSource(js),
			"an inline script the page served is not admitted by its own policy")
	}
}

// There must be no nonce left to steal. via previously derived a boot nonce from
// the signing key so an action's injected redirect script would be admitted by a
// document any pod served — but that made one constant token authorise arbitrary
// inline script for the process's whole life, readable with a single curl. A
// hash carries the same cross-pod property with nothing bearer-shaped in it:
// publishing it authorises exactly the bytes via already ships.
func TestPage_cspCarriesNoNonce(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/", "")
	csp := resp.Header.Get("Content-Security-Policy")
	assert.NotContains(t, csp, "'nonce-", "a hash-based policy must mint no nonce")
	assert.NotContains(t, body, "nonce=", "no script tag may carry a nonce attribute")
	assert.Contains(t, csp, "'sha256-", "via's inline scripts are admitted by hash")
}

// The action element-patch response is morphed into the live document, so it
// must carry the same hardening headers as the page rather than shipping bare.
func TestActionPatch_carriesSecurityHeaders(t *testing.T) {
	t.Parallel()
	resp, _ := do(t, newCounter(t), http.MethodPost, "/_via/a/1", "{}")
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'self'")
}
