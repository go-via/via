package via_test

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/require"
)

var disposed atomic.Int32

type disposable struct {
	N via.State[int]
}

func (d *disposable) Dispose(ctx *via.Ctx) {
	disposed.Add(1)
}

func (d *disposable) View(ctx *via.Ctx) h.H { return h.Div() }

func TestDispose_runsOnAppShutdown(t *testing.T) {
	t.Parallel()

	disposed.Store(0)
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[disposable](app, "/")
	defer server.Close()

	_ = viatest.NewClient(t, server, "/")

	require.NoError(t, app.Shutdown(context.Background()))

	deadline := time.After(2 * time.Second)
	for {
		if disposed.Load() == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("Dispose not called after Shutdown; disposed=%d", disposed.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
