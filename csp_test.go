package via_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptSrcNonce extracts the nonce from a CSP's script-src 'nonce-...' token.
func scriptSrcNonce(t *testing.T, csp string) string {
	t.Helper()
	const marker = "'nonce-"
	i := strings.Index(csp, marker)
	require.GreaterOrEqual(t, i, 0, "CSP must carry a script-src nonce: %q", csp)
	rest := csp[i+len(marker):]
	j := strings.IndexByte(rest, '\'')
	require.GreaterOrEqual(t, j, 0, "unterminated nonce in CSP: %q", csp)
	return rest[:j]
}

// scriptTagNonce extracts the nonce="..." attribute from the module script tag.
func scriptTagNonce(t *testing.T, body string) string {
	t.Helper()
	const marker = `nonce="`
	i := strings.Index(body, marker)
	require.GreaterOrEqual(t, i, 0, "page script tag must carry a nonce attribute")
	rest := body[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	require.GreaterOrEqual(t, j, 0)
	return rest[:j]
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
		"script-src 'self' 'nonce-",
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

// The browser only loads the client module if its nonce matches the policy's;
// a mismatch means nothing hydrates. They must be emitted from one value.
func TestPage_scriptNonceMatchesCSPHeader(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/", "")
	headerNonce := scriptSrcNonce(t, resp.Header.Get("Content-Security-Policy"))
	tagNonce := scriptTagNonce(t, body)
	assert.NotEmpty(t, headerNonce)
	assert.Equal(t, headerNonce, tagNonce, "script tag nonce must equal the CSP nonce")
}

// A nonce reused across responses is no nonce at all; each render must mint a
// fresh one.
func TestPage_cspNonceIsFreshPerRequest(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	r1, _ := do(t, srv, http.MethodGet, "/", "")
	r2, _ := do(t, srv, http.MethodGet, "/", "")
	assert.NotEqual(t,
		scriptSrcNonce(t, r1.Header.Get("Content-Security-Policy")),
		scriptSrcNonce(t, r2.Header.Get("Content-Security-Policy")),
		"two renders must not share a CSP nonce")
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
