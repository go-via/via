package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startServer wraps an already-configured *via.App in an httptest.Server.
func startServer(t *testing.T, app *via.App) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(app.HTTPServeMux())
	t.Cleanup(server.Close)
	return server
}

func TestNew_returnsNonNil(t *testing.T) {
	v := via.New()
	assert.NotNil(t, v)
}

// TestNew_withTitle verifies WithTitle() sets the HTML <title> element.
// This guards against document title changes being silently ignored.
func TestNew_withTitle(t *testing.T) {
	app := via.New(via.WithTitle("My App"))
	app.Page("/", func(c *via.Context) {
		c.View(func() h.H { return h.Div(h.Text("hello")) })
	})
	server := startServer(t, app)
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "My App")
}

// TestNew_defaultTitle verifies the default document title is "Via".
func TestNew_defaultTitle(t *testing.T) {
	app := via.New()
	app.Page("/", func(c *via.Context) {
		c.View(func() h.H { return h.Div(h.Text("hello")) })
	})
	server := startServer(t, app)
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "Via")
}

// TestPage_rendersViewInDocument verifies the view function output appears in the HTTP response.
// This guards against page content being dropped from the rendered document.
func TestPage_rendersViewInDocument(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		c.View(func() h.H { return h.H1(h.Text("Hello Via!")) })
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "Hello Via!")
}

// TestPage_includesDatastarScript verifies the Datastar JS file reference is present in every page.
// This guards against accidentally breaking client-side reactivity by omitting the script.
func TestPage_includesDatastarScript(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		c.View(func() h.H { return h.Div() })
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "_datastar.js")
}

// TestPage_includesViaCtxSignal verifies every page includes a via-ctx data-signal initialization.
// This guards against SSE connections failing because the context ID is missing.
func TestPage_includesViaCtxSignal(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		c.View(func() h.H { return h.Div() })
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "via-ctx")
}

// TestPage_panicsOnNilView verifies registering a page with a nil view function panics.
// This guards against silent registration of broken pages that would fail at runtime.
func TestPage_panicsOnNilView(t *testing.T) {
	v := via.New()
	assert.Panics(t, func() {
		v.Page("/", func(c *via.Context) {
			c.View(nil)
		})
	})
}

// TestPage_withPathParam verifies path parameters are injected into the context and accessible in the view.
// This guards against dynamic routes silently ignoring URL parameters.
func TestPage_withPathParam(t *testing.T) {
	server := newTestApp(t, "/users/{id}", func(c *via.Context) {
		c.View(func() h.H {
			id := c.GetPathParam("id")
			return h.Div(h.Textf("user-%s", id))
		})
	})
	body := getPageBody(t, server, "/users/42")
	assert.Contains(t, body, "user-42")
}

// TestAppendToHead_addsElement verifies AppendToHead() injects elements into the document <head>.
// This guards against plugin head elements being silently dropped.
func TestAppendToHead_addsElement(t *testing.T) {
	v := via.New()
	v.AppendToHead(h.Link(h.Rel("stylesheet"), h.Href("/app.css")))
	v.Page("/", func(c *via.Context) {
		c.View(func() h.H { return h.Div() })
	})
	server := startServer(t, v)
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "/app.css")
}

// TestAppendToFoot_addsElement verifies AppendToFoot() injects elements at the end of <body>.
// This guards against footer scripts being silently dropped.
func TestAppendToFoot_addsElement(t *testing.T) {
	v := via.New()
	v.AppendToFoot(h.Script(h.Src("/foot.js")))
	v.Page("/", func(c *via.Context) {
		c.View(func() h.H { return h.Div() })
	})
	server := startServer(t, v)
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "/foot.js")
}

// TestDatastarJS_served verifies the embedded Datastar JS is served at /_datastar.js.
// This guards against accidentally breaking client-side reactivity by embedding stale/broken JS.
func TestDatastarJS_served(t *testing.T) {
	v := via.New()
	server := startServer(t, v)
	resp, err := http.Get(server.URL + "/_datastar.js")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
