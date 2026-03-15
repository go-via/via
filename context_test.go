package via_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestView_rendersInDivWithContextID verifies the view is wrapped in a div with the context ID as its HTML id.
// This guards against Datastar element patching failing to find the target element.
func TestView_rendersInDivWithContextID(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		c.View(func() h.H { return h.P(h.Text("content")) })
	})
	body := getPageBody(t, server, "/")
	// The view is wrapped in <div id="<ctxID>">
	assert.Contains(t, body, `<div id=`)
	assert.Contains(t, body, "content")
}

// TestComponent_rendersNestedInView verifies a registered component's output appears inside the page view.
// This guards against component registration silently dropping component output.
func TestComponent_rendersNestedInView(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		compView := c.Component(func(comp *via.Context) {
			comp.View(func() h.H { return h.Span(h.Text("from-component")) })
		})
		c.View(func() h.H {
			return h.Div(compView())
		})
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "from-component")
}

// TestGetPathParam_returnsEmpty_forMissingParam verifies GetPathParam returns an empty string for unknown keys.
// This guards against nil-map panics on routes without declared parameters.
func TestGetPathParam_returnsEmpty_forMissingParam(t *testing.T) {
	v := via.New()
	var got string
	v.Page("/", func(c *via.Context) {
		got = c.GetPathParam("missing")
		c.View(func() h.H { return h.Div() })
	})
	assert.Equal(t, "", got)
}

// TestComponent_initCallback verifies the Init callback runs when the component first renders.
// This guards against init callbacks being silently ignored.
func TestComponent_initCallback(t *testing.T) {
	initCalled := false
	server := newTestApp(t, "/", func(c *via.Context) {
		comp := c.Component(func(comp *via.Context) {
			comp.Init(func() { initCalled = true })
			comp.View(func() h.H { return h.Span(h.Text("component")) })
		})
		c.View(func() h.H { return h.Div(comp()) })
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	readSSEEvent(t, stream, sseTimeout)
	assert.False(t, initCalled, "init should not run on page load")

	stream2, cancel2 := connectSSE(t, server, ctxID)
	defer cancel2()
	readSSEEvent(t, stream2, sseTimeout)
	assert.False(t, initCalled, "init should not run on subsequent connections")
}

// TestComponent_disposeCallback verifies the Dispose callback runs when session closes.
// This guards against dispose callbacks being silently ignored on session termination.
func TestComponent_disposeCallback(t *testing.T) {
	disposeCalled := false
	server := newTestApp(t, "/", func(c *via.Context) {
		c.Dispose(func() { disposeCalled = true })
		c.View(func() h.H { return h.Div(h.Text("page")) })
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	readSSEEvent(t, stream, sseTimeout)
	assert.False(t, disposeCalled, "dispose should not run before close")

	req, err := http.NewRequest("POST", server.URL+"/_sse/close", bytes.NewBufferString(ctxID))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.True(t, disposeCalled, "dispose should run after session close")
}
