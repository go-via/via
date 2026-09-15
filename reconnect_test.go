package via_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A streaming page must ship the client reconnect manager so a dropped SSE stream is
// visible (a banner) and a give-up triggers a re-bootstrap reload, instead of
// freezing the tab silently. Assert the manager's load-bearing branches are
// present in the page.
func TestReconnect_livePageShipsConnectionManager(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(quietChild{})), http.MethodGet, "/", "")

	for _, want := range []string{
		"window.__viaRC",          // single-injection guard
		"datastar-fetch",          // the lifecycle event it listens on
		"'retrying'",              // drop → banner
		"'retries-failed'",        // give-up → reload
		"location.reload",         // the re-bootstrap
		"n>=2",                    // terminal state: max 2 reloads, then a pinned banner
		"data-via-connection",     // connection-status attribute for app CSS
		"datastar-patch-elements", // a patch is the only "alive again" signal
		"d.el===document.body",    // a clean close of the SSE @post IS a drop (blocker: retry:"auto" fires only 'finished')
		"'error'",                 // datastar-fetch error carries the HTTP status
		"argsRaw",                 // ...in detail.argsRaw.status, per the bundled datastar.js
		"s===410",                 // a stale tab reloads once
		"s===403||s>=500",         // a server-side refusal is a banner, never a reload loop
	} {
		assert.Contains(t, body, want, "streaming page missing reconnect-manager fragment")
	}
}

// The reconnect manager is an inline script and a strict CSP blocks inline
// scripts it does not explicitly admit. If it ships unadmitted it is silently
// dropped and the tab freezes on a drop exactly when the manager was meant to
// save it. It is admitted by the SHA-256 of its own bytes, so assert the served
// tag is bare (no nonce) and that the policy carries its exact digest — that is
// what catches a stray byte added around the script.
func TestReconnect_managerScriptIsAdmittedByCSP(t *testing.T) {
	t.Parallel()
	resp, body := do(t, serve(t, via.Handler(quietChild{})), http.MethodGet, "/", "")

	assert.Contains(t, body, `<script>(()=>{if(window.__viaRC)`,
		"the reconnect script ships bare — its hash, not a nonce, admits it")
	csp := resp.Header.Get("Content-Security-Policy")
	found := false
	for _, js := range inlineScripts(t, body) {
		if strings.Contains(js, "__viaRC") {
			found = true
			assert.Contains(t, csp, hashSource(js),
				"the reconnect script's digest must be in the policy or the browser drops it")
		}
	}
	require.True(t, found, "no inline script contained __viaRC — the loop above asserted nothing")
}

// A plain page has no SSE stream to lose, so injecting a reconnect manager
// would be dead weight (and a banner that can never clear). It must ship only on
// streaming pages.
func TestReconnect_plainPageOmitsTheManager(t *testing.T) {
	t.Parallel()
	_, body := do(t, newCounter(t), http.MethodGet, "/", "")
	require.NotEmpty(t, body)

	assert.NotContains(t, body, "window.__viaRC",
		"plain page must not ship the reconnect manager")
}

// The reconnect blob is a single IIFE; a stray syntax error would silently dead
// the whole manager in the browser while every server-side test still passes.
// Balanced braces/parens is a cheap structural guard against that.
func TestReconnect_blobIsBalanced(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(quietChild{})), http.MethodGet, "/", "")

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
