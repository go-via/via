package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/spec"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type counterPage struct {
	Hits via.StateTab[int]
	Step via.Signal[int] `via:"step,init=1"`
}

func (c *counterPage) Inc(ctx *via.Ctx) error {
	c.Hits.Set(ctx, c.Hits.Get(ctx)+c.Step.Get(ctx))
	return nil
}

func (c *counterPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Button(h.Text("+"), on.Click(c.Inc)),
		c.Hits.Text(ctx),
	)
}

func TestAction_methodNameAppearsInOnClickPost(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[counterPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `@post(&#39;/_action/Inc&#39;)`,
		"on.Click(c.Inc) must render @post('/_action/Inc')")
}

func TestAction_unknownMethodReturns404(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[counterPage](app, "/")
	defer server.Close()

	resp, err := http.Post(server.URL+"/_action/Nope", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestMethodName_resolvesBoundMethod doubles as a Go-runtime canary:
// spec.MethodName recovers a method name by stripping the "-fm"
// trampoline suffix that the Go runtime emits for bound method values
// (e.g. "pkg.(*counterPage).Inc-fm"). The "-fm" suffix is a runtime
// internal, not a language contract — a Go release that changes the
// trampoline naming would silently break every `on.Click(c.Inc)` call
// site in via and downstream apps. If this test ever starts failing
// after a Go upgrade, fix MethodName before bumping the toolchain.
func TestMethodName_resolvesBoundMethod(t *testing.T) {
	t.Parallel()

	c := &counterPage{}
	assert.Equal(t, "Inc", spec.MethodName(c.Inc))
}

func TestMethodName_returnsEmptyForAnonymousFunction(t *testing.T) {
	t.Parallel()
	// Anonymous closures have no "-fm" suffix; MethodName returns "".
	// The on/* helpers turn that empty string into a panic so misuse
	// is loud — see TestClick_panicsOnAnonymousFunction.
	assert.Equal(t, "", spec.MethodName(func() {}))
}

func TestMethodName_returnsEmptyForTopLevelFunction(t *testing.T) {
	t.Parallel()
	// Package-level funcs (no receiver) have no "-fm" suffix either, so
	// MethodName must reject them just like anonymous closures.
	assert.Equal(t, "", spec.MethodName(topLevelHandler))
}

func TestMethodName_returnsEmptyForNil(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", spec.MethodName(nil))
}

func topLevelHandler(ctx *via.Ctx) error { return nil }

func TestMethodName_returnsSameStringForSameMethod(t *testing.T) {
	t.Parallel()

	// Two distinct *counterPage instances → same method PC → same
	// resolved name. Catches a regression in the PC-keyed cache where
	// e.g. caching by closure address (changes per instance) instead of
	// PC would silently re-parse.
	a := &counterPage{}
	b := &counterPage{}
	assert.Equal(t, spec.MethodName(a.Inc), spec.MethodName(b.Inc))
	assert.Equal(t, "Inc", spec.MethodName(b.Inc))
}

type erroringActionPage struct{}

func (p *erroringActionPage) Save(ctx *via.Ctx) error {
	return assertSaveErr("validation: email required")
}

func (p *erroringActionPage) View(ctx *via.CtxR) h.H { return h.Div() }

type assertSaveErr string

func (e assertSaveErr) Error() string { return string(e) }

func TestAction_defaultErrorPathAlertsTheBrowser(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[erroringActionPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	require.Equal(t, 200, tc.Action("Save").Fire())

	// The default action-error handler queues alert("…"). The script
	// arrives in the SSE stream as a datastar-execute-script event.
	vt.AwaitFrame(t, frames, 2*time.Second, `alert("validation: email required")`)
}

type customErrPage struct{}

func (p *customErrPage) Save(ctx *via.Ctx) error {
	return assertSaveErr("nope")
}

func (p *customErrPage) View(ctx *via.CtxR) h.H { return h.Div() }

type panicStringPage struct{}

func (p *panicStringPage) Crash(ctx *via.Ctx) error {
	panic("internal database connection string: secret-leaks-here")
}

func (p *panicStringPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestAction_defaultPanicAlertHidesInternalMessage(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[panicStringPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	require.Equal(t, 200, tc.Action("Crash").Fire())

	got := vt.AwaitFrame(t, frames, 2*time.Second, `alert("Something went wrong")`)
	assert.NotContains(t, got, "secret-leaks-here",
		"default panic alert must not leak the internal panic message")
}

type panicTypedErr struct {
	Code string
}

func (e *panicTypedErr) Error() string { return e.Code }

type panicTypedPage struct{}

func (p *panicTypedPage) Boom(ctx *via.Ctx) error {
	panic(&panicTypedErr{Code: "E_TYPED"})
}

func (p *panicTypedPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestAction_panicWithTypedErrorPreservesType(t *testing.T) {
	t.Parallel()

	var got error
	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithActionErrorHandler(func(ctx *via.Ctx, err error) {
			got = err
		}),
	)
	via.Mount[panicTypedPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Boom").Fire())

	require.NotNil(t, got)
	te, ok := got.(*panicTypedErr)
	require.True(t, ok, "panic with typed *panicTypedErr should be passed through to the handler verbatim, got %T", got)
	assert.Equal(t, "E_TYPED", te.Code)
}

func TestAction_WithActionErrorHandler_replacesDefaultAlert(t *testing.T) {
	t.Parallel()

	var seenErr atomic.Pointer[string]
	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithActionErrorHandler(func(ctx *via.Ctx, err error) {
			s := err.Error()
			seenErr.Store(&s)
		}),
	)
	via.Mount[customErrPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Save").Fire())

	got := seenErr.Load()
	require.NotNil(t, got, "WithActionErrorHandler should fire on errored action")
	assert.Equal(t, "nope", *got)
}

// Per-Ctx serialization

type serialPage struct {
	N via.StateTab[int]
}

// Bump is intentionally non-atomic on N.Get/N.Set so the only thing
// keeping a parallel race from corrupting it is the runtime's per-Ctx
// action serialization.
func (p *serialPage) Bump(ctx *via.Ctx) error {
	cur := p.N.Get(ctx)
	p.N.Set(ctx, cur+1)
	return nil
}

func (p *serialPage) View(ctx *via.CtxR) h.H {
	return h.Div(p.N.Text(ctx), h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestAction_concurrentPOSTsAreSerializedPerCtx(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[serialPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			tc.Action("Bump").Fire()
		}()
	}
	wg.Wait()

	frames, cancel := tc.SSE()
	defer cancel()

	tc.Action("Bump").Fire() // N+1 increments by now

	// After 51 serialized increments the rendered count must be 51 — if
	// the per-Ctx mutex were broken, parallel Get/Set would lose updates
	// and we'd see a number lower than 51.
	vt.AwaitFrame(t, frames, 5*time.Second, "<div>51")
}
