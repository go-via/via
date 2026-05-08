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

type unmarshalable struct{ Ch chan int }

func TestSetSignal_panicsOnNonJSONValue(t *testing.T) {
	t.Parallel()

	// Constructing a Signal with a chan field; channels never serialize
	// to JSON, so SetSignal must surface the programmer error as a panic
	// rather than silently dropping the assignment.
	var sig via.Signal[unmarshalable]
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "SetSignal must panic when the value type cannot be JSON-encoded")
		msg, ok := rec.(string)
		require.True(t, ok, "panic value should be a string, got %T", rec)
		assert.Contains(t, msg, "on.SetSignal:",
			"panic message should be prefixed with the package + function for grep-ability")
		assert.Contains(t, msg, "cannot be JSON-encoded",
			"panic message should explain the failure mode")
	}()
	on.SetSignal(&sig, unmarshalable{Ch: make(chan int)})
}

type eventCoveragePage struct{}

func (p *eventCoveragePage) Hit(ctx *via.Ctx) error { return nil }

func (p *eventCoveragePage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Input(on.Focus(p.Hit)),
		h.Input(on.Blur(p.Hit)),
		h.Div(on.DblClick(p.Hit)),
		h.Div(on.MouseEnter(p.Hit)),
		h.Div(on.MouseLeave(p.Hit)),
		h.Div(on.Load(p.Hit)),
		h.Div(on.Event("contextmenu", p.Hit)),
	)
}

func TestOn_NamedEventHelpersRenderExpectedTriggers(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[eventCoveragePage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")

	for _, want := range []string{
		"on:focus", "on:blur",
		"on:dblclick",
		"on:mouseenter", "on:mouseleave",
		"on:load",
		"on:contextmenu",
	} {
		assert.Contains(t, body, want,
			"each named helper / on.Event must emit on:<name> attribute")
	}
}

func getBody(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	resp, err := http.Get(server.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return string(buf)
}
