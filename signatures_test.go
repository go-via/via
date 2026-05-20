package via_test

import (
	"net/http/httptest"
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
	N via.StateTab[int]
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
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Bump").Fire())
	require.Equal(t, 200, tc.Action("Bump").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, "<div>2")
}

type onlyVoidPage struct {
	N via.StateTab[int]
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
