package via_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

// Test that Sync() sends patches via SSE
func TestSSE_SyncSendsPatch(t *testing.T) {
	v := via.New()
	var trigger *via.ActionHandle
	var cID string

	v.Page("/", func(c *via.Composition) {
		cID = c.ID()
		count := via.State(0)

		trigger = via.Action(c, func(s *via.Session) {
			count.Set(s, 42) // Auto-syncs
		})

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.ID("counter"),
				h.Textf("Count: %d", count.Get(s)),
			)
		})
	})

	// Load page to create session
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	// Trigger action (this will call Sync)
	actionURL := fmt.Sprintf("/_action/%s", trigger.ID())
	req2 := httptest.NewRequest("GET", actionURL, nil)
	signals := map[string]any{"via-c": cID}
	signalsJSON, _ := json.Marshal(signals)
	req2.URL.RawQuery = "datastar=" + url.QueryEscape(string(signalsJSON))
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	// Verify session exists and has a patch
	session, err := v.TestGetSession(cID)
	assert.NoError(t, err)
	assert.NotNil(t, session)

	// Check if patch was sent (non-blocking check)
	select {
	case patch := <-session.TestGetPatchChan():
		// Patch should contain updated HTML with count=42
		assert.Contains(t, patch.TestContent(), "Count: 42")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No patch received within timeout")
	}
}

// Test SSE connection establishment
func TestSSE_ConnectionEstablished(t *testing.T) {
	v := via.New()
	var cID string

	v.Page("/", func(c *via.Composition) {
		cID = c.ID()
		c.View(func(s *via.Session) h.H {
			return h.Div(h.Text("Hello"))
		})
	})

	// Load page to create session
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	// Extract cID from HTML
	html := w1.Body.String()
	start := strings.Index(html, "&#39;via-c&#39;:&#39;")
	assert.NotEqual(t, -1, start, "via-c should be in HTML")
	start += len("&#39;via-c&#39;:&#39;")
	end := strings.Index(html[start:], "&#39;")
	extractedCID := html[start : start+end]
	assert.Equal(t, cID, extractedCID)

	// Verify session was created
	session, err := v.TestGetSession(cID)
	assert.NoError(t, err)
	assert.NotNil(t, session)
}
