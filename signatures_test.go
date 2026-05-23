package via_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type voidActionPage struct {
	N via.StateTabNum[int]
}

// Bump returns nothing — actions don't have to surface errors when
// the body can't fail meaningfully.
func (p *voidActionPage) Bump(ctx *via.Ctx) {
	_ = p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil })
}

func (p *voidActionPage) View(ctx *via.CtxR) h.H {
	return h.Div(p.N.Text(ctx), h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestAction_voidReturnIsRecognised(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[voidActionPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("Bump").Fire())
	require.Equal(t, 200, tc.Action("Bump").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "<div>2")
}

type onlyVoidPage struct {
	N via.StateTabNum[int]
}

func (p *onlyVoidPage) Bump(ctx *via.Ctx) {
	_ = p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil })
}

func (p *onlyVoidPage) View(ctx *via.CtxR) h.H {
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

type ctxRActionPage struct {
	N via.StateTabNum[int]
}

func (p *ctxRActionPage) View(ctx *via.CtxR) h.H { return h.Div() }
func (p *ctxRActionPage) Bump(ctx *via.CtxR) error {
	return nil
}

func TestAction_panicsWhenHandlerTakesCtxR(t *testing.T) {
	t.Parallel()
	// A method named like an action that types *via.CtxR is always a
	// user typo — the read-only ctx has no Set/Update, so the handler
	// can't mutate state. Silently dropping the method (the old
	// behavior) makes the "why doesn't my action fire?" question
	// invisible. Mount must surface it with a precise panic.
	app := via.New()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Mount with action(ctx *via.CtxR) must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "Bump",
			"the panic must name the offending method")
		assert.Contains(t, msg, "via.CtxR",
			"the panic must point at the wrong ctx type")
		assert.Contains(t, msg, "via.Ctx",
			"the panic must name the required ctx type")
	}()
	via.Mount[ctxRActionPage](app, "/")
}
