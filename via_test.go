package via

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

// TestPageRoute verifies basic page serving works.
// This guards against regressions in the core routing/serving pipeline.
func TestPageRoute(t *testing.T) {
	v := New()
	v.Page("/", func(c *Context) {
		c.View(func() h.H {
			return h.Div(h.Text("Hello Via!"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Hello Via!")
	assert.Contains(t, w.Body.String(), "<!doctype html>")
}

// TestDatastarJS ensures the embedded Datastar JS is served correctly.
// This guards against accidentally breaking client-side reactivity by embedding stale/broken JS.
func TestDatastarJS(t *testing.T) {
	v := New()
	req := httptest.NewRequest("GET", "/_datastar.js", nil)
	w := httptest.NewRecorder()
	v.mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/javascript", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "🖕JS_DS🚀")
}

// TestSignal verifies Signal creates a signal and its value is retrievable.
// This guards against signal lifecycle issues where signals might be lost or return nil.
func TestSignal(t *testing.T) {
	var sig *signal
	v := New()
	v.Page("/", func(c *Context) {
		sig = c.Signal("test")
		c.View(func() h.H { return h.Div() })
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.mux.ServeHTTP(w, req)

	assert.Equal(t, "test", sig.String())
}

// TestAction verifies actions are registered and rendered as Datastar attributes.
// This guards against broken event binding that would prevent user interactions from reaching server handlers.
func TestAction(t *testing.T) {
	var trigger *actionTrigger
	var sig *signal
	v := New()
	v.Page("/", func(c *Context) {
		trigger = c.Action(func() {})
		sig = c.Signal("value")
		c.View(func() h.H {
			return h.Div(
				h.Button(trigger.OnClick()),
				h.Input(trigger.OnChange()),
				h.Input(trigger.OnKeyDown("Enter")),
				h.Button(trigger.OnClick(WithSignal(sig, "test"))),
				h.Button(trigger.OnClick(WithSignalInt(sig, 42))),
			)
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	v.mux.ServeHTTP(w, req)
	body := w.Body.String()
	assert.Contains(t, body, "data-on:click")
	assert.Contains(t, body, "data-on:change__debounce.200ms")
	assert.Contains(t, body, "data-on:keydown")
	assert.Contains(t, body, "/_action/")
}

// TestConfig verifies Options.Config correctly overrides defaults.
// This guards against silent config failures where users expect overrides to take effect.
func TestConfig(t *testing.T) {
	v := New()
	v.Config(Options{DocumentTitle: "Test"})
	assert.Equal(t, "Test", v.cfg.DocumentTitle)
}

// TestPage_PanicsOnNoView ensures Page panics when no View is provided.
// This guards against silent failures where pages would render nothing without any error.
func TestPage_PanicsOnNoView(t *testing.T) {
	assert.Panics(t, func() {
		v := New()
		v.Page("/", func(c *Context) {})
	})
}
