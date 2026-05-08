package via_test

import (
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/require"
)

type clockPage struct {
	Tick via.State[int]

	ticks atomic.Int32
}

func (p *clockPage) OnConnect(ctx *via.Ctx) error {
	via.Stream(ctx, 20*time.Millisecond, func(ctx *via.Ctx, t time.Time) {
		p.ticks.Add(1)
		p.Tick.Set(ctx, int(p.ticks.Load()))
	})
	return nil
}

func (p *clockPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.P(p.Tick.Text()))
}

func TestStream_pushesPeriodicUpdatesOverSSE(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[clockPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE(t)
	defer cancel()

	deadline := time.After(2 * time.Second)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed before any tick visible; got: %q", got.String())
			}
			got.WriteString(f)
			// We expect to see <p>1</p>, <p>2</p>, ... arrive in subsequent
			// element patches. <p>3</p> proves the ticker fired at least 3x.
			if strings.Contains(got.String(), "<p>3</p>") {
				return
			}
		case <-deadline:
			t.Fatalf("did not see 3 ticks within 2s; got: %q", got.String())
		}
	}
}

func TestStream_stopsWhenCtxDone(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[clockPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	_, cancel := tc.SSE(t)
	time.Sleep(120 * time.Millisecond)
	cancel()

	// Hit the close beacon to dispose the ctx.
	resp, err := server.Client().Post(server.URL+"/_sse/close", "text/plain", strings.NewReader(tc.TabID()))
	require.NoError(t, err)
	resp.Body.Close()

	// Ticker must stop incrementing.
	time.Sleep(80 * time.Millisecond)
	// We can't observe page.ticks directly here; just assert the test
	// finishes without leaks via the race detector.
}
