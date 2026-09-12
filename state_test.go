package via_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// List[E] is server-authoritative slice state with an Append one-liner — the
// chat log. Append/Get work without a live island for the unit.
func TestList_appendAddsElementsInOrder(t *testing.T) {
	t.Parallel()
	var l via.List[string]
	l.Append("a")
	l.Append("b")
	assert.Equal(t, []string{"a", "b"}, l.Get())
}

// hookless holds server State and nothing else — no OnInit, no Tick, no
// Listen. Rendering the State is its only claim to a connection.
type hookless struct{ v via.State[int] }

func (s *hookless) View() h.H { return h.Div(h.Str("n="), s.v.Display()) }

// Rendering a State is what MAKES a unit live: a composition with no hook at
// all must still bootstrap its SSE stream and hold an open connection, because
// server-held state is meaningless without one.
func TestState_makesItsUnitLiveWithNoHook(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(hookless{}))

	status, body := app.Get("/")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `data-init="@post('/_via/sse')"`,
		"a page whose View renders State must bootstrap the live stream")

	c := app.Connect()
	defer c.Close()
	assert.NotEmpty(t, c.TabID(), "the connect must hand back a tab id, not 404")
}

// stateEcho is a live island whose action writes a user-influenced value into
// server State, which is then re-rendered and pushed over the connection's SSE.
type stateEcho struct{ msg via.State[string] }

func (e *stateEcho) Set(ctx *via.Ctx) { e.msg.Set("<b>Ada</b>") }
func (e *stateEcho) View() h.H {
	return h.Div(
		h.P(h.Str("msg: "), e.msg.Display()),
		h.Button(via.On("click", e.Set), h.Str("set")),
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

	status, _ := app.Action(0).Live(conn).Fire()
	assert.Equal(t, http.StatusNoContent, status, "a live action acks 204; the render ships over the SSE")

	frame := conn.Await("&lt;b&gt;Ada&lt;/b&gt;") // the escaped value reaches the client
	assert.Contains(t, frame, "msg: ", "the frame must re-render the island's State")
	assert.NotContains(t, frame, "<b>Ada", "the raw value must not survive into markup")
}

// TestState_bareSetAndAppend pins the bare-mutator contract for State and List:
// Set(v)/Append(v) take no ctx — the value is server-owned and reaches the
// browser on the next push, so there is nothing per-request to hand them.
// Fails if either mutator grows a required ctx again.
func TestState_bareSetAndAppend(t *testing.T) {
	t.Parallel()
	var s via.State[int]
	s.Set(7)
	assert.Equal(t, 7, s.Get())

	var l via.List[string]
	l.Append("a")
	l.Append("b")
	assert.Equal(t, []string{"a", "b"}, l.Get())
}

// Remove is the mirror of Append: it must leave the list in exactly the state
// the name promises, shifting the rest left.
func TestList_remove(t *testing.T) {
	t.Parallel()
	var l via.List[string]
	l.Append("a")
	l.Append("b")
	l.Append("c")

	l.Remove(1)
	assert.Equal(t, []string{"a", "c"}, l.Get(), "Remove shifts the rest left")
}

// A wrong index is a programming error. Remove panics rather than silently
// doing nothing, because a no-op would hide the bug behind a list that just
// never changes — the same reason State panics off-island.
func TestList_outOfRangeIndexPanics(t *testing.T) {
	t.Parallel()
	newList := func() *via.List[string] {
		var l via.List[string]
		l.Append("a")
		return &l
	}
	assert.Panics(t, func() { newList().Remove(1) }, "Remove past the end")
}

// Remove must not leave the removed element reachable through the backing
// array: the freed slot is zeroed, so a pointer row does not keep its target
// alive for as long as the island's connection lives.
func TestList_removeZeroesTheFreedSlot(t *testing.T) {
	t.Parallel()
	var l via.List[*string]
	a, b := "a", "b"
	l.Append(&a)
	l.Append(&b)
	full := l.Get()[:2]
	l.Remove(0)
	assert.Nil(t, full[1], "the vacated tail slot must be zeroed, not left aliasing the element")
}

// listIsland is the canonical case item 04 claimed was impossible: a live list
// a user can delete a row from. Each row carries a stable id so the morph
// matches by identity rather than by position.
type listIsland struct{ items via.List[string] }

func (t *listIsland) DropFirst(ctx *via.Ctx) { t.items.Remove(0) }
func (t *listIsland) View() h.H {
	return h.Div(
		t.items.Each(func(s string) h.H {
			return h.P(h.RawAttr("id", "row-"+s), h.Str(s))
		}),
		// A marker that changes with the length, so a test can await the
		// post-removal frame rather than matching the row it expects gone.
		h.P(h.RawAttr("id", "count-"+strconv.Itoa(len(t.items.Get())))),
		h.Button(via.On("click", t.DropFirst), h.Str("drop")),
	)
}

// Removal has to reach the browser, not just the server value: the patch the
// action pushes must no longer contain the dropped row. This is the half a
// unit test on Get() cannot see.
func TestList_removeReachesTheBrowser(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(listIsland{items: newListItems("drop", "keep")}))
	status, first := app.Get("/")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, first, "count-2", "both rows render on the first paint")
	assert.Contains(t, first, "row-drop")

	c := app.Connect()
	defer c.Close()
	// A live island's action answers 204: the re-render travels on the
	// connection as a patch frame, not in the action's own response body.
	status, _ = app.Action(0).Live(c).Fire()
	assert.Equal(t, http.StatusNoContent, status)
	patch := c.Await("count-1")
	assert.NotContains(t, patch, "row-drop", "the dropped row must be gone from the pushed patch")
	assert.Contains(t, patch, "row-keep", "the surviving row must still render")
}

// eachListIsland renders via l.Each(row) — the List method that sugars over
// via.Each(l.Get(), row) — rather than calling via.Each directly.
type eachListIsland struct{ items via.List[string] }

func (e *eachListIsland) View() h.H        { return h.Ul(e.items.Each(e.row)) }
func (e *eachListIsland) row(s string) h.H { return h.Li(h.Str(s)) }

// List.Each must render every element in order through the List method, not
// just through the free via.Each function it wraps.
func TestList_eachRendersRowsInOrder(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(eachListIsland{items: newListItems("one", "two", "three")}))
	status, body := app.Get("/")
	assert.Equal(t, http.StatusOK, status)
	assert.Regexp(t, "<li>one</li>.*<li>two</li>.*<li>three</li>", body,
		"List.Each must render rows in the list's order")
}

func newListItems(items ...string) via.List[string] {
	var l via.List[string]
	for _, s := range items {
		l.Append(s)
	}
	return l
}

// hooklessCounter is live purely because its View renders State — no OnInit,
// no Tick, no Listen — and its action must still route to this connection's
// own instance and push the re-render over the stream.
type hooklessCounter struct{ n via.State[int] }

func (c *hooklessCounter) Inc(ctx *via.Ctx) { c.n.Set(c.n.Get() + 1) }
func (c *hooklessCounter) View() h.H {
	return h.Div(h.Str("n="), c.n.Display(), h.Button(via.On("click", c.Inc)))
}

func TestState_liveActionOnAHooklessUnitPushesItsPatch(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(hooklessCounter{}))
	c := app.Connect()
	defer c.Close()

	status, _ := app.Action(0).Live(c).Fire()
	require.Equal(t, http.StatusNoContent, status,
		"a live action answers 204 — its re-render travels on the stream, not in the response")
	assert.Contains(t, c.Await("n="), "n=1", "the mutation must reach the browser as a patch frame")
}
