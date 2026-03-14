package via_test

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/require"
)

func newTestApp(t *testing.T, route string, initFn func(*via.Context)) *httptest.Server {
	t.Helper()
	v := via.New()
	v.Page(route, initFn)
	server := httptest.NewServer(v.HTTPServeMux())
	t.Cleanup(server.Close)
	return server
}

func getPageBody(t *testing.T, server *httptest.Server, route string) string {
	t.Helper()
	resp, err := http.Get(server.URL + route)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// extractCtxID parses the via-ctx value from the page HTML (data-signals meta tag).
// Single quotes in attribute values are HTML-escaped to &#39; by the renderer.
func extractCtxID(t *testing.T, body string) string {
	t.Helper()
	const marker = "&#39;via-ctx&#39;:&#39;"
	idx := strings.Index(body, marker)
	require.NotEqual(t, -1, idx, "via-ctx not found in page body")
	start := idx + len(marker)
	end := strings.Index(body[start:], "&#39;")
	require.NotEqual(t, -1, end, "via-ctx value not terminated")
	return body[start : start+end]
}

// connectSSE opens an SSE stream for the given ctxID and returns a scanner for reading events and a cancel func.
// The scanner must be used from a single goroutine only.
func connectSSE(t *testing.T, server *httptest.Server, ctxID string) (*bufio.Scanner, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sigsJSON := `{"via-ctx":"` + ctxID + `"}`
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/_sse?datastar="+sigsJSON, nil)
	require.NoError(t, err)
	// No Accept-Encoding header → uncompressed response
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return bufio.NewScanner(resp.Body), cancel
}

// sseEvent holds a parsed SSE event.
type sseEvent struct {
	eventType string
	data      string
}

// readSSEEvent reads the next SSE event from the stream with a timeout.
// The scanner must not be shared across goroutines.
func readSSEEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) sseEvent {
	t.Helper()
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
		return ev
	case <-time.After(timeout):
		t.Fatal("timed out waiting for SSE event")
		return sseEvent{}
	}
}

// renderH renders an h.H node to a string.
func renderH(t *testing.T, node h.H) string {
	t.Helper()
	var buf bytes.Buffer
	err := node.Render(&buf)
	require.NoError(t, err)
	return buf.String()
}

// signalT is the test interface covering the UI/identity methods of any signal.
// Get() is excluded because its return type is generic — use concrete signal types for Get tests.
type signalT interface {
	ID() string
	Err() error
	Bind() h.H
	Text() h.H
	Show() h.H
	Ref() string
	Tag(string)
}

// captureSignal creates a throwaway via app, runs initFn eagerly via v.Page, and returns the captured signal.
func captureSignal(initFn func(c *via.Context) signalT) signalT {
	v := via.New()
	var sig signalT
	v.Page("/", func(c *via.Context) {
		sig = initFn(c)
		c.View(func() h.H { return h.Div() })
	})
	return sig
}

// actionT is a local interface matching the exported methods of *actionTrigger.
type actionT interface {
	OnClick(options ...via.ActionTriggerOption) h.H
	OnChange(options ...via.ActionTriggerOption) h.H
	OnKeyDown(key string, options ...via.ActionTriggerOption) h.H
}

// captureAction creates a throwaway via app, runs initFn eagerly via v.Page, and returns the captured action trigger.
func captureAction(initFn func(c *via.Context) actionT) actionT {
	v := via.New()
	var act actionT
	v.Page("/", func(c *via.Context) {
		act = initFn(c)
		c.View(func() h.H { return h.Div() })
	})
	return act
}
