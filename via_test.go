package via

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

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

func TestDatastarJS(t *testing.T) {
	v := New()
	req := httptest.NewRequest("GET", "/_datastar.js", nil)
	w := httptest.NewRecorder()
	v.mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/javascript", w.Header().Get("Content-Type"))
}

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

	assert.Equal(t, "test", sig.v.Interface())
}

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

func TestConfig(t *testing.T) {
	v := New()
	v.Config(Options{DocumentTitle: "Test"})
	assert.Equal(t, "Test", v.cfg.DocumentTitle)
}
