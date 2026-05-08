package on_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type setSignalPage struct {
	Step via.Signal[int] `via:"step,init=1"`
}

func (p *setSignalPage) Apply(ctx *via.Ctx) error { return nil }

func (p *setSignalPage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Button(
			h.Text("Set step to 5"),
			on.Click(p.Apply, on.SetSignal(&p.Step, 5)),
		),
	)
}

func TestSetSignal_writesAssignmentBeforePost(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[setSignalPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `$step=5;@post(&#39;/_action/Apply&#39;)`,
		"on.SetSignal should prepend the assignment, joined by ;")
}

type setSignalStringPage struct {
	Theme via.Signal[string] `via:"theme,init=blue"`
}

func (p *setSignalStringPage) Pick(ctx *via.Ctx) error { return nil }

func (p *setSignalStringPage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Button(
			h.Text("Use red"),
			on.Click(p.Pick, on.SetSignal(&p.Theme, "red")),
		),
	)
}

type modifierPage struct{}

func (p *modifierPage) Submit(ctx *via.Ctx) error { return nil }
func (p *modifierPage) Search(ctx *via.Ctx) error { return nil }

func (p *modifierPage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Form(on.Submit(p.Submit, on.Prevent())),
		h.Input(on.Input(p.Search, on.Debounce("200ms"))),
		h.Button(h.Text("Stop"), on.Click(p.Submit, on.Stop())),
		h.Input(on.Input(p.Search, on.Throttle("500ms"))),
	)
}

func TestOn_DebounceModifierAppendsToTrigger(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[modifierPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, "on:input.debounce.200ms",
		"Debounce should append .debounce.<dur> to the trigger spec")
}

func TestOn_ThrottleModifierAppendsToTrigger(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[modifierPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, "on:input.throttle.500ms",
		"Throttle should append .throttle.<dur> to the trigger spec")
}

func TestOn_PreventModifierAppendsToTrigger(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[modifierPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, "on:submit.prevent",
		"Prevent should append .prevent to the trigger spec")
}

func TestOn_StopModifierAppendsToTrigger(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[modifierPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, "on:click.stop",
		"Stop should append .stop to the trigger spec")
}

type keyFilterPage struct{}

func (p *keyFilterPage) Send(ctx *via.Ctx) error { return nil }

func (p *keyFilterPage) View(ctx *via.Ctx) h.H {
	return h.Div(on.Key("Enter", p.Send))
}

func TestOn_KeyAttributeIncludesNamedKey(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[keyFilterPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, "on:keydown.Enter",
		"on.Key should append .<key> to the keydown trigger")
}

func TestSetSignal_quotesStringValues(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[setSignalStringPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	// JSON-encoded string is quoted; HTML-escaped quotes become &#34;.
	assert.Contains(t, body, `$theme=&#34;red&#34;`)
}

func getBody(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	resp, err := http.Get(server.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return string(buf)
}
