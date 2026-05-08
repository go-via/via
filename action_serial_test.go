package via_test

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
)

type serialPage struct {
	N via.State[int]
}

// Bump is intentionally non-atomic on N.Get/N.Set so the only thing
// keeping a parallel race from corrupting it is the runtime's per-Ctx
// action serialization.
func (p *serialPage) Bump(ctx *via.Ctx) error {
	cur := p.N.Get(ctx)
	p.N.Set(ctx, cur+1)
	return nil
}

func (p *serialPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.N.Text(), h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestAction_concurrentPOSTsAreSerializedPerCtx(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[serialPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			tc.Action("Bump").Fire()
		}()
	}
	wg.Wait()

	// Fire one synchronous action that returns the final value through
	// the rendered fragment. We can't introspect ctx.N directly across
	// the wire; assert via SSE re-render.
	frames, cancel := tc.SSE(t)
	defer cancel()

	tc.Action("Bump").Fire() // N+1 increments by now

	// Look for "<div>51<…" or any number ≥ 51 — exact equality is hard
	// because frames may carry intermediate values, but the *final*
	// frame must show 51 because actions are serialized.
	var got string
	for f := range frames {
		got += f
		if containsFinalCount(got, 51) {
			return
		}
		if len(got) > 8192 {
			break
		}
	}
	assert.Contains(t, got, "<div>51",
		"after 51 serialized increments the final count must be 51")
}

func containsFinalCount(body string, want int) bool {
	const target = "<div>"
	rest := body
	for {
		i := strings.Index(rest, target)
		if i < 0 {
			return false
		}
		rest = rest[i+len(target):]
		n, j := 0, 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			n = n*10 + int(rest[j]-'0')
			j++
		}
		if j > 0 && n == want {
			return true
		}
	}
}
