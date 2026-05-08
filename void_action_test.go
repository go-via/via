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

type voidActionPage struct {
	N via.State[int]
}

// Bump returns nothing — actions don't have to surface errors when
// the body can't fail meaningfully.
func (p *voidActionPage) Bump(ctx *via.Ctx) {
	p.N.Update(ctx, func(n int) int { return n + 1 })
}

func (p *voidActionPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.N.Text(), h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestAction_voidReturnIsRecognised(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[voidActionPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE(t)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Bump").Fire())
	require.Equal(t, 200, tc.Action("Bump").Fire())

	deadline := time.After(2 * time.Second)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed early; got %q", got.String())
			}
			got.WriteString(f)
			if strings.Contains(got.String(), "<div>2") {
				return
			}
		case <-deadline:
			t.Fatalf("expected void-action to bump state to 2; got %q", got.String())
		}
	}
}

type onlyVoidPage struct {
	N via.State[int]
}

func (p *onlyVoidPage) Bump(ctx *via.Ctx) {
	p.N.Update(ctx, func(n int) int { return n + 1 })
}

func (p *onlyVoidPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestAction_voidReturnRendersAtPostURL(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[onlyVoidPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `@post(&#39;/_action/Bump&#39;)`,
		"void-return action should still wire on.Click → @post('/_action/Bump')")
}
