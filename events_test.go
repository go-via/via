package via_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvents_scriptShipsOnPlainAndLivePages(t *testing.T) {
	t.Parallel()
	_, plain := do(t, newCounter(t), http.MethodGet, "/", "")
	assert.Contains(t, plain, "window.__viaEV", "a plain page patches too and must ship the client events")

	_, live := do(t, serve(t, via.Handler(quietChild{})), http.MethodGet, "/", "")
	assert.Contains(t, live, "window.__viaEV", "a live page must ship the client events")
}

func TestEvents_scriptIsAdmittedByCSP(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/", "")

	csp := resp.Header.Get("Content-Security-Policy")
	found := false
	for _, js := range inlineScripts(t, body) {
		if strings.Contains(js, "__viaEV") {
			found = true
			assert.Contains(t, csp, hashSource(js),
				"the client-events script's digest must be in the policy or the browser drops it")
		}
	}
	require.True(t, found, "no inline script contained __viaEV — the loop above asserted nothing")
}

func TestEvents_scriptNamesBothEvents(t *testing.T) {
	t.Parallel()
	_, body := do(t, newCounter(t), http.MethodGet, "/", "")
	for _, want := range []string{"'via:patch'", "'via:remove'", "isConnected"} {
		assert.Contains(t, body, want)
	}
}
