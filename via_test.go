package via_test

import (
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
		c.View(func(s *via.Session) h.H {
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

func TestPageRouteParams(t *testing.T) {
	v := via.New()
	v.Page("/test/{myID}", func(c *via.Composition) {
		c.View(func(s *via.Session) h.H {
			return h.P(h.Textf("testID=%s", s.PathParam("myID")))
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
		count := via.State(42)

		c.View(func(s *via.Session) h.H {
			gotValue = count.Get(s)
			return h.Div()
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w, req)

	assert.Equal(t, 42, gotValue)
}

func TestState_SetUpdatesValue(t *testing.T) {
	s := via.NewSession()
	count := via.State(0)

	count.Set(s, 99)
	got := count.Get(s)

	assert.Equal(t, 99, got)
}

func TestState_SetAutoSyncs(t *testing.T) {
	v := via.New()
	var trigger *via.ActionHandle
	var cID string

	v.Page("/", func(c *via.Composition) {
		cID = c.ID()
		count := via.State(0)

		trigger = via.Action(c, func(s *via.Session) {
			count.Set(s, 42) // Should auto-sync
		})

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.ID("counter"),
				h.Textf("Count: %d", count.Get(s)),
			)
		})
	})

	// Load page
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	// Trigger action (auto-syncs)
	actionURL := fmt.Sprintf("/_action/%s", trigger.ID())
	req2 := httptest.NewRequest("GET", actionURL, nil)
	signals := map[string]any{"via-c": cID}
	signalsJSON, _ := json.Marshal(signals)
	req2.URL.RawQuery = "datastar=" + url.QueryEscape(string(signalsJSON))
	w2 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w2, req2)

	// Verify auto-sync sent patch
	session, err := v.TestGetSession(cID)
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
		trigger = via.Action(c, func(s *via.Session) {})
		c.View(func(s *via.Session) h.H {
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
	var cID string
	var executed bool

	v.Page("/", func(c *via.Composition) {
		cID = c.ID()

		trigger = via.Action(c, func(s *via.Session) {
			executed = true
		})

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.Button(h.Text("+"), trigger.OnClick()),
			)
		})
	})

	// First request: get the page
	req1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	v.HTTPServeMux().ServeHTTP(w1, req1)

	// Second request: trigger the action via GET with C ID
	actionURL := fmt.Sprintf("/_action/%s", trigger.ID())
	req2 := httptest.NewRequest("GET", actionURL, nil)
	// Inject via-c signal as query param (properly URL-encoded JSON)
	signals := map[string]any{"via-c": cID}
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
		trigger = via.Action(c, func(s *via.Session) {
			paramValue = s.PathParam("id")
		})

		c.View(func(s *via.Session) h.H {
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
