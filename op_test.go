package via_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Op(ctx) returns a typed chain entry on every reactive kind: each
// shape-specialized type (NumOps here) adds its typed verbs, which route
// through the handle's Update path. Apply was removed — custom transforms
// go through Update.

type opGenericPage struct {
	Signal via.SignalNum[int]
	Tab    via.StateTabNum[int]
	Sess   via.StateSessNum[int]
	AppV   via.StateAppNum[int]
}

func (p *opGenericPage) AddSignal(ctx *via.Ctx) error {
	p.Signal.Op(ctx).Add(5)
	return nil
}

func (p *opGenericPage) AddTab(ctx *via.Ctx) error {
	p.Tab.Op(ctx).Add(7)
	return nil
}

func (p *opGenericPage) AddSess(ctx *via.Ctx) error {
	p.Sess.Op(ctx).Add(11)
	return nil
}

func (p *opGenericPage) AddApp(ctx *via.Ctx) error {
	p.AppV.Op(ctx).Add(13)
	return nil
}

func (p *opGenericPage) ToSignal(ctx *via.Ctx) error {
	p.Signal.Write(ctx, 42)
	return nil
}

func (p *opGenericPage) ToTab(ctx *via.Ctx) error {
	p.Tab.Write(ctx, 99)
	return nil
}

func (p *opGenericPage) ToSess(ctx *via.Ctx) error {
	_ = p.Sess.Update(ctx, func(int) (int, error) { return 33, nil })
	return nil
}

func (p *opGenericPage) ToApp(ctx *via.Ctx) error {
	_ = p.AppV.Update(ctx, func(int) (int, error) { return 77, nil })
	return nil
}

// UpdateErrorRejectsWrite — fn returning a non-nil error must leave
// the value unchanged. Bumping Tab by 100 then trying to error out:
// final value should remain whatever Tab was before the failed call.
func (p *opGenericPage) BumpThenFail(ctx *via.Ctx) error {
	p.Tab.Op(ctx).Add(100)
	_ = p.Tab.Update(ctx, func(n int) (int, error) {
		return n + 1000, errors.New("rejected")
	})
	return nil
}

func (p *opGenericPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Span(h.ID("sig"), h.Textf("%d", p.Signal.Read(ctx))),
		h.Span(h.ID("tab"), h.Textf("%d", p.Tab.Read(ctx))),
		h.Span(h.ID("sess"), h.Textf("%d", p.Sess.Read(ctx))),
		h.Span(h.ID("app"), h.Textf("%d", p.AppV.Read(ctx))),
	)
}

func TestOp_TypedAddOnEveryKind(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[opGenericPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("AddSignal").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"signal":5`)

	require.Equal(t, http.StatusOK, tc.Action("AddTab").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tab">7</span>`)

	require.Equal(t, http.StatusOK, tc.Action("AddSess").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="sess">11</span>`)

	require.Equal(t, http.StatusOK, tc.Action("AddApp").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="app">13</span>`)
}

func TestOp_ToOnEveryKind(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[opGenericPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("ToSignal").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"signal":42`)

	require.Equal(t, http.StatusOK, tc.Action("ToTab").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tab">99</span>`)

	require.Equal(t, http.StatusOK, tc.Action("ToSess").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="sess">33</span>`)

	require.Equal(t, http.StatusOK, tc.Action("ToApp").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="app">77</span>`)
}

func TestOp_UpdateErrorRejectsTheWrite(t *testing.T) {
	t.Parallel()
	// Update's fn returning a non-nil error must leave the value
	// unchanged — the new value computed by fn is discarded.
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[opGenericPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("BumpThenFail").Fire())
	// Tab went 0 → 100 via Add(100); the failing Update tried to add
	// another 1000 but errored, so the value must remain 100.
	body := vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tab">`)
	assert.Contains(t, body, `<span id="tab">100</span>`,
		"failing Update must not commit its computed value")
}

func TestOp_panicsOnNilCtx(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		call func()
	}{
		{"SignalNum", func() { var s via.SignalNum[int]; _ = s.Op(nil) }},
		{"SignalBool", func() { var s via.SignalBool; _ = s.Op(nil) }},
		{"SignalStr", func() { var s via.SignalStr; _ = s.Op(nil) }},
		{"SignalSlice", func() { var s via.SignalSlice[int]; _ = s.Op(nil) }},
		{"SignalMap", func() { var s via.SignalMap[string, int]; _ = s.Op(nil) }},
		{"StateTabNum", func() { var s via.StateTabNum[int]; _ = s.Op(nil) }},
		{"StateSessNum", func() { var s via.StateSessNum[int]; _ = s.Op(nil) }},
		{"StateAppNum", func() { var s via.StateAppNum[int]; _ = s.Op(nil) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.PanicsWithValue(t,
				"via: Op called with nil *Ctx", c.call,
				"Op must panic eagerly so the stack points at the user's Op(nil) call site")
		})
	}
}
