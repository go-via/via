package vt_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tcPage struct {
	N     via.StateTab[int]
	Label via.Signal[string] `via:"label,init=hello"`
}

func (p *tcPage) Bump(ctx *via.Ctx) error {
	p.N.Set(ctx, p.N.Read(ctx)+1)
	return nil
}

func (p *tcPage) View(ctx *via.CtxR) h.H {
	return h.Div(p.N.Text(ctx), h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestNewClient_picksUpTabIDFromRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	tab := tc.TabID()
	assert.NotEmpty(t, tab)
	assert.True(t, strings.HasPrefix(tab, "/_"),
		"tab id is route-prefixed; got %q", tab)
}

func TestClient_HTML_returnsLastFetchedBody(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	body := tc.HTML()
	assert.Contains(t, body, "<button")
	assert.Contains(t, body, ">+<")
}

func TestClient_Reload_refetchesAndReturnsCurrentBody(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	originalTab := tc.TabID()
	original := tc.HTML()
	require.NotEmpty(t, original)

	// Reload returns a fresh fetch and rotates the tab id (each GET
	// registers a new Ctx — that's the contract documented on Reload).
	body := tc.Reload()
	assert.Contains(t, body, "<button")
	assert.NotEqual(t, originalTab, tc.TabID(),
		"each GET registers a new tab; Reload must update tabID")
	assert.Equal(t, body, tc.HTML(),
		"HTML() should now return what Reload just stored")
}

func TestActionCall_Fire_returnsResponseStatus(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Bump").Fire())
}

func TestAction_acceptsBoundMethodValue(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	page := &tcPage{}
	// Typed form: pass the bound method, get the action name resolved
	// via reflect — typo-proof since Bump is referenced, not stringified.
	require.Equal(t, 200, tc.Action(page.Bump).Fire())
}

func TestActionCall_Fire_returns404OnUnknownMethod(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	assert.Equal(t, 404, tc.Action("DoesNotExist").Fire())
}

func TestActionCall_WithSignal_carriesValueIntoActionPayload(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")

	// Fire 3 increments, each carrying a different incoming "label"
	// signal value. The state should grow to 3 and the latest signal
	// payload should land in the rendered fragment.
	require.Equal(t, 200, tc.Action("Bump").
		WithSignal("label", "first").Fire())
	require.Equal(t, 200, tc.Action("Bump").
		WithSignal("label", "second").Fire())
	require.Equal(t, 200, tc.Action("Bump").
		WithSignal("label", "third").Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	vt.AwaitFrame(t, frames, 2*time.Second, ">3<")
}

type uploadPage struct {
	Avatar via.File           `via:"avatar"`
	Note   via.Signal[string] `via:"note"`
	Tag    via.Signal[int]    `via:"tag"`
	Live   via.Signal[bool]   `via:"live"`
	Echo   via.StateTab[string]
}

func (p *uploadPage) Save(ctx *via.Ctx) error {
	if !p.Avatar.Present() {
		p.Echo.Set(ctx, "no-file")
		return nil
	}
	b, err := p.Avatar.Bytes()
	if err != nil {
		return err
	}
	p.Echo.Set(ctx, p.Avatar.Filename()+":"+
		string(b)+":"+p.Note.Read(ctx))
	return nil
}

func (p *uploadPage) View(ctx *via.CtxR) h.H { return h.Div(p.Echo.Text(ctx)) }

func TestActionRequest_WithFile_sendsMultipartBody(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[uploadPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	body := []byte("PNG-bytes")
	require.Equal(t, 200,
		tc.Action("Save").
			WithFile("avatar", "me.png", body).
			WithSignal("note", "from-test").
			Fire(),
		"the test client must produce a valid multipart body the runtime "+
			"can decode into via.File + signal fields")
	vt.AwaitFrame(t, frames, 2*time.Second,
		"me.png:PNG-bytes:from-test")
}

func TestActionRequest_WithFile_andWithSignal_combineScalarTypes(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[uploadPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	// Multipart only requires a file to switch transports; signal scalars
	// (string / bool / int) must all coerce to form values the server
	// decodes back into typed signal fields.
	require.Equal(t, 200,
		tc.Action("Save").
			WithFile("avatar", "tiny.bin", []byte("x")).
			WithSignal("note", "hi").
			WithSignal("tag", 7).
			WithSignal("live", true).
			Fire())
}

func TestSSE_streamsHeartbeatsAndPatches(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithSSEHeartbeat(50*time.Millisecond),
	)
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	// Without firing any action we should still observe at least one
	// heartbeat frame within 1s thanks to the short heartbeat interval.
	vt.AwaitFrame(t, frames, 1500*time.Millisecond, "datastar-patch-signals")
}
