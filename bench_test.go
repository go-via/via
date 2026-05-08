package via_test

import (
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	viatest "github.com/go-via/via/test"
)

type benchPage struct {
	Hits via.State[int]
	Step via.Signal[int] `via:"step,init=1"`
}

func (p *benchPage) Inc(ctx *via.Ctx) error {
	p.Hits.Set(ctx, p.Hits.Get(ctx)+p.Step.Get(ctx))
	return nil
}

func (p *benchPage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.P(p.Hits.Text()),
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
	for i := 0; i < b.N; i++ {
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

	tc := viatest.NewClient(b, server, "/")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if got := tc.Action("Inc").Fire(); got != 200 {
			b.Fatalf("status %d", got)
		}
	}
}
