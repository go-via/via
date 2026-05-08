package via_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
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
	frames, cancel := tc.SSE(t)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("PushList").Fire())

	deadline := time.After(2 * time.Second)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed early; got %q", got.String())
			}
			got.WriteString(f)
			if strings.Contains(got.String(), `id="results"`) &&
				strings.Contains(got.String(), "first") {
				return
			}
		case <-deadline:
			t.Fatalf("expected manual patch with id=results; got %q", got.String())
		}
	}
}

func TestPatchSignal_pushesKeyedValueToClient(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE(t)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("PickTheme").Fire())

	deadline := time.After(2 * time.Second)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed early; got %q", got.String())
			}
			got.WriteString(f)
			if strings.Contains(got.String(), `"_picoTheme":"purple"`) {
				return
			}
		case <-deadline:
			t.Fatalf("expected _picoTheme=purple patch; got %q", got.String())
		}
	}
}

func TestExecScriptf_formatsArgsBeforeQueueing(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE(t)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Toast").Fire())

	deadline := time.After(2 * time.Second)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed early; got %q", got.String())
			}
			got.WriteString(f)
			if strings.Contains(got.String(), `console.log("hello world")`) {
				return
			}
		case <-deadline:
			t.Fatalf("expected formatted script; got %q", got.String())
		}
	}
}
