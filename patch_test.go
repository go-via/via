package via_test

import (
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/require"
)

// ctx.Patch.* groups the low-level wire-push primitives behind a single
// sub-object so they don't crowd the top-level Ctx surface and read as
// what they are: emit-a-frame, not the first-reach for typed state.

type patchPage struct{}

func (p *patchPage) PushSignal(ctx *via.Ctx) error {
	ctx.Patch.Signal("_picoTheme", "purple")
	return nil
}

func (p *patchPage) PushSignalsBatch(ctx *via.Ctx) error {
	ctx.Patch.Signals(map[string]any{
		"_picoTheme": "amber",
		"_density":   3,
	})
	return nil
}

func (p *patchPage) PushElement(ctx *via.Ctx) error {
	ctx.Patch.Element(h.Div(h.ID("solo"), h.Text("only")))
	return nil
}

func (p *patchPage) PushElements(ctx *via.Ctx) error {
	ctx.Patch.Elements(
		h.Div(h.ID("a"), h.Text("first")),
		h.Div(h.ID("b"), h.Text("second")),
	)
	return nil
}

func (p *patchPage) EmptyGuards(ctx *via.Ctx) error {
	ctx.Patch.Signal("", "ignored")
	ctx.Patch.Signals(nil)
	ctx.Patch.Signals(map[string]any{})
	ctx.Patch.Element(nil)
	ctx.Patch.Elements()
	ctx.Patch.Elements(nil, nil)
	return nil
}

func (p *patchPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.ID("root"), h.P(h.Text("ready")))
}

func TestPatch_SignalEmitsWirePatchSignals(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[patchPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("PushSignal").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"_picoTheme":"purple"`)
}

func TestPatch_SignalsEmitsBatchedWirePatch(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[patchPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("PushSignalsBatch").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"_picoTheme":"amber"`, `"_density":3`)
}

func TestPatch_ElementEmitsSingleElementMorph(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[patchPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("PushElement").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<div id="solo">only</div>`)
}

func TestPatch_EmptyInputsAreNoOps(t *testing.T) {
	t.Parallel()
	// Empty key, nil/empty map, nil element, no args, all-nil-args: every
	// guard must be a silent no-op. A regression would surface as an
	// unexpected SSE frame within the wait window.
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[patchPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("EmptyGuards").Fire())

	select {
	case frame := <-frames:
		t.Fatalf("empty-input Patch calls must not emit a frame; got %q", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestPatch_ElementsEmitsVariadicElementMorphs(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[patchPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("PushElements").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second,
		`<div id="a">first</div>`, `<div id="b">second</div>`)
}
