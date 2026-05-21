package via_test

import (
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
)

type benchPage struct {
	Hits via.StateTabNum[int]
	Step via.SignalNum[int] `via:"step,init=1"`
}

func (p *benchPage) Inc(ctx *via.Ctx) error {
	_ = p.Hits.Update(ctx, func(n int) (int, error) { return n + p.Step.Read(ctx), nil })
	return nil
}

func (p *benchPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.P(p.Hits.Text(ctx)),
		h.Button(h.Text("+"), on.Click(p.Inc)),
	)
}

// BenchmarkCounterRender measures per-page-render allocations on a typical
// composition: one State, one Signal, one action button.
func BenchmarkCounterRender(b *testing.B) {
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[benchPage](app, "/")
	defer server.Close()

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		resp, err := server.Client().Get(server.URL + "/")
		if err != nil {
			b.Fatal(err)
		}
		_, _ = resp.Body.Read(make([]byte, 1<<14))
		resp.Body.Close()
	}
}

// BenchmarkCounterAction measures per-action-POST allocations in the hot
// path. The bench fires Inc on a single tab repeatedly; allocations are
// dominated by reflect.Value boxing and JSON decode of the request body.
func BenchmarkCounterAction(b *testing.B) {
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[benchPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(b, server, "/")

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if got := tc.Action("Inc").Fire(); got != 200 {
			b.Fatalf("status %d", got)
		}
	}
}

type discardLogger struct{}

func (discardLogger) Log(via.LogLevel, string, ...any) {}

// BenchmarkCounterActionWithLogger establishes that installing a custom
// Logger is alloc-flat — neither the default-logger fallback nor the
// app.emit format-string path should add unbounded allocations per
// action. Pairs with BenchmarkCounterAction so a regression in one
// shows up against the other.
func BenchmarkCounterActionWithLogger(b *testing.B) {
	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithLogger(discardLogger{}),
		via.WithLogLevel(via.LogDebug), // exercise the full logger path
	)
	via.Mount[benchPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(b, server, "/")

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if got := tc.Action("Inc").Fire(); got != 200 {
			b.Fatalf("status %d", got)
		}
	}
}
