package via_test

import (
	"bufio"
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

// extractActionIDs extracts all action IDs from page HTML.
func extractActionIDs(t *testing.T, body string) []string {
	t.Helper()
	var ids []string
	const prefix = "/_action/"
	searchStart := 0
	for {
		idx := strings.Index(body[searchStart:], prefix)
		if idx == -1 {
			break
		}
		idx += searchStart
		start := idx + len(prefix)
		end := strings.IndexAny(body[start:], "'&#\"")
		if end == -1 {
			break
		}
		ids = append(ids, body[start:start+end])
		searchStart = start + end
	}
	require.NotEmpty(t, ids, "no action IDs found in page body")
	return ids
}

// collectEventOrTimeout reads an event from the stream with a timeout.
// Returns a bool indicating if an event was read, and the event itself.
func collectEventOrTimeout(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) (bool, sseEvent) {
	resultCh := make(chan sseEvent, 1)
	go func() {
		var ev sseEvent
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event:") {
				ev.eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if ev.data == "" {
					ev.data = d
				} else {
					ev.data += "\n" + d
				}
			} else if line == "" && ev.eventType != "" {
				resultCh <- ev
				return
			}
		}
	}()
	select {
	case ev := <-resultCh:
		return true, ev
	case <-time.After(timeout):
		return false, sseEvent{}
	}
}

// triggerAction fires a POST to /_action/{id} with the given ctxID signal.
func triggerAction(t *testing.T, serverURL, ctxID, actionID string) {
	t.Helper()
	sigsJSON := `{"via-ctx":"` + ctxID + `"}`
	resp, err := http.Post(serverURL+"/_action/"+actionID, "application/json", strings.NewReader(sigsJSON))
	require.NoError(t, err)
	resp.Body.Close()
}

// triggerActionWithSignal fires a POST to /_action/{id} with signal values in the body.
func triggerActionWithSignal(t *testing.T, serverURL, ctxID, actionID, sigID, sigValue string) {
	t.Helper()
	sigsJSON := `{"via-ctx":"` + ctxID + `","` + sigID + `":"` + sigValue + `"}`
	resp, err := http.Post(serverURL+"/_action/"+actionID, "application/json", strings.NewReader(sigsJSON))
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

func TestSSE_actionReceivesInjectedSignal(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		sig := via.Signal(c, "initial")
		act := c.Action(func() error {
			assert.Equal(t, "injected", sig.Get(c))
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

	initialEv := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", initialEv.eventType)

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

func TestSSE_noSignalSyncWhenSignalNotModifiedInAction(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		sig := via.Signal(c, "initial")
		act := c.Action(func() error {
			val := sig.Get(c)
			t.Logf("Action read: %s", val)
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

	initialEv := readSSEEvent(t, stream, sseTimeout)
	t.Logf("Initial event: type=%s", initialEv.eventType)
	assert.Equal(t, "datastar-patch-elements", initialEv.eventType)

	triggerActionWithSignal(t, server.URL, ctxID, actionID, sigID, "injected")

	for {
		ev := readSSEEvent(t, stream, sseTimeout)
		t.Logf("Event after action: type=%s, data=%s", ev.eventType, ev.data)

		if ev.eventType == "datastar-patch-signals" {
			t.Fatal("Should not receive signal patch when signal was not modified in action")
		}
		if ev.eventType == "datastar-patch-elements" {
			break
		}
	}
}

func TestSSE_noSignalPatchWhenSignalUnchanged(t *testing.T) {
	counter := 0
	server := newTestApp(t, "/", func(c *via.Context) {
		sig := via.Signal(c, "original")
		act := c.Action(func() error {
			counter++
			return nil
		})
		c.View(func() h.H {
			return h.Div(
				h.Textf("val=%s", sig.Get(c)),
				act.OnClick(),
			)
		})
	})

	require.Equal(t, 0, counter)

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	initialEv := readSSEEvent(t, stream, sseTimeout)
	t.Logf("Initial event: type=%s", initialEv.eventType)
	assert.Equal(t, "datastar-patch-elements", initialEv.eventType)

	triggerAction(t, server.URL, ctxID, actionID)

	gotEvent, ev := collectEventOrTimeout(t, stream, 50*time.Millisecond)

	t.Logf("After action - event: type=%s, data=%s, counter=%d", ev.eventType, ev.data, counter)

	assert.False(t, gotEvent, "no patch should be sent when signal is unchanged")
}
