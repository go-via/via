package via_test

import (
	"net/http"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
)

// List[E] is server-authoritative slice state with an Append one-liner — the
// chat log. Append/Get work without a live island for the unit (Set ignores ctx).
func TestList_appendAddsElementsInOrder(t *testing.T) {
	t.Parallel()
	var l via.List[string]
	l.Append(nil, "a")
	l.Append(nil, "b")
	assert.Equal(t, []string{"a", "b"}, l.Get())
}

// statelessState reads server State from a composition that never opts into a
// live island (it has no OnConnect).
type statelessState struct{ v via.State[int] }

func (s *statelessState) View() h.H { return h.Div(s.v.Display()) }

// State is server-only: reading it on a stateless request/response page is a
// programming error, guarded by a render-time panic. Observed black-box, that
// misuse must abort the render — the page does not serve — rather than silently
// hand back a value the server never meant to expose on a stateless page.
func TestState_isUnreadableOnAStatelessPage(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(statelessState{}))
	status, _ := app.Get("/")
	assert.NotEqual(t, http.StatusOK, status,
		"reading State on a non-island page must abort the render, not serve a page")
}

// stateEcho is a live island whose action writes a user-influenced value into
// server State, which is then re-rendered and pushed over the connection's SSE.
type stateEcho struct{ msg via.State[string] }

func (e *stateEcho) OnConnect(ctx *via.Ctx) error { return nil }
func (e *stateEcho) Set(ctx *via.Ctx)             { e.msg.Set(ctx, "<b>Ada</b>") }
func (e *stateEcho) View() h.H {
	return h.Div(
		h.P(h.Str("msg: "), e.msg.Display()),
		h.Button(via.OnClick(e.Set), h.Str("set")),
	)
}

// On a live island, State renders its current value into the pushed frame. And
// because server state routinely carries user-influenced data (a name, a chat
// message), the value is HTML-escaped on render — a raw value must not break
// out into markup. Asserted against the real SSE frame: the escaped form is
// present and the raw form is absent.
func TestState_rendersEscapedValueOnALiveIsland(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(stateEcho{}))
	conn := app.Connect()

	status, _ := app.Action(0).Tab(conn.TabID()).Fire()
	assert.Equal(t, http.StatusNoContent, status, "a live action acks 204; the render ships over the SSE")

	frame := conn.Await("&lt;b&gt;Ada&lt;/b&gt;") // the escaped value reaches the client
	assert.Contains(t, frame, "msg: ", "the frame must re-render the island's State")
	assert.NotContains(t, frame, "<b>Ada", "the raw value must not survive into markup")
}
