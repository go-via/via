package via_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sseTimeout = 3 * time.Second

// extractActionID parses an action ID from page HTML by looking for the /_action/ URL pattern.
func extractActionID(t *testing.T, body string) string {
	t.Helper()
	const prefix = "/_action/"
	idx := strings.Index(body, prefix)
	require.NotEqual(t, -1, idx, "action URL not found in page body")
	start := idx + len(prefix)
	end := strings.IndexAny(body[start:], "'&#\"")
	require.NotEqual(t, -1, end)
	return body[start : start+end]
}

// triggerAction fires a GET to /_action/{id} with the given ctxID signal.
func triggerAction(t *testing.T, serverURL, ctxID, actionID string) {
	t.Helper()
	sigsJSON := `{"via-ctx":"` + ctxID + `"}`
	resp, err := http.Get(serverURL + "/_action/" + actionID + "?datastar=" + sigsJSON)
	require.NoError(t, err)
	resp.Body.Close()
}

// TestSSE_connectionEstablished verifies opening an SSE stream for a valid context ID succeeds.
// This guards against the SSE handshake silently failing for new page contexts.
func TestSSE_connectionEstablished(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		c.View(func() h.H { return h.Div(h.Text("hi")) })
	})
	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	// The SSE handler sends an initial empty patch-elements event on connect.
	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
}

// TestSSE_syncElementsSendsElementPatch verifies SyncElements() sends a patch-elements SSE event.
// This guards against element patches being dropped instead of forwarded to the browser.
func TestSSE_syncElementsSendsElementPatch(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		syncAct := c.Action(func() {
			c.SyncElements(h.Div(h.ID("box"), h.Text("updated")))
		})
		c.View(func() h.H {
			return h.Div(
				h.Div(h.ID("box"), h.Text("initial")),
				syncAct.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	// Drain initial connection event.
	readSSEEvent(t, stream, sseTimeout)

	// Wait for SSE goroutine to be listening.
	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "updated")
}

// TestSSE_execScriptSendsScriptEvent verifies ExecScript() sends a script via a patch-elements event.
// ExecScript wraps the script in a <script> tag and sends it using PatchElements internally.
// This guards against script execution patches being dropped.
func TestSSE_execScriptSendsScriptEvent(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		scriptAct := c.Action(func() {
			c.ExecScript("console.log('hello')")
		})
		c.View(func() h.H {
			return h.Div(scriptAct.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	// Drain initial event.
	readSSEEvent(t, stream, sseTimeout)

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	// ExecuteScript uses PatchElements internally, so event type is patch-elements.
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "console.log")
}

// TestSSE_actionTriggersSyncUpdate verifies triggering an action over HTTP sends element and signal patches.
// This guards against the action → sync → SSE pipeline being broken end to end.
func TestSSE_actionTriggersSyncUpdate(t *testing.T) {
	n := 0
	server := newTestApp(t, "/", func(c *via.Context) {
		act := c.Action(func() {
			n++
			c.Sync()
		})
		c.View(func() h.H {
			return h.Div(h.Textf("n=%d", n), act.OnClick())
		})
	})

	// GET the page to create a real context with ctxID and actionID embedded in the HTML.
	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	// Drain initial event.
	readSSEEvent(t, stream, sseTimeout)

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	// The incremented counter should appear in the patched HTML.
	assert.Contains(t, ev.data, "n=1")
}
