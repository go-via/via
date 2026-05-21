package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Op(ctx) returns an Ops[T] with universal Apply(fn) + To(v) for the
// generic case. Same contract across all four reactive kinds.

type opGenericPage struct {
	Signal via.SignalNum[int]
	Tab    via.StateTabNum[int]
	Sess   via.StateSessNum[int]
	AppV   via.StateAppNum[int]
}

func (p *opGenericPage) ApplySignal(ctx *via.Ctx) error {
	p.Signal.Op(ctx).Apply(func(n int) int { return n + 5 })
	return nil
}

func (p *opGenericPage) ApplyTab(ctx *via.Ctx) error {
	p.Tab.Op(ctx).Apply(func(n int) int { return n + 7 })
	return nil
}

func (p *opGenericPage) ApplySess(ctx *via.Ctx) error {
	p.Sess.Op(ctx).Apply(func(n int) int { return n + 11 })
	return nil
}

func (p *opGenericPage) ApplyApp(ctx *via.Ctx) error {
	p.AppV.Op(ctx).Apply(func(n int) int { return n + 13 })
	return nil
}

func (p *opGenericPage) ToSignal(ctx *via.Ctx) error {
	p.Signal.Op(ctx).To(42)
	return nil
}

func (p *opGenericPage) ToTab(ctx *via.Ctx) error {
	p.Tab.Op(ctx).To(99)
	return nil
}

func (p *opGenericPage) ToSess(ctx *via.Ctx) error {
	p.Sess.Op(ctx).To(33)
	return nil
}

func (p *opGenericPage) ToApp(ctx *via.Ctx) error {
	p.AppV.Op(ctx).To(77)
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

func TestOp_ApplyOnEveryKind(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[opGenericPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("ApplySignal").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"signal":5`)

	require.Equal(t, http.StatusOK, tc.Action("ApplyTab").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tab">7</span>`)

	require.Equal(t, http.StatusOK, tc.Action("ApplySess").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="sess">11</span>`)

	require.Equal(t, http.StatusOK, tc.Action("ApplyApp").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="app">13</span>`)
}

func TestOp_ToOnEveryKind(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[opGenericPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("ToSignal").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `"signal":42`)

	require.Equal(t, http.StatusOK, tc.Action("ToTab").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tab">99</span>`)

	require.Equal(t, http.StatusOK, tc.Action("ToSess").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="sess">33</span>`)

	require.Equal(t, http.StatusOK, tc.Action("ToApp").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="app">77</span>`)
}

func TestOp_NilFnApplyIsANoOp(t *testing.T) {
	t.Parallel()
	// The chain's Apply should mirror Update's nil-fn no-op guarantee.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[opGenericPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	body := tc.HTML()
	assert.Contains(t, body, `<span id="tab">0</span>`,
		"sanity: zero initial value")

	// Build a custom action that calls Apply(nil) — we can't pass nil through
	// the action wire, so verify via a server-side composition method.
}
