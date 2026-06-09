package via_test

import (
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type syncPage struct{}

func (p *syncPage) PushList(ctx *via.Ctx) error {
	ctx.Patch().Elements(
		h.Ul(h.ID("results"),
			h.Li(h.Text("first")),
			h.Li(h.Text("second")),
		),
	)
	return nil
}

func (p *syncPage) PickTheme(ctx *via.Ctx) error {
	ctx.Patch().Signal("_picoTheme", "purple")
	return nil
}

func (p *syncPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.ID("root"), h.P(h.Text("ready")))
}

func TestSyncElements_pushesManualPatchOverSSE(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[syncPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("PushList").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `id="results"`, "first")
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
		{"Reload", func() { ctx.Reload() }},
		{"Toast", func() { ctx.Toast("hi") }},
		{"Redirect", func() { ctx.Redirect("/") }},
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

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[syncPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("PickTheme").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"_picoTheme":"purple"`)
}
