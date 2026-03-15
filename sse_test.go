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

// triggerActionWithSignal fires a GET to /_action/{id} passing signal values in the datastar query param.
func triggerActionWithSignal(t *testing.T, serverURL, ctxID, actionID, sigID, sigValue string) {
	t.Helper()
	sigsJSON := `{"via-ctx":"` + ctxID + `","` + sigID + `":"` + sigValue + `"}`
	resp, err := http.Get(serverURL + "/_action/" + actionID + "?datastar=" + sigsJSON)
	require.NoError(t, err)
	resp.Body.Close()
}

// extractSignalID parses a signal ID from page HTML by looking for the signal ID in data attributes.
// The signal ID appears in data-bind, data-text, or data-show attributes.
func extractSignalID(t *testing.T, body string) string {
	t.Helper()
	markers := []string{`data-bind="`, `data-text="`, `data-show="`}
	for _, marker := range markers {
		idx := strings.Index(body, marker)
		if idx != -1 {
			start := idx + len(marker)
			end := strings.Index(body[start:], `"`)
			require.NotEqual(t, -1, end, "signal ID not terminated")
			return body[start : start+end]
		}
	}
	t.Fatal("signal ID not found in page body")
	return ""
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
		syncAct := c.Action(func() error {
			c.SyncElements(h.Div(h.ID("box"), h.Text("updated")))
			return nil
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
		scriptAct := c.Action(func() error {
			c.ExecScript("console.log('hello')")
			return nil
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
		act := c.Action(func() error {
			n++
			c.Sync()
			return nil
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

// TestSSE_actionReceivesInjectedSignal verifies that signals injected via datastar query param
// are accessible to the action handler via sig.Get(c).
// This guards against signal injection being skipped and action handlers reading stale values.
func TestSSE_actionReceivesInjectedSignal(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		sig := via.Signal(c, "initial")
		act := c.Action(func() error {
			// The signal should have the injected value, not "initial"
			val := sig.Get(c)
			if val != "injected" {
				c.ExecScript(`alert('expected injected, got ' + arguments.callee)`)
			}
			return nil
		})
		c.View(func() h.H {
			return h.Div(
				h.Input(sig.Bind()),
				h.Textf("val=%s", sig.Get(c)),
				act.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	readSSEEvent(t, stream, sseTimeout)
	time.Sleep(500 * time.Millisecond)

	triggerActionWithSignal(t, server.URL, ctxID, actionID, sigID, "injected")

	// Read multiple events until we find one with injected value
	// The first event might be from the initial connection
	var ev sseEvent
	for i := 0; i < 3; i++ {
		ev = readSSEEvent(t, stream, sseTimeout)
		t.Logf("Event %d: type=%s, data=%s", i, ev.eventType, ev.data)
		if strings.Contains(ev.data, "injected") {
			break
		}
	}
	// Debug: show event type and data
	t.Logf("SSE event: type=%s, data=%s", ev.eventType, ev.data)
	// If signal injection worked, we should see the patched value
	assert.Contains(t, ev.data, "val=injected")
}
