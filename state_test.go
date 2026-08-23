package via_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
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
func (e *stateEcho) Set(ctx *via.Ctx)             { e.msg.Set("<b>Ada</b>") }
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

// The slice edits are the mirror of Append: each one is the intent spelled at
// the call site, and each must leave the list in exactly the state the name
// promises. Read as a whole this pins the shape of the surface, not one method.
func TestList_slice_edits(t *testing.T) {
	t.Parallel()
	var l via.List[string]
	l.Append("a")
	l.Append("c")
	l.Insert(1, "b")
	assert.Equal(t, []string{"a", "b", "c"}, l.Get(), "Insert shifts the rest right")
	assert.Equal(t, 3, l.Len())
	assert.Equal(t, "b", l.At(1))

	l.Replace(1, "B")
	assert.Equal(t, []string{"a", "B", "c"}, l.Get(), "Replace overwrites in place")

	l.Remove(1)
	assert.Equal(t, []string{"a", "c"}, l.Get(), "Remove shifts the rest left")

	l.Insert(l.Len(), "d")
	assert.Equal(t, []string{"a", "c", "d"}, l.Get(), "Insert at Len appends")

	l.Truncate(2)
	assert.Equal(t, []string{"a", "c"}, l.Get(), "Truncate keeps the first n")
	l.Truncate(9)
	assert.Equal(t, []string{"a", "c"}, l.Get(), "Truncate past the end is a no-op")

	l.Clear()
	assert.Empty(t, l.Get(), "Clear drops every element")
	assert.Equal(t, 0, l.Len())
}

// A wrong index is a programming error. The list panics rather than silently
// doing nothing, because a no-op would hide the bug behind a list that just
// never changes — the same reason State panics off-island.
func TestList_outOfRangeIndexPanics(t *testing.T) {
	t.Parallel()
	newList := func() *via.List[string] {
		var l via.List[string]
		l.Append("a")
		return &l
	}
	assert.Panics(t, func() { newList().At(1) }, "At past the end")
	assert.Panics(t, func() { newList().At(-1) }, "At below zero")
	assert.Panics(t, func() { newList().Remove(1) }, "Remove past the end")
	assert.Panics(t, func() { newList().Replace(1, "b") }, "Replace past the end")
	assert.Panics(t, func() { newList().Insert(2, "b") }, "Insert past the append position")
	assert.Panics(t, func() { newList().Truncate(-1) }, "Truncate to a negative length")
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

func (t *listIsland) OnConnect(ctx *via.Ctx) error { return nil }
func (t *listIsland) DropFirst(ctx *via.Ctx)       { t.items.Remove(0) }
func (t *listIsland) View() h.H {
	return h.Div(
		via.Each(t.items.Get(), func(s string) h.H {
			return h.P(h.RawAttr("id", "row-"+s), h.Str(s))
		}),
		// A marker that changes with the length, so a test can await the
		// post-removal frame rather than matching the row it expects gone.
		h.P(h.RawAttr("id", "count-"+strconv.Itoa(t.items.Len()))),
		h.Button(via.OnClick(t.DropFirst), h.Str("drop")),
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
	status, _ = app.Action(0).Tab(c.TabID()).Fire()
	assert.Equal(t, http.StatusNoContent, status)
	patch := c.Await("count-1")
	assert.NotContains(t, patch, "row-drop", "the dropped row must be gone from the pushed patch")
	assert.Contains(t, patch, "row-keep", "the surviving row must still render")
}

func newListItems(items ...string) via.List[string] {
	var l via.List[string]
	for _, s := range items {
		l.Append(s)
	}
	return l
}
