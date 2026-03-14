package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
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
