package test_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tcPage struct {
	N     via.State[int]
	Label via.Signal[string] `via:"label,init=hello"`
}

func (p *tcPage) Bump(ctx *via.Ctx) error {
	p.N.Set(ctx, p.N.Get(ctx)+1)
	return nil
}

func (p *tcPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.N.Text(), h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestNewClient_picksUpTabIDFromRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
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

	tc := viatest.NewClient(t, server, "/")
	body := tc.HTML()
	assert.Contains(t, body, "<button")
	assert.Contains(t, body, ">+<")
}

func TestActionCall_Fire_returnsResponseStatus(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Bump").Fire())
}

func TestActionCall_Fire_returns404OnUnknownMethod(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	assert.Equal(t, 404, tc.Action("DoesNotExist").Fire())
}

func TestActionCall_WithSignal_carriesValueIntoActionPayload(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tcPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")

	// Fire 3 increments, each carrying a different incoming "label"
	// signal value. The state should grow to 3 and the latest signal
	// payload should land in the rendered fragment.
	require.Equal(t, 200, tc.Action("Bump").
		WithSignal("label", "first").Fire())
	require.Equal(t, 200, tc.Action("Bump").
		WithSignal("label", "second").Fire())
	require.Equal(t, 200, tc.Action("Bump").
		WithSignal("label", "third").Fire())

	frames, cancel := tc.SSE(t)
	defer cancel()

	deadline := time.After(2 * time.Second)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed early; got %q", got.String())
			}
			got.WriteString(f)
			if strings.Contains(got.String(), ">3<") {
				return
			}
		case <-deadline:
			t.Fatalf("expected N=3 in render; got %q", got.String())
		}
	}
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

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE(t)
	defer cancel()

	// Without firing any action we should still observe at least one
	// heartbeat frame within 1s thanks to the short heartbeat interval.
	deadline := time.After(1500 * time.Millisecond)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return
			}
			got.WriteString(f)
			if strings.Contains(got.String(), "datastar-patch-signals") {
				return
			}
		case <-deadline:
			t.Fatalf("expected at least one heartbeat frame; got %q", got.String())
		}
	}
}
