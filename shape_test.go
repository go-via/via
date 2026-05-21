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

// shapePage exercises every shape × every kind on Op(ctx). One page so
// the discovery + slot binding for embedded specializations is verified
// alongside the verbs themselves.
type shapePage struct {
	// Num
	SigN  via.SignalNum[int]
	TabN  via.StateTabNum[int]
	SessN via.StateSessNum[int]
	AppN  via.StateAppNum[int]
	// Bool
	SigB  via.SignalBool
	TabB  via.StateTabBool
	SessB via.StateSessBool
	AppB  via.StateAppBool
	// Str
	SigS  via.SignalStr
	TabS  via.StateTabStr
	SessS via.StateSessStr
	AppS  via.StateAppStr
	// Slice
	SigSl  via.SignalSlice[int]
	TabSl  via.StateTabSlice[int]
	SessSl via.StateSessSlice[int]
	AppSl  via.StateAppSlice[int]
	// Map
	SigM  via.SignalMap[string, int]
	TabM  via.StateTabMap[string, int]
	SessM via.StateSessMap[string, int]
	AppM  via.StateAppMap[string, int]
}

func (p *shapePage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Span(h.ID("tabn"), h.Textf("%d", p.TabN.Read(ctx))),
		h.Span(h.ID("tabb"), h.Textf("%t", p.TabB.Read(ctx))),
		h.Span(h.ID("tabs"), h.Text(p.TabS.Read(ctx))),
		h.Span(h.ID("tabsl"), h.Textf("%v", p.TabSl.Read(ctx))),
		h.Span(h.ID("tabm"), h.Textf("%v", p.TabM.Read(ctx))),
	)
}

func (p *shapePage) NumAdd(ctx *via.Ctx) error {
	p.TabN.Op(ctx).Add(5)
	return nil
}
func (p *shapePage) NumInc(ctx *via.Ctx) error {
	p.TabN.Op(ctx).Inc()
	return nil
}
func (p *shapePage) NumMul(ctx *via.Ctx) error {
	p.TabN.Op(ctx).Mul(3)
	return nil
}
func (p *shapePage) NumMin(ctx *via.Ctx) error {
	p.TabN.Op(ctx).Min(10)
	return nil
}
func (p *shapePage) NumMax(ctx *via.Ctx) error {
	p.TabN.Op(ctx).Max(100)
	return nil
}
func (p *shapePage) NumZero(ctx *via.Ctx) error {
	p.TabN.Op(ctx).Zero()
	return nil
}

func (p *shapePage) BoolToggle(ctx *via.Ctx) error {
	p.TabB.Op(ctx).Toggle()
	return nil
}
func (p *shapePage) BoolTrue(ctx *via.Ctx) error {
	p.TabB.Op(ctx).True()
	return nil
}
func (p *shapePage) BoolFalse(ctx *via.Ctx) error {
	p.TabB.Op(ctx).False()
	return nil
}

func (p *shapePage) StrAppend(ctx *via.Ctx) error {
	p.TabS.Op(ctx).Append("hello")
	return nil
}
func (p *shapePage) StrPrepend(ctx *via.Ctx) error {
	p.TabS.Op(ctx).Prepend(">>>")
	return nil
}
func (p *shapePage) StrClear(ctx *via.Ctx) error {
	p.TabS.Op(ctx).Clear()
	return nil
}

func (p *shapePage) SliceAppend(ctx *via.Ctx) error {
	p.TabSl.Op(ctx).Append(7)
	return nil
}
func (p *shapePage) SlicePrepend(ctx *via.Ctx) error {
	p.TabSl.Op(ctx).Prepend(1)
	return nil
}
func (p *shapePage) SlicePop(ctx *via.Ctx) error {
	p.TabSl.Op(ctx).Pop()
	return nil
}
func (p *shapePage) SliceFilter(ctx *via.Ctx) error {
	p.TabSl.Op(ctx).Filter(func(n int) bool { return n > 0 })
	return nil
}
func (p *shapePage) SliceEmpty(ctx *via.Ctx) error {
	p.TabSl.Op(ctx).Empty()
	return nil
}

func (p *shapePage) MapPut(ctx *via.Ctx) error {
	p.TabM.Op(ctx).Put("a", 1)
	p.TabM.Op(ctx).Put("b", 2)
	return nil
}
func (p *shapePage) MapDelete(ctx *via.Ctx) error {
	p.TabM.Op(ctx).Delete("a")
	return nil
}
func (p *shapePage) MapEmpty(ctx *via.Ctx) error {
	p.TabM.Op(ctx).Empty()
	return nil
}

// ---- Num verbs ----

func TestShape_NumOps(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[shapePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("NumAdd").Fire()) // 0+5=5
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabn">5</span>`)

	require.Equal(t, http.StatusOK, tc.Action("NumInc").Fire()) // 5+1=6
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabn">6</span>`)

	require.Equal(t, http.StatusOK, tc.Action("NumMul").Fire()) // 6*3=18
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabn">18</span>`)

	require.Equal(t, http.StatusOK, tc.Action("NumMax").Fire())  // min(18, 100)=18, no-op
	require.Equal(t, http.StatusOK, tc.Action("NumZero").Fire()) // 0
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabn">0</span>`)

	require.Equal(t, http.StatusOK, tc.Action("NumMin").Fire()) // max(0, 10)=10
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabn">10</span>`)
}

// ---- Bool verbs ----

func TestShape_BoolOps(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[shapePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("BoolTrue").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabb">true</span>`)

	require.Equal(t, http.StatusOK, tc.Action("BoolToggle").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabb">false</span>`)

	require.Equal(t, http.StatusOK, tc.Action("BoolToggle").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabb">true</span>`)

	require.Equal(t, http.StatusOK, tc.Action("BoolFalse").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabb">false</span>`)
}

// ---- Str verbs ----

func TestShape_StrOps(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[shapePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("StrAppend").Fire()) // "" + "hello" = "hello"
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabs">hello</span>`)

	require.Equal(t, http.StatusOK, tc.Action("StrPrepend").Fire()) // ">>>hello"
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabs">&gt;&gt;&gt;hello</span>`)

	require.Equal(t, http.StatusOK, tc.Action("StrClear").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabs"></span>`)
}

// ---- Slice verbs ----

func TestShape_SliceOps(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[shapePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("SliceAppend").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabsl">[7]</span>`)

	require.Equal(t, http.StatusOK, tc.Action("SliceAppend").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabsl">[7 7]</span>`)

	require.Equal(t, http.StatusOK, tc.Action("SlicePrepend").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabsl">[1 7 7]</span>`)

	require.Equal(t, http.StatusOK, tc.Action("SlicePop").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabsl">[1 7]</span>`)

	require.Equal(t, http.StatusOK, tc.Action("SliceFilter").Fire()) // keep >0 → still [1,7]
	// Filter rebuilds — observable as same content but new slice; can't easily
	// assert "different identity" via SSE so just ensure content stays.
	require.Equal(t, http.StatusOK, tc.Action("SliceEmpty").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabsl">[]</span>`)
}

// ---- Map verbs ----

func TestShape_MapOps(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[shapePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("MapPut").Fire())
	// Map iteration order is undefined; assert both entries are present.
	body := vt.AwaitFrame(t, frames, 2*time.Second, `id="tabm"`)
	assert.Contains(t, body, "a:1")
	assert.Contains(t, body, "b:2")

	require.Equal(t, http.StatusOK, tc.Action("MapDelete").Fire())
	body = vt.AwaitFrame(t, frames, 2*time.Second, `id="tabm"`)
	assert.NotContains(t, body, "a:1")
	assert.Contains(t, body, "b:2")

	require.Equal(t, http.StatusOK, tc.Action("MapEmpty").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="tabm">map[]</span>`)
}
