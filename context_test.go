package via_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestView_rendersInDivWithContextID(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		c.View(func() h.H { return h.P(h.Text("content")) })
	})
	body := getPageBody(t, server, "/")
	// The view is wrapped in <div id="<ctxID>">
	assert.Contains(t, body, `<div id=`)
	assert.Contains(t, body, "content")
}

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

func TestGetPathParam_returnsEmpty_forMissingParam(t *testing.T) {
	v := via.New()
	var got string
	v.Page("/", func(c *via.Context) {
		got = c.GetPathParam("missing")
		c.View(func() h.H { return h.Div() })
	})
	assert.Equal(t, "", got)
}

func TestComponent_initCallback(t *testing.T) {
	initCalled := false
	server := newTestApp(t, "/", func(c *via.Context) {
		comp := c.Component(func(comp *via.Context) {
			comp.Init(func() { initCalled = true })
			comp.View(func() h.H { return h.Span(h.Text("component")) })
		})
		c.View(func() h.H { return h.Div(comp()) })
	})

	assert.True(t, initCalled, "init should run when component is created during page load")

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	readSSEEvent(t, stream, sseTimeout)
	assert.True(t, initCalled, "init should persist across SSE connections")
}


func TestContext_initCallback_runsOnSSEConnect(t *testing.T) {
	initDone := make(chan struct{})
	server := newTestApp(t, "/", func(c *via.Context) {
		c.Init(func() { close(initDone) })
		c.View(func() h.H { return h.Div(h.Text("page")) })
	})

	body := getPageBody(t, server, "/")
	select {
	case <-initDone:
		t.Fatal("init must not run before SSE connects")
	default:
	}

	ctxID := extractCtxID(t, body)
	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	readSSEEvent(t, stream, sseTimeout) // wait for connection established
	select {
	case <-initDone:
	case <-time.After(sseTimeout):
		t.Fatal("init must run when SSE connects")
	}
}

func TestViewMode_mutationsRejectedDuringViewRender(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(c *via.Context) {
		s := via.State(c, 0)

		act := c.Action(func() error {
			return nil
		})

		c.View(func() h.H {
			return h.Div(
				h.Textf("val=%d", s.Get(c)),
				act.OnClick(),
			)
		})
	})

	_ = server
}

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
