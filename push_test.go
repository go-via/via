package via_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type syncPage struct{}

func (p *syncPage) PushList(ctx *via.Ctx) error {
	ctx.SyncElements(
		h.Ul(h.ID("results"),
			h.Li(h.Text("first")),
			h.Li(h.Text("second")),
		),
	)
	return nil
}

func (p *syncPage) Toast(ctx *via.Ctx) error {
	ctx.ExecScriptf("console.log(%q)", "hello world")
	return nil
}

func (p *syncPage) PickTheme(ctx *via.Ctx) error {
	ctx.PatchSignal("_picoTheme", "purple")
	return nil
}

func (p *syncPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.ID("root"), h.P(h.Text("ready")))
}

func TestSyncElements_pushesManualPatchOverSSE(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("PushList").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `id="results"`, "first")
}

func TestCtx_pushHelpersToleratesNilReceiver(t *testing.T) {
	t.Parallel()
	// Every push.go helper has `if ctx == nil { return }` as its first
	// line. A regression that dropped any one of those guards would
	// panic on a nil-pointer method call. None of these are realistic
	// user code, but the defensive guards are part of the contract.
	var ctx *via.Ctx
	cases := []struct {
		name string
		fn   func()
	}{
		{"ExecScript", func() { ctx.ExecScript("x") }},
		{"ExecScriptf", func() { ctx.ExecScriptf("x %d", 1) }},
		{"Reload", func() { ctx.Reload() }},
		{"Toast", func() { ctx.Toast("hi") }},
		{"Redirect", func() { ctx.Redirect("/") }},
		{"PatchSignals", func() { ctx.PatchSignals(map[string]any{"k": 1}) }},
		{"SyncElements", func() { ctx.SyncElements(h.Div()) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.NotPanics(t, c.fn)
		})
	}
}

func TestPatchSignal_pushesKeyedValueToClient(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("PickTheme").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `"_picoTheme":"purple"`)
}

func TestExecScriptf_formatsArgsBeforeQueueing(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Toast").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `console.log("hello world")`)
}
