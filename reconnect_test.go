package via_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconnect_livePageShipsConnectionManager(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(quietChild{})), http.MethodGet, "/", "")

	for _, want := range []string{
		"window.__viaRC",                // single-injection guard
		"datastar-fetch",                // the lifecycle event it listens on
		"'retrying'",                    // drop → banner
		"'retries-failed'",              // give-up → reload
		"location.reload",               // the re-bootstrap
		"n>=2",                          // terminal state: max 2 reloads, then a pinned banner
		"data-via-connection",           // connection-status attribute for app CSS
		"datastar-patch-elements",       // a patch is the only "alive again" signal
		"d.el===document.body",          // a clean close of the SSE @post IS a drop (blocker: retry:"auto" fires only 'finished')
		"'error'",                       // datastar-fetch error carries the HTTP status
		"argsRaw",                       // ...in detail.argsRaw.status, per the bundled datastar.js
		"s===410",                       // a stale tab reloads once
		"s===403)stop",                  // a refused stream is a banner, never a reload loop
		"adoptedStyleSheets",            // the banner's styling is a constructed sheet, not inline
		":where(#via-reconnect-banner)", // ...whose rules carry zero specificity
		"[data-via-connection=offline]", // ...and colour the banner by state
		"'Disconnected",                 // the give-up copy
		"'Reconnect'",                   // ...and the button that acts on it
	} {
		assert.Contains(t, body, want, "streaming page missing reconnect-manager fragment")
	}
}

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

func TestReconnect_plainPageOmitsTheManager(t *testing.T) {
	t.Parallel()
	_, body := do(t, newCounter(t), http.MethodGet, "/", "")
	require.NotEmpty(t, body)

	assert.NotContains(t, body, "window.__viaRC",
		"plain page must not ship the reconnect manager")
}

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
