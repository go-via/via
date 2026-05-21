package on_test

import (
	"bytes"
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

func (p *setSignalPage) View(ctx *via.CtxR) h.H {
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

func (p *setSignalStringPage) View(ctx *via.CtxR) h.H {
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

func (p *modifierPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Form(on.Submit(p.Submit, on.Prevent())),
		h.Input(on.Input(p.Search, on.Debounce("200ms"))),
		h.Button(h.Text("Stop"), on.Click(p.Submit, on.Stop())),
		h.Input(on.Input(p.Search, on.Throttle("500ms"))),
	)
}

func TestOn_modifiersAppendToTrigger(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[modifierPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	cases := []struct {
		name, needle, why string
	}{
		{"debounce", "on:input.debounce.200ms", "Debounce should append .debounce.<dur>"},
		{"throttle", "on:input.throttle.500ms", "Throttle should append .throttle.<dur>"},
		{"prevent", "on:submit.prevent", "Prevent should append .prevent"},
		{"stop", "on:click.stop", "Stop should append .stop"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, body, c.needle, c.why)
		})
	}
}

type keyFilterPage struct{}

func (p *keyFilterPage) Send(ctx *via.Ctx) error { return nil }

func (p *keyFilterPage) View(ctx *via.CtxR) h.H {
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

func (p *eventCoveragePage) View(ctx *via.CtxR) h.H {
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

type internPage struct{}

func (p *internPage) Inc(ctx *via.Ctx) error { return nil }
func (p *internPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestClick_bareBindingRendersIdentically(t *testing.T) {
	t.Parallel()
	p := &internPage{}
	a := on.Click(p.Inc)
	b := on.Click(p.Inc)
	var bufA, bufB bytes.Buffer
	require.NoError(t, a.Render(&bufA))
	require.NoError(t, b.Render(&bufB))
	assert.Equal(t, bufA.String(), bufB.String())
	assert.Contains(t, bufA.String(), `on:click`)
	assert.Contains(t, bufA.String(), `/_action/Inc`)
}

func TestClick_bareBindingRenderIsAllocFree(t *testing.T) {
	// AllocsPerRun forbids t.Parallel.
	p := &internPage{}
	node := on.Click(p.Inc)
	var buf bytes.Buffer
	require.NoError(t, node.Render(&buf)) // warm any internal state
	allocs := testing.AllocsPerRun(50, func() {
		buf.Reset()
		_ = node.Render(&buf)
	})
	assert.Zero(t, allocs,
		"rendering a cached bare binding should write pre-escaped bytes without allocating")
}

func TestClick_optionedBindingRenderIsAllocFree(t *testing.T) {
	// AllocsPerRun forbids t.Parallel.
	p := &internPage{}
	node := on.Click(p.Inc, on.Debounce("200ms"))
	var buf bytes.Buffer
	require.NoError(t, node.Render(&buf)) // warm
	allocs := testing.AllocsPerRun(50, func() {
		buf.Reset()
		_ = node.Render(&buf)
	})
	assert.Zero(t, allocs,
		"rendering an optioned binding should write pre-escaped bytes without allocating")
}

func TestClick_bareBindingAllocatesAtMostOnceAfterFirstCall(t *testing.T) {
	// AllocsPerRun forbids t.Parallel; the runtime asserts on it.
	// Passing the bound method through `fn any` boxes the 2-word method
	// value — that's an unavoidable 1 alloc at the call boundary. The
	// intern cache must contribute zero on top of that.
	p := &internPage{}
	on.Click(p.Inc) // prime the intern cache
	allocs := testing.AllocsPerRun(50, func() {
		_ = on.Click(p.Inc)
	})
	assert.LessOrEqual(t, allocs, float64(1),
		"bare bindings should be interned — only the fn-to-any boxing may remain")
}

// TestClick_panicsOnAnonymousFunction guards the contract that
// every on.* helper accepts a bound *method value* — not an arbitrary
// closure. spec.MethodName parses the runtime "-fm" trampoline suffix
// to recover the method name; anonymous functions and top-level funcs
// have no such suffix, so spec.MethodName returns "". Previously the
// helpers swallowed this as a silently-dead binding (return nil);
// the helpers must instead panic so the programming error surfaces
// at the first render rather than as a button that does nothing.
func TestClick_panicsOnAnonymousFunction(t *testing.T) {
	t.Parallel()

	defer func() {
		rec := recover()
		require.NotNil(t, rec, "on.Click with a non-method must panic, not return nil")
		msg, ok := rec.(string)
		require.True(t, ok, "panic value should be a string, got %T", rec)
		assert.Contains(t, msg, "on:",
			"panic message should be package-prefixed for grep-ability")
		assert.Contains(t, msg, "bound method",
			"panic message should explain the required input shape")
	}()
	on.Click(func(ctx *via.Ctx) error { return nil })
}

func TestKey_panicsOnAnonymousFunction(t *testing.T) {
	t.Parallel()
	// on.Key drives the optioned render() path; cover that branch too.
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "on.Key with a non-method must panic")
		msg, ok := rec.(string)
		require.True(t, ok)
		assert.Contains(t, msg, "bound method")
	}()
	on.Key("Enter", func(ctx *via.Ctx) error { return nil })
}

func getBody(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	resp, err := http.Get(server.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return string(buf)
}
