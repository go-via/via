package via_test

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

type statePage struct {
	Hits via.State[int]
}

func (p *statePage) Inc(ctx *via.Ctx) error {
	p.Hits.Set(ctx, p.Hits.Get(ctx)+1)
	return nil
}

func (p *statePage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Button(h.Text("+"), on.Click(p.Inc)),
		h.P(p.Hits.Text()),
	)
}

func TestState_initialZeroValueAppearsInRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[statePage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, "<p>0</p>",
		"State[int] zero value renders inside view fragment")
}

func TestState_actionMutatesStateForCurrentTab(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[statePage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")

	// Open SSE first so flushed patches land in the stream.
	frames, cancel := tc.SSE(t)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Inc").Fire())
	require.Equal(t, 200, tc.Action("Inc").Fire())
	require.Equal(t, 200, tc.Action("Inc").Fire())

	// We expect at least one element patch with "<p>3</p>".
	deadline := time.After(2 * time.Second)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed before reaching <p>3</p>; got %q", got.String())
			}
			got.WriteString(f)
			if strings.Contains(got.String(), "<p>3</p>") {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for <p>3</p> in SSE; got %q", got.String())
		}
	}
}
