package on_test

import (
	"bytes"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type setSignalPage struct {
	Step via.SignalNum[int] `via:"step,init=1"`
}

func (p *setSignalPage) Apply(ctx *via.Ctx) error { return nil }

func (p *setSignalPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Button(
			h.Text("Set step to 5"),
			on.Click(p.Apply, on.SetSignal(&p.Step.Signal, 5)),
		),
	)
}

func TestSetSignal_writesAssignmentBeforePost(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[setSignalPage](app, "/")

	body := getBody(t, server, "/")
	assert.Contains(t, body, `$step=5;@post(&#39;/_action/Apply&#39;)`,
		"on.SetSignal should prepend the assignment, joined by ;")
}

type setSignalStringPage struct {
	Theme via.SignalStr `via:"theme,init=blue"`
}

func (p *setSignalStringPage) Pick(ctx *via.Ctx) error { return nil }

func (p *setSignalStringPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Button(
			h.Text("Use red"),
			on.Click(p.Pick, on.SetSignal(&p.Theme.Signal, "red")),
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

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[modifierPage](app, "/")

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

type triggerOptsPage struct {
	Saving via.SignalBool `via:"saving"`
}

func (p *triggerOptsPage) Act(ctx *via.Ctx) error { return nil }

func (p *triggerOptsPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Button(h.Text("once"), on.Click(p.Act, on.Once())),
		h.Button(h.Text("outside"), on.Click(p.Act, on.Outside())),
		h.Button(h.Text("window"), on.Click(p.Act, on.Window())),
		h.Button(h.Text("del"), on.Click(p.Act, on.Confirm("Delete?"))),
		h.Button(h.Text("save"), on.Click(p.Act), on.Indicator(&p.Saving.Signal)),
	)
}

func TestOn_lifecycleModifiersAppendToTrigger(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[triggerOptsPage](app, "/")

	body := getBody(t, server, "/")
	cases := []struct {
		name, needle, why string
	}{
		{"once", "on:click.once", "Once should append .once"},
		{"outside", "on:click.outside", "Outside should append .outside"},
		{"window", "on:click.window", "Window should append .window"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, body, c.needle, c.why)
		})
	}
}

func TestConfirm_gatesPostBehindBrowserConfirm(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[triggerOptsPage](app, "/")

	body := getBody(t, server, "/")
	assert.Contains(t, body, `confirm(&#34;Delete?&#34;)&amp;&amp;@post(&#39;/_action/Act&#39;)`,
		"Confirm should short-circuit the @post behind a JSON-encoded confirm() guard")
}

func TestIndicator_bindsRequestInFlightSignalByKey(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[triggerOptsPage](app, "/")

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-indicator="saving"`,
		"Indicator should emit data-indicator with the signal's wire key")
}

type confirmWithPrePage struct {
	Step via.SignalNum[int] `via:"step,init=1"`
}

func (p *confirmWithPrePage) Apply(ctx *via.Ctx) error { return nil }

func (p *confirmWithPrePage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Button(
			h.Text("Apply"),
			on.Click(p.Apply, on.SetSignal(&p.Step.Signal, 5), on.Confirm("Sure?")),
		),
	)
}

// TestConfirm_composesWithPreStatements locks the sequencing when a Pre
// statement (on.SetSignal) and on.Confirm are present on the same trigger:
// the Pre assignment must run first (terminated by ';'), then the confirm()
// guard short-circuits the @post via '&&'. The combined expression is a
// valid JS statement sequence: `$step=5;confirm("Sure?")&&@post(...)`.
func TestConfirm_composesWithPreStatements(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[confirmWithPrePage](app, "/")

	body := getBody(t, server, "/")
	assert.Contains(t, body,
		`$step=5;confirm(&#34;Sure?&#34;)&amp;&amp;@post(&#39;/_action/Apply&#39;)`,
		"Pre assignment must run first (;), then confirm() must gate @post (&&)")
}

type keyFilterPage struct{}

func (p *keyFilterPage) Send(ctx *via.Ctx) error { return nil }

func (p *keyFilterPage) View(ctx *via.CtxR) h.H {
	return h.Div(on.Key("Enter", p.Send))
}

func TestOn_KeyFiltersByEvtKeyExpressionNotModifier(t *testing.T) {
	t.Parallel()
	// Datastar v1 has NO keyboard-key modifier — `on:keydown.Enter` would fire
	// on EVERY keystroke. Key filtering must be an expression guard on evt.key,
	// which the on plugin exposes via argNames:["evt"].
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[keyFilterPage](app, "/")

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-on:keydown="evt.key===&#39;Enter&#39;&amp;&amp;@post(&#39;/_action/Send&#39;)"`,
		"on.Key must guard the @post with evt.key===<Key>, not a (no-op) .Enter modifier")
	assert.NotContains(t, body, "on:keydown.Enter",
		"the .Enter attribute modifier is a no-op in datastar and must not be emitted")
}

func TestSetSignal_quotesStringValues(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[setSignalStringPage](app, "/")

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
		h.Input(on.Change(p.Hit)),
		h.Div(on.DblClick(p.Hit)),
		h.Div(on.MouseEnter(p.Hit)),
		h.Div(on.MouseLeave(p.Hit)),
		h.Div(on.Load(p.Hit)),
		h.Div(on.Event("contextmenu", p.Hit)),
	)
}

func TestOn_NamedEventHelpersRenderExpectedTriggers(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[eventCoveragePage](app, "/")

	body := getBody(t, server, "/")

	for _, want := range []string{
		"on:focus", "on:blur",
		"on:change",
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

func TestClick_bareBindingRenderIsAllocFree(t *testing.T) { //nolint:paralleltest // AllocsPerRun must run serially
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

func TestClick_optionedBindingRenderIsAllocFree(t *testing.T) { //nolint:paralleltest // AllocsPerRun must run serially
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

func TestClick_bareBindingAllocatesAtMostOnceAfterFirstCall(t *testing.T) { //nolint:paralleltest // AllocsPerRun must run serially
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

func TestClick_panicMessageNamesNil(t *testing.T) {
	t.Parallel()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "on.Click(nil) must panic")
		msg, ok := rec.(string)
		require.True(t, ok)
		assert.Contains(t, msg, "got nil",
			"panic should specifically call out nil rather than a generic 'closure/top-level/nil' clause")
	}()
	// Bare on.Click(nil) no longer compiles (F can't be inferred); a
	// typed nil func value is the remaining way to smuggle nil in.
	on.Click[func(*via.Ctx)](nil)
}

func topLevelClickHandler(ctx *via.Ctx) error { return nil }

func TestClick_panicMessageNamesTopLevelFunction(t *testing.T) {
	t.Parallel()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "on.Click(topLevelFn) must panic")
		msg, ok := rec.(string)
		require.True(t, ok)
		assert.Contains(t, msg, "top-level function",
			"panic should specifically identify the top-level function case")
	}()
	on.Click(topLevelClickHandler)
}

func TestClick_panicMessageNamesClosure(t *testing.T) {
	t.Parallel()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "on.Click(closure) must panic")
		msg, ok := rec.(string)
		require.True(t, ok)
		assert.Contains(t, msg, "closure",
			"panic should specifically identify the closure case")
	}()
	on.Click(func(ctx *via.Ctx) error { return nil })
}

func functionalTopLevelHandler(ctx *via.Ctx) error { return nil }

func TestClick_panicMessageNamesTopLevelFunctionWhoseNameStartsWithFunc(t *testing.T) {
	t.Parallel()
	// Guards against a too-loose `.func` substring heuristic: a top-level
	// function named e.g. `functionalTopLevelHandler` has runtime name
	// `pkg.functionalTopLevelHandler`, which contains the substring
	// `.func` — but the Go runtime only assigns `.funcN` (digit-suffixed)
	// to anonymous closures.
	defer func() {
		rec := recover()
		require.NotNil(t, rec)
		msg, ok := rec.(string)
		require.True(t, ok)
		assert.Contains(t, msg, "top-level function",
			"top-level fn whose identifier starts with 'func' must not be misclassified as a closure")
		assert.NotContains(t, msg, "got a closure")
	}()
	on.Click(functionalTopLevelHandler)
}

func TestKey_panicMessageNamesClosure(t *testing.T) {
	t.Parallel()
	// on.Key drives the optioned render() path; the classification must
	// also reach that branch, not only the event() fast path.
	defer func() {
		rec := recover()
		require.NotNil(t, rec)
		msg, ok := rec.(string)
		require.True(t, ok)
		assert.Contains(t, msg, "closure")
	}()
	on.Key("Enter", func(ctx *via.Ctx) error { return nil })
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
	resp, err := server.Client().Get(server.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return string(buf)
}
