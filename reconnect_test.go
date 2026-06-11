package via_test

import (
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
)

type reconnectPage struct{}

func (p *reconnectPage) View(ctx *via.CtxR) h.H { return h.Div(h.Text("hi")) }

// Dropping the SSE stream (a graceful-deploy clean close, or retries exhausting)
// leaves the tab silently frozen. The page must ship a reconnect manager that
// listens for Datastar's fetch lifecycle and, once retries fail, reloads to
// re-bootstrap a fresh stream + session — with a visible affordance.
func TestReconnect_scriptInjectedByDefault(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[reconnectPage](app, "/")

	html := vt.NewClient(t, server, "/").HTML()
	assert.Contains(t, html, "datastar-fetch",
		"the page must wire a datastar-fetch listener to observe SSE health")
	assert.Contains(t, html, "retries-failed",
		"it must react to Datastar's retries-failed (the stream is dead)")
	assert.Contains(t, strings.ToLower(html), "reload",
		"on retries-failed it must reload to re-bootstrap the stream")
	assert.Contains(t, html, "datastar-patch-signals",
		"it must clear on an incoming SSE patch — the only reliable reconnect signal")
	assert.Contains(t, html, "aria-live",
		"the reconnect banner must announce to assistive tech")
}

// The reconnect manager must also publish connection status as a
// data-via-connection attribute on <html>, so an app can style its own
// connection UI in CSS without via's built-in banner.
func TestReconnect_publishesConnectionStatus(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[reconnectPage](app, "/")

	html := vt.NewClient(t, server, "/").HTML()
	assert.Contains(t, html, "data-via-connection",
		"the page must publish connection status as a root attribute")
	assert.Contains(t, html, "offline",
		"the manager must mark the connection offline when retries fail")
	assert.Contains(t, html, "connecting",
		"the manager must mark the connection connecting while retrying")
}

// Apps that want to own reconnect behavior can opt out entirely.
func TestReconnect_optOutRemovesScript(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithoutSSEReconnect())
	server := vt.Serve(t, app)
	via.Mount[reconnectPage](app, "/")

	html := vt.NewClient(t, server, "/").HTML()
	assert.NotContains(t, html, "datastar-fetch",
		"WithoutSSEReconnect must remove the injected reconnect manager")
}
