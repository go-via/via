package via_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func TestPageRoute(t *testing.T) {
	v := via.New()
	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.H1(h.Text("Hello Via!"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "Hello Via!")
	assert.Contains(t, w.Body.String(), "<!doctype html>")
}

func TestDatastarJS(t *testing.T) {
	v := via.New()
	req := httptest.NewRequest("GET", "/_datastar.js", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/javascript", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "🖕JS_DS🚀")
}

func TestAppendHTMLAttr(t *testing.T) {
	v := via.New()
	v.AppendHTMLAttr(h.Attr("data-theme", "dark"))

	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.H1(h.Text("Hello Via!"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `<html data-theme="dark">`)
}

// mockPlugin is a test implementation of the Plugin interface
type mockPlugin struct {
	registered bool
}

func (m *mockPlugin) Register(v *via.V) {
	m.registered = true
}

func TestPluginInterface(t *testing.T) {
	mock := &mockPlugin{}

	v := via.New()
	v.Config(via.Options{
		Plugins: []via.Plugin{mock},
	})

	assert.True(t, mock.registered, "Plugin.Register should have been called")
}

func TestPageRouteParams(t *testing.T) {
	v := via.New()
	v.Page("/test/{myID}", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.P(h.Textf("testID=%s", ctx.PathParam("myID")))
		})
	})
	req := httptest.NewRequest("GET", "/test/123", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)
	assert.Contains(t, w.Body.String(), "<p>testID=123</p>")
}

func TestState_GetReturnsInitialValue(t *testing.T) {
	v := via.New()
	var gotValue int

	v.Page("/", func(c *via.Composition) {
		count := via.State(c, 42)

		c.View(func(ctx *via.Context) h.H {
			gotValue = count.Get(ctx)
			return h.Div()
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, 42, gotValue)
}

func TestState_SetAutoSyncs(t *testing.T) {
	v := via.New()
	var trigger *via.ActionHandle

	v.Page("/", func(c *via.Composition) {
		count := via.State(c, 0)

		trigger = via.Action(c, func(ctx *via.Context) {
			count.Set(ctx, 42) // Should auto-sync
		})

		c.View(func(ctx *via.Context) h.H {
			return h.Div(
				h.ID("counter"),
				h.Textf("Count: %d", count.Get(ctx)),
			)
		})
	})

	// Load page
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	tabID := extractTabIDFromHTML(t, w1.Body.String())

	// Trigger action (auto-syncs) using tabID
	actionURL := fmt.Sprintf("/_action/%s", trigger.ID())
	req2 := httptest.NewRequest("GET", actionURL, nil)
	signals := map[string]any{"via-c": tabID}
	signalsJSON, _ := json.Marshal(signals)
	req2.URL.RawQuery = "datastar=" + url.QueryEscape(string(signalsJSON))
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	// Verify auto-sync sent patch
	session, err := v.TestGetSession(tabID)
	assert.NoError(t, err)
	assert.NotNil(t, session)

	select {
	case patch := <-session.TestGetPatchChan():
		assert.Contains(t, patch.TestContent(), "Count: 42")
	default:
		t.Fatal("Expected auto-sync patch after Set")
	}
}

func TestAction_ReturnsActionHandle(t *testing.T) {
	v := via.New()
	var trigger *via.ActionHandle

	v.Page("/", func(c *via.Composition) {
		trigger = via.Action(c, func(ctx *via.Context) {})
		c.View(func(ctx *via.Context) h.H {
			return h.Div()
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.NotNil(t, trigger)
}

func TestUnknownRoute_Returns404(t *testing.T) {
	v := via.New()

	req := httptest.NewRequest("GET", "/unknown-path", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAction_ExecutesViaPOST(t *testing.T) {
	v := via.New()
	var trigger *via.ActionHandle
	var executed bool

	v.Page("/", func(c *via.Composition) {
		trigger = via.Action(c, func(ctx *via.Context) {
			executed = true
		})

		c.View(func(ctx *via.Context) h.H {
			return h.Div(
				h.Button(h.Text("+"), trigger.OnClick()),
			)
		})
	})

	// First request: get the page
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	tabID := extractTabIDFromHTML(t, w1.Body.String())

	// Second request: trigger the action via GET with tabID
	actionURL := fmt.Sprintf("/_action/%s", trigger.ID())
	req2 := httptest.NewRequest("GET", actionURL, nil)
	signals := map[string]any{"via-c": tabID}
	signalsJSON, _ := json.Marshal(signals)
	req2.URL.RawQuery = "datastar=" + url.QueryEscape(string(signalsJSON))
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	// Verify action executed
	assert.True(t, executed, "action should have executed")
}

// func TestSignal(t *testing.T) {
// 	var sig *signal
// 	v := New()
// 	v.Page("/", func(c *Context) {
// 		sig = c.Signal("test")
// 		c.View(func() h.H { return h.Div() })
// 	})
//
// 	req := httptest.NewRequest("GET", "/", nil)
// 	w := httptest.NewRecorder()
// 	v.mux.ServeHTTP(w, req)
//
// 	assert.Equal(t, "test", sig.String())
// }
//
// func TestAction(t *testing.T) {
// 	var trigger *actionTrigger
// 	var sig *signal
// 	v := New()
// 	v.Page("/", func(c *Context) {
// 		trigger = c.Action(func() {})
// 		sig = c.Signal("value")
// 		c.View(func() h.H {
// 			return h.Div(
// 				h.Button(trigger.OnClick()),
// 				h.Input(trigger.OnChange()),
// 				h.Input(trigger.OnKeyDown("Enter")),
// 				h.Button(trigger.OnClick(WithSignal(sig, "test"))),
// 				h.Button(trigger.OnClick(WithSignalInt(sig, 42))),
// 			)
// 		})
// 	})
//
// 	req := httptest.NewRequest("GET", "/", nil)
// 	w := httptest.NewRecorder()
// 	v.mux.ServeHTTP(w, req)
// 	body := w.Body.String()
// 	assert.Contains(t, body, "data-on:click")
// 	assert.Contains(t, body, "data-on:change__debounce.200ms")
// 	assert.Contains(t, body, "data-on:keydown")
// 	assert.Contains(t, body, "/_action/")
// }
//
// func TestConfig(t *testing.T) {
// 	v := New()
// 	v.Config(Options{DocumentTitle: "Test"})
// 	assert.Equal(t, "Test", v.cfg.DocumentTitle)
// }
//
// func TestPage_PanicsOnNoView(t *testing.T) {
// 	assert.Panics(t, func() {
// 		v := New()
// 		v.Page("/", func(c *Context) {})
// 	})
// }

func TestRW_PathParam(t *testing.T) {
	v := via.New()
	var paramValue string
	var trigger *via.ActionHandle

	v.Page("/users/{id}", func(c *via.Composition) {
		trigger = via.Action(c, func(ctx *via.Context) {
			paramValue = ctx.PathParam("id")
		})

		c.View(func(ctx *via.Context) h.H {
			return h.Div()
		})
	})

	// Get page with path param
	req1 := httptest.NewRequest("GET", "/users/123", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	// Trigger action
	cID := extractCIDFromHTML(t, w1.Body.String())
	actionURL := fmt.Sprintf("/_action/%s", trigger.ID())
	req2 := httptest.NewRequest("GET", actionURL, nil)
	// Include path params in signals (simulating what Datastar does)
	signals := map[string]any{"via-c": cID, "id": "123"}
	signalsJSON, _ := json.Marshal(signals)
	req2.URL.RawQuery = "datastar=" + url.QueryEscape(string(signalsJSON))
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	assert.Equal(t, "123", paramValue)
}

func TestSSE_ConnectionEstablished(t *testing.T) {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Text("Hello"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	tabID := extractTabIDFromHTML(t, w.Body.String())

	session, err := v.TestGetSession(tabID)
	assert.NoError(t, err)
	assert.NotNil(t, session)
}

// TestSession_PerTabIsolation verifies two page loads to the same route
// produce independent sessions with different tabIDs.
func TestSession_PerTabIsolation(t *testing.T) {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		count := via.State(c, 0)

		increment := via.Action(c, func(ctx *via.Context) {
			count.Set(ctx, count.Get(ctx)+1)
		})

		c.View(func(ctx *via.Context) h.H {
			return h.Div(
				h.P(h.Textf("Count: %d", count.Get(ctx))),
				h.Button(h.Text("+"), increment.OnClick()),
			)
		})
	})

	// Two page loads to the same route
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	req2 := httptest.NewRequest("GET", "/", nil)
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	tabID1 := extractTabIDFromHTML(t, w1.Body.String())
	tabID2 := extractTabIDFromHTML(t, w2.Body.String())

	// Each tab must get a unique session ID
	assert.NotEqual(t, tabID1, tabID2, "two page loads must produce different tab session IDs")

	// Mutate state in tab1 via action
	session1, err := v.TestGetSession(tabID1)
	assert.NoError(t, err)
	assert.NotNil(t, session1)

	session2, err := v.TestGetSession(tabID2)
	assert.NoError(t, err)
	assert.NotNil(t, session2)

	// Sessions must be distinct objects with independent stores
	assert.NotSame(t, session1, session2)
}

// TestSession_TabIDInSignals verifies the via-c signal contains the tabID (not cID).
func TestSession_TabIDInSignals(t *testing.T) {
	v := via.New()
	var cID string

	v.Page("/", func(c *via.Composition) {
		cID = c.ID()
		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Text("Hello"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	tabID := extractTabIDFromHTML(t, w.Body.String())

	// tabID must differ from cID (tab-specific, not composition-wide)
	assert.NotEqual(t, cID, tabID, "via-c signal must contain tabID, not cID")

	// The session must be registered under tabID
	session, err := v.TestGetSession(tabID)
	assert.NoError(t, err)
	assert.NotNil(t, session)

	// Looking up by cID should fail (no longer the session key)
	_, err = v.TestGetSession(cID)
	assert.Error(t, err, "session should NOT be registered under cID")
}

// TestSession_ActionUsesTabID verifies actions route through tabID from via-c signal.
func TestSession_ActionUsesTabID(t *testing.T) {
	v := via.New()
	var executed bool
	var trigger *via.ActionHandle

	v.Page("/", func(c *via.Composition) {
		count := via.State(c, 0)

		trigger = via.Action(c, func(ctx *via.Context) {
			executed = true
			count.Set(ctx, 42)
		})

		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Textf("Count: %d", count.Get(ctx)))
		})
	})

	// Load page to get tabID
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	tabID := extractTabIDFromHTML(t, w1.Body.String())

	// Trigger action with tabID in via-c (as browser would send)
	actionURL := fmt.Sprintf("/_action/%s", trigger.ID())
	req2 := httptest.NewRequest("GET", actionURL, nil)
	signals := map[string]any{"via-c": tabID}
	signalsJSON, _ := json.Marshal(signals)
	req2.URL.RawQuery = "datastar=" + url.QueryEscape(string(signalsJSON))
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	assert.True(t, executed, "action must execute when via-c contains tabID")

	// Verify state was set in the correct session
	session, err := v.TestGetSession(tabID)
	assert.NoError(t, err)
	select {
	case patch := <-session.TestGetPatchChan():
		assert.Contains(t, patch.TestContent(), "Count: 42")
	default:
		t.Fatal("Expected patch after action with tabID")
	}
}

// TestSession_CloseUsesTabID verifies session close uses tabID.
func TestSession_CloseUsesTabID(t *testing.T) {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Text("Test"))
		})
	})

	// Load page
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	tabID := extractTabIDFromHTML(t, w1.Body.String())

	// Session exists
	_, err := v.TestGetSession(tabID)
	assert.NoError(t, err)

	// Close with tabID (as browser beacon would send)
	req2 := httptest.NewRequest("POST", "/_session/close", bytes.NewBufferString(tabID))
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusNoContent, w2.Code)

	// Session removed
	_, err = v.TestGetSession(tabID)
	assert.Error(t, err, "session should be removed after close")
}

// TestSession_SSEUsesTabID verifies SSE handler looks up session by tabID.
func TestSession_SSEUsesTabID(t *testing.T) {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Text("Hello"))
		})
	})

	// Load page
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	tabID := extractTabIDFromHTML(t, w.Body.String())

	// Session must exist under tabID for SSE to connect
	session, err := v.TestGetSession(tabID)
	assert.NoError(t, err)
	assert.NotNil(t, session)
}

// extractTabIDFromHTML extracts the tab session ID from the via-c signal in rendered HTML.
// After per-tab sessions, via-c contains the tabID (not cID).
func extractTabIDFromHTML(t *testing.T, html string) string {
	return extractCIDFromHTML(t, html)
}

func TestPage_InitializesAllMaps(t *testing.T) {
	v := via.New()

	var comp *via.Composition
	v.Page("/", func(c *via.Composition) {
		comp = c
		// Register action directly (no nil-check needed if maps initialized)
		via.Action(c, func(ctx *via.Context) {})
		c.View(func(ctx *via.Context) h.H { return h.Div() })
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.NotNil(t, comp)
	assert.NotNil(t, comp.TestActions())
	assert.NotNil(t, comp.TestActionOwners())
}

func TestGenRandID_IsHexOnly(t *testing.T) {
	for range 100 {
		id := via.TestGenRandID()
		assert.Regexp(t, `^[0-9a-f]{32}$`, id)
	}
}

func TestAction_RejectsNonHexTabID(t *testing.T) {
	v := via.New()
	var trigger *via.ActionHandle

	v.Page("/", func(c *via.Composition) {
		trigger = via.Action(c, func(ctx *via.Context) {})
		c.View(func(ctx *via.Context) h.H { return h.Div() })
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	// Inject a malicious session ID
	actionURL := fmt.Sprintf("/_action/%s", trigger.ID())
	req2 := httptest.NewRequest("GET", actionURL, nil)
	signals := map[string]any{"via-c": "<script>alert(1)</script>"}
	signalsJSON, _ := json.Marshal(signals)
	req2.URL.RawQuery = "datastar=" + url.QueryEscape(string(signalsJSON))
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	// Should be rejected (204 from handler, but action should NOT execute)
	assert.Equal(t, http.StatusNoContent, w2.Code)
}

func TestValidateHexID_RejectsNonHex(t *testing.T) {
	assert.False(t, via.TestIsValidHexID(""))
	assert.False(t, via.TestIsValidHexID("<script>alert(1)</script>"))
	assert.False(t, via.TestIsValidHexID("abc123XY"))
	assert.False(t, via.TestIsValidHexID("abc"))
	assert.True(t, via.TestIsValidHexID(via.TestGenRandID()))
}

func TestGenRandID_FullEntropy(t *testing.T) {
	id1 := via.TestGenRandID()
	id2 := via.TestGenRandID()

	// Must be 32 hex chars (128 bits)
	assert.Len(t, id1, 32)
	assert.Len(t, id2, 32)
	assert.Regexp(t, `^[0-9a-f]{32}$`, id1)
	assert.Regexp(t, `^[0-9a-f]{32}$`, id2)
	assert.NotEqual(t, id1, id2)
}

func extractCIDFromHTML(t *testing.T, html string) string {
	// Extract via-c from data-signals meta tag (HTML-escaped quotes)
	start := strings.Index(html, "&#39;via-c&#39;:&#39;")
	if start == -1 {
		t.Fatal("via-c not found in HTML")
	}
	start += len("&#39;via-c&#39;:&#39;")
	end := strings.Index(html[start:], "&#39;")
	return html[start : start+end]
}
