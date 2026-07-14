package via_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A live page must ship the client reconnect manager so a dropped SSE stream is
// visible (a banner) and a give-up triggers a re-bootstrap reload, instead of
// freezing the tab silently. Assert the manager's load-bearing branches are
// present in the page.
func TestReconnect_livePageShipsConnectionManager(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(quietIsland{})), http.MethodGet, "/", "")

	for _, want := range []string{
		"window.__viaRC",          // single-injection guard
		"datastar-fetch",          // the lifecycle event it listens on
		"'retrying'",              // drop → banner
		"'retries-failed'",        // give-up → reload
		"location.reload",         // the re-bootstrap
		"data-via-connection",     // connection-status attribute for app CSS
		"datastar-patch-elements", // a patch is the only "alive again" signal
	} {
		assert.Contains(t, body, want, "live page missing reconnect-manager fragment")
	}
}

// The reconnect manager is an inline script; a strict CSP blocks inline scripts
// unless they carry the page nonce. If it ships without the nonce it is silently
// dropped and the tab freezes on a drop exactly when the manager was meant to
// save it. Assert the script tag carries the policy's nonce.
func TestReconnect_managerScriptIsAdmittedByCSP(t *testing.T) {
	t.Parallel()
	resp, body := do(t, serve(t, via.Register(quietIsland{})), http.MethodGet, "/", "")

	nonce := scriptSrcNonce(t, resp.Header.Get("Content-Security-Policy"))
	assert.Contains(t, body, `nonce="`+nonce+`">(()=>{if(window.__viaRC)`,
		"reconnect script must carry the CSP nonce or the browser drops it")
}

// WithoutSSEReconnect is the documented opt-out; it must remove the manager
// entirely so an app that ships its own reconnect UX is not double-served.
func TestReconnect_optOutDropsTheManager(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(quietIsland{}, via.WithoutSSEReconnect())),
		http.MethodGet, "/", "")

	assert.NotContains(t, body, "window.__viaRC",
		"WithoutSSEReconnect must drop the reconnect manager")
}

// A stateless page has no SSE stream to lose, so injecting a reconnect manager
// would be dead weight (and a banner that can never clear). It must ship only on
// live pages.
func TestReconnect_statelessPageOmitsTheManager(t *testing.T) {
	t.Parallel()
	_, body := do(t, newCounter(t), http.MethodGet, "/", "")
	require.NotEmpty(t, body)

	assert.NotContains(t, body, "window.__viaRC",
		"stateless page must not ship the reconnect manager")
}

// The reconnect blob is a single IIFE; a stray syntax error would silently dead
// the whole manager in the browser while every server-side test still passes.
// Balanced braces/parens is a cheap structural guard against that.
func TestReconnect_blobIsBalanced(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(quietIsland{})), http.MethodGet, "/", "")

	i := strings.Index(body, "(()=>{if(window.__viaRC)")
	require.GreaterOrEqual(t, i, 0, "reconnect IIFE not found in page")
	blob := body[i:]
	end := strings.Index(blob, "</script>")
	require.GreaterOrEqual(t, end, 0, "reconnect script tag not closed")
	blob = blob[:end]

	assert.Equal(t, strings.Count(blob, "{"), strings.Count(blob, "}"),
		"unbalanced braces in reconnect blob")
	assert.Equal(t, strings.Count(blob, "("), strings.Count(blob, ")"),
		"unbalanced parens in reconnect blob")
}
