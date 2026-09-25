package via_test

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type todoItem struct {
	ID   int
	Text string
}

type todoBox struct{ items []todoItem }

func (b *todoBox) remove(id int) {
	out := b.items[:0]
	for _, it := range b.items {
		if it.ID != id {
			out = append(out, it)
		}
	}
	b.items = out
}

// todoList is a plain list whose rows each carry a delete action bound to the
// row's id — the per-row-action case. Del receives the id as a typed parameter.
type todoList struct{ box *todoBox }

func (l *todoList) Del(ctx *via.Ctx, id int) { l.box.remove(id) }
func (l *todoList) row(t todoItem) h.H {
	return h.Li(h.Str(t.Text), h.Button(via.OnArg("click", l.Del, t.ID), h.Str("x")))
}
func (l *todoList) View() h.H { return h.Ul(via.Each(l.box.items, l.row)) }

func newTodoList() *todoBox {
	return &todoBox{items: []todoItem{{1, "alpha"}, {2, "bravo"}, {3, "gamma"}}}
}

func TestActionArg_buttonCarriesTheRowValue(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(todoList{box: newTodoList()})), http.MethodGet, "/", "")
	assert.Regexp(t, `@post\('/_via/a/r/[A-Za-z0-9_-]+\?a=2'`, body, "the bravo row's button must carry its id (2) as the action arg")
}

func TestActionArg_handlerReceivesTheTypedValue(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(todoList{box: newTodoList()}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, body, "bravo", "the row whose value was sent must be deleted")
	assert.Contains(t, body, "alpha")
	assert.Contains(t, body, "gamma")
}

func TestActionArg_valueNotSlotIdentifiesTheRow(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(todoList{box: newTodoList()}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	// slot 0 is alpha's own action (its rendered arg is ?a=1); swap in bravo's
	// value (2) while keeping alpha's slot and shape digest.
	url := strings.Replace(actionURL(t, page, "r", 0), "a=1", "a=2", 1)
	resp, body := do(t, srv, http.MethodPost, url, "{}") // slot 0 (alpha), but arg=2 (bravo)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, body, "bravo", "the carried value (2) must win over the slot (0)")
	assert.Contains(t, body, "alpha")
}

func TestActionArg_malformedArgAnswers400(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(todoList{box: newTodoList()}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	url := strings.Replace(actionURL(t, page, "r", 0), "a=1", "a=%22abc%22", 1)
	resp, body := do(t, srv, http.MethodPost, url, "{}")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.NotContains(t, body, "alpha", "the row must not be rendered as deleted by a malformed arg")
}

func TestActionArg_missingArgAnswers400(t *testing.T) {
	tests := []struct {
		name string
		a    string
	}{
		{"empty", "a="},
		{"null", "a=null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := serve(t, via.Handler(todoList{box: newTodoList()}))
			_, page := do(t, srv, http.MethodGet, "/", "")
			url := strings.Replace(actionURL(t, page, "r", 0), "a=1", tt.a, 1)
			resp, body := do(t, srv, http.MethodPost, url, "{}")
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.NotContains(t, body, "alpha", "the row must not be rendered as deleted by a missing arg")
		})
	}
}

// todoBoard embeds the todo list as a plain child — per-row value-actions
// must work there too, not only at the root.
type todoBoard struct{ List todoList }

func (b *todoBoard) View() h.H { return h.Div(via.Child(b.List)) }

func TestActionArg_worksInsideAPlainChild(t *testing.T) {
	t.Parallel()
	board := todoBoard{List: todoList{box: newTodoList()}}
	srv := serve(t, via.Handler(board))
	_, page := do(t, srv, http.MethodGet, "/", "")

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "0", 1), "{}") // child 1, bravo's slot, arg=2
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, body, "bravo", "the child row's value-action did not fire")
	assert.Contains(t, body, "alpha")
}

func TestActionID_listMutationByAnotherTabDoesNotBreakOpenTabs(t *testing.T) {
	t.Parallel()
	box := newTodoList()
	srv := serve(t, via.Handler(todoList{box: box}))

	_, tabB := do(t, srv, http.MethodGet, "/", "") // tab B paints, then sits idle
	bravoFromB := rowActionURL(t, tabB, 2)

	_, tabA := do(t, srv, http.MethodGet, "/", "")
	resp, _ := do(t, srv, http.MethodPost, rowActionURL(t, tabA, 1), "{}") // tab A deletes alpha
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, body := do(t, srv, http.MethodPost, bravoFromB, "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"tab B's already-rendered row action must survive another tab resizing the list")
	assert.NotContains(t, body, "bravo", "and it must have deleted bravo — the row it named")
	assert.Contains(t, body, "gamma", "not some other row")
}

func rowActionURL(t *testing.T, html string, id int) string {
	t.Helper()
	m := regexp.MustCompile(`@post\('([^']*_via/a/r/[A-Za-z0-9_-]+\?a=` + strconv.Itoa(id) + `(?:&[^']*)?)'`).FindStringSubmatch(html)
	require.NotEmptyf(t, m, "no row action for id %d in:\n%s", id, html)
	return m[1]
}

func TestActionID_isStableAcrossRendersAndInstances(t *testing.T) {
	t.Parallel()
	_, first := do(t, serve(t, via.Handler(counter{count: &store{}})), http.MethodGet, "/", "")
	_, second := do(t, serve(t, via.Handler(counter{count: &store{}})), http.MethodGet, "/", "")
	assert.Equal(t, actionURL(t, first, "r", 1), actionURL(t, second, "r", 1),
		"two independent instances must address the same handler identically")
}

// twinButtons binds the same handler twice. Both buttons mean the same thing,
// so they collapse onto one action entry and one URL — identity is the
// handler, never the render position.
type twinButtons struct{ count *store }

func (c *twinButtons) Inc(ctx *via.Ctx) { c.count.Add(1) }
func (c *twinButtons) View() h.H {
	return h.Div(
		h.H1(h.Str(strconv.Itoa(c.count.Value()))),
		h.Button(via.On("click", c.Inc)),
		h.Button(via.On("click", c.Inc)),
	)
}

func TestActionID_sameHandlerTwiceCollapsesToOneEntry(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(twinButtons{count: &store{}}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	assert.Equal(t, actionURL(t, page, "r", 0), actionURL(t, page, "r", 1),
		"two bindings of one handler must share one id")

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<h1>1</h1>", "and it must dispatch to that handler")
}

func TestUnknownAction_answers410NamingOnlyTheAskedForID(t *testing.T) {
	// Sequential: it captures the global log output.
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, swapActionID(t, actionURL(t, page, "r", 0), "zzzzzzzz"), "{}")
	require.Equal(t, http.StatusGone, resp.StatusCode)
	assert.Contains(t, body, "zzzzzzzz", "the 410 must name the id that was asked for")
	assert.NotContains(t, body, "Inc", "but never the Go method names of the render")
	assert.Contains(t, buf.String(), "Inc", "the bound handlers go to the server log instead")
}

type idCounter struct {
	N via.Signal[int]
}

func (c *idCounter) Inc(ctx *via.Ctx) { c.N.Set(c.N.Get() + 1) }

func (c *idCounter) View() h.H {
	return h.Div(h.Button(via.On("click", c.Inc), h.Str("+")), c.N.Display())
}

type idPair struct{ A, B idCounter }

func (p *idPair) View() h.H { return h.Div(via.Child(p.A), via.Child(p.B)) }

// idTwins holds two instances of one type as plain fields (no Child), so both
// bind into the same action table. runtime.FuncForPC drops the receiver, so
// without the offset in the id both buttons would render the same action URL
// and A's click would run B's handler.
//
// It renders through a plain method: a field with its own View is a child
// composition, and only via.Child may render one.
type idTwin struct {
	N via.Signal[int]
}

func (c *idTwin) Inc(ctx *via.Ctx) { c.N.Set(c.N.Get() + 1) }

func (c *idTwin) row() h.H {
	return h.Div(h.Button(via.On("click", c.Inc), h.Str("+")), c.N.Display())
}

type idTwins struct{ A, B idTwin }

func (p *idTwins) View() h.H { return h.Div(p.A.row(), p.B.row()) }

var actionURLRe = regexp.MustCompile(`@post\('([^']+)'`)

func actionURLs(t *testing.T, h http.Handler) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var out []string
	for _, m := range actionURLRe.FindAllStringSubmatch(rec.Body.String(), -1) {
		out = append(out, m[1])
	}
	return out
}

func TestActionID_twoInstancesOfOneTypeGetDistinctIDs(t *testing.T) {
	t.Parallel()
	urls := actionURLs(t, via.Handler(idTwins{}))
	require.Len(t, urls, 2)
	require.NotEqual(t, urls[0], urls[1],
		"two instances of one type must not share an action id — A's click would run B")
}

func TestActionID_embeddedSiblingsGetDistinctIDs(t *testing.T) {
	t.Parallel()
	urls := actionURLs(t, via.Handler(idPair{}))
	require.Len(t, urls, 2)
	require.NotEqual(t, urls[0], urls[1])
}

func TestActionID_postRoutesToItsOwnReceiver(t *testing.T) {
	t.Parallel()
	app := via.Handler(idTwins{})
	urls := actionURLs(t, app)
	require.Len(t, urls, 2)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, urls[1], strings.NewReader(`{}`))
	req.Header.Set("Datastar-Request", "true")
	app.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	// Two signals on the page; the second twin's is the one that moved.
	slots := regexp.MustCompile(`data-text="\$([a-z0-9_]+)"`).FindAllStringSubmatch(rec.Body.String(), -1)
	require.Len(t, slots, 2)
	require.Contains(t, rec.Body.String(), `"`+slots[1][1]+`":1`, "body: %s", rec.Body.String())
}

type idSameMethodTwice struct{ N via.Signal[int] }

func (c *idSameMethodTwice) Inc(ctx *via.Ctx) { c.N.Set(c.N.Get() + 1) }

func (c *idSameMethodTwice) View() h.H {
	return h.Div(
		h.Button(via.On("click", c.Inc), h.Str("+")),
		h.Button(via.On("click", c.Inc), h.Str("also +")),
	)
}

func TestActionID_sameHandlerTwiceCollapses(t *testing.T) {
	t.Parallel()
	urls := actionURLs(t, via.Handler(idSameMethodTwice{}))
	require.Len(t, urls, 2)
	require.Equal(t, urls[0], urls[1])
}

type idClosurePair struct{ hits [2]int }

func (p *idClosurePair) View() h.H {
	var kids []h.H
	for i := range p.hits {
		kids = append(kids, h.Button(via.On("click", func(ctx *via.Ctx) { p.hits[i]++ })))
	}
	return h.Div(kids...)
}

func TestActionID_indistinguishableHandlersPanic(t *testing.T) {
	// Sequential: it captures the global log output.
	app := via.Handler(idClosurePair{})
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, logs.String(), "share the action id")
}

// gridBench binds one handler a thousand times, which is the shape actionID's
// cost shows up in: the id is a pure function of (code pointer, receiver
// offset), so resolving the Go name and hashing it per binding per render was
// pure waste.
type gridBench struct{ rows []int }

func (g *gridBench) Hit(ctx *via.Ctx) {}

func (g *gridBench) row(int) h.H { return h.Button(via.On("click", g.Hit)) }

func (g *gridBench) View() h.H { return h.Div(via.Each(g.rows, g.row)) }

func BenchmarkRender_thousandActionBindings(b *testing.B) {
	handler := via.Handler(gridBench{rows: make([]int, 1000)})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for b.Loop() {
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func TestActionID_memoIsStableAcrossRenders(t *testing.T) {
	// Not Parallel: it captures the process-global log, which every other test writes to.
	handler := via.Handler(idTwins{})
	first := actionURLs(t, handler)
	second := actionURLs(t, handler)
	require.Len(t, first, 2)
	assert.Equal(t, first, second, "an action URL must survive a re-render, or every open tab 410s")
	assert.Equal(t, first, actionURLs(t, via.Handler(idTwins{})),
		"the id is content-addressed, not per-instance")
}

// ownedRows is the arg-authorization fixture: a list filtered by owner, plus
// an admin-only row reachable only through a via.When. Two users see two
// disjoint sets of ?a= values from one handler and one action id — which is
// the whole point: the action id is content-addressed on the handler, so the
// arg is the only thing separating alice's row from bob's.
type ownedRows struct {
	me    string
	admin bool
	rows  map[int]string // id -> owner
	gone  []int
}

func newOwnedRows(me string, admin bool) ownedRows {
	return ownedRows{me: me, admin: admin, rows: map[int]string{1: "alice", 2: "bob", 9: "system"}}
}

func (o *ownedRows) Delete(ctx *via.Ctx, id int) { o.gone = append(o.gone, id) }

func (o *ownedRows) mine() []int {
	var out []int
	for id, owner := range o.rows {
		if owner == o.me {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

func (o *ownedRows) systemRow() h.H {
	return h.Li(h.ID("system"), h.Button(via.OnArg("click", o.Delete, 9), h.Str("drop system")))
}

func (o *ownedRows) row(id int) h.H {
	return h.Li(h.ID("r"+strconv.Itoa(id)), h.Str(strconv.Itoa(id)),
		h.Button(via.OnArg("click", o.Delete, id), h.Str("delete")))
}

func (o *ownedRows) View() h.H {
	return h.Div(
		h.P(h.Str("deleted: "+fmt.Sprint(o.gone))),
		h.Ul(via.Each(o.mine(), o.row)),
		via.When(o.admin, o.systemRow),
	)
}

func TestActionArg_swappingInAnotherUsersArgIs410(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(newOwnedRows("bob", false)))
	_, page := do(t, srv, http.MethodGet, "/", "")
	require.Contains(t, page, "a=2", "bob's own row must be bound")
	require.NotContains(t, page, "a=1", "alice's row must not be rendered for bob")

	url := strings.Replace(actionURL(t, page, "r", 0), "a=2", "a=1", 1)
	resp, body := do(t, srv, http.MethodPost, url, "{}")
	assert.Equal(t, http.StatusGone, resp.StatusCode, "an arg bob's render never bound must not dispatch")
	assert.NotContains(t, body, "deleted: [1]", "alice's row was deleted by an arg swap")
}

func TestLiveActionArg_swappingInAnotherUsersArgIs410(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(liveOwnedRows{}))
		conn := app.Connect()

		url := strings.Replace(conn.ActionURL("r", 0), "a=2", "a=1", 1)
		status, _ := app.Action(0).Raw(url).Over(conn).Fire()
		assert.Equal(t, http.StatusGone, status, "an unrendered arg must not dispatch over a live stream")

		status, _ = app.Action(0).Over(conn).Fire() // the rendered arg, same slot
		assert.Equal(t, http.StatusNoContent, status, "the stream goroutine must still be alive")
		conn.Await("deleted 2")
	})
}

// liveOwnedRows is ownedRows' live twin: one rendered arg (2), a State to make
// the unit live, and a handler that reports what it was asked to delete.
type liveOwnedRows struct{ last via.State[string] }

func (l *liveOwnedRows) Delete(ctx *via.Ctx, id int) { l.last.Set("deleted " + strconv.Itoa(id)) }
func (l *liveOwnedRows) View() h.H {
	return h.Div(l.last.Display(), h.Button(via.OnArg("click", l.Delete, 2)))
}

func TestActionArg_argFromAClosedWhenBranchIs410(t *testing.T) {
	t.Parallel()
	admin := serve(t, via.Handler(newOwnedRows("alice", true)))
	_, adminPage := do(t, admin, http.MethodGet, "/", "")
	require.Contains(t, adminPage, `id="system"`)
	systemURL := actionURL(t, adminPage, "r", 1) // the When branch's own binding
	require.Contains(t, systemURL, "a=9")

	// Same handler, same action id, same URL — but a render with the branch shut.
	plain := serve(t, via.Handler(newOwnedRows("alice", false)))
	_, plainPage := do(t, plain, http.MethodGet, "/", "")
	require.NotContains(t, plainPage, `id="system"`)
	require.Contains(t, plainPage, "a=1", "the handler must still be bound, or this proves nothing")

	resp, body := do(t, plain, http.MethodPost, systemURL, "{}")
	assert.Equal(t, http.StatusGone, resp.StatusCode)
	assert.NotContains(t, body, "deleted: [9]")
}

func TestActionArg_renderedArgStillDispatches(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(newOwnedRows("bob", false)))
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), "{}")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "deleted: [2]", "bob's own row must still be deletable")
}

// bigList is via.Each over a large list: one handler, one action id, one
// thousand bound args. It pins that the arg set stays bounded by the render
// (a set of the very strings the HTML already carries) and that every row in
// it still dispatches.
type bigList struct{ hit int }

func (b *bigList) Pick(ctx *via.Ctx, id int) { b.hit = id }
func (b *bigList) row(id int) h.H            { return h.Li(h.Button(via.OnArg("click", b.Pick, id))) }
func (b *bigList) View() h.H {
	ids := make([]int, 1000)
	for i := range ids {
		ids[i] = i + 1
	}
	return h.Div(h.P(h.Str(b.hit)), h.Ul(via.Each(ids, b.row)))
}

func TestActionArg_eachOverALargeListDispatchesEveryRow(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(bigList{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	base := actionURL(t, page, "r", 0)
	for _, id := range []string{"1", "500", "1000"} {
		url := strings.Replace(base, "a=1", "a="+id, 1)
		resp, body := do(t, srv, http.MethodPost, url, "{}")
		require.Equalf(t, http.StatusOK, resp.StatusCode, "row %s must dispatch", id)
		assert.Containsf(t, body, "<p>"+id+"</p>", "row %s did not reach the handler", id)
	}
	resp, _ := do(t, srv, http.MethodPost, strings.Replace(base, "a=1", "a=1001", 1), "{}")
	assert.Equal(t, http.StatusGone, resp.StatusCode, "an id past the end of the list must not dispatch")
}

// The arg set's cost, measured rather than asserted by eye: rendering 1000
// value-carrying bindings must stay within a small constant per binding. The
// ceiling is deliberately loose (it is a regression tripwire, not a budget) —
// what it catches is the set turning into something super-linear, or the args
// being retained per row rather than merged per slot.
func BenchmarkEachArgSet(b *testing.B) {
	h := via.Handler(bigList{})
	srv := httptest.NewServer(h)
	defer srv.Close()
	b.ReportAllocs()
	for b.Loop() {
		resp, err := srv.Client().Get(srv.URL)
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// liveOwned promotes ownedRows' whole ownership model — two users, disjoint
// ?a= sets, one handler id — onto a unit that can be live, so the arg check
// can be held to the same claim off the root unit it is held to on it. The
// live twin below it (liveOwnedRows) proves only that an unrendered arg 410s;
// what matters is that another user's arg does, with his row left intact.
type liveOwned struct {
	ownedRows
	live bool
}

func (l *liveOwned) OnInit(ctx *via.Ctx) error {
	if l.live {
		ctx.Tick(time.Hour, func(*via.Ctx) {})
	}
	return nil
}

// ownedChildPage is the same model one level down: the dispatch address gains
// a child key, which is the part the root-only tests never exercise.
type ownedChildPage struct{ Rows liveOwned }

func (p *ownedChildPage) View() h.H { return h.Div(h.Str("page"), via.Child(p.Rows)) }

func bobsRows(live bool) liveOwned {
	return liveOwned{ownedRows: newOwnedRows("bob", false), live: live}
}

func TestActionArg_swappingInAnotherUsersArgIs410InEveryUnitShape(t *testing.T) {
	t.Parallel()
	shapes := []struct {
		name    string
		handler http.Handler
		child   string
		live    bool
	}{
		{"live root", via.Handler(bobsRows(true)), "r", true},
		{"plain child", via.Handler(ownedChildPage{Rows: bobsRows(false)}), "0", false},
		{"live child", via.Handler(ownedChildPage{Rows: bobsRows(true)}), "0", true},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			app := vt.Serve(t, shape.handler)
			_, page := app.Get("/")
			require.Contains(t, page, "a=2", "bob's own row must be bound")
			require.NotContains(t, page, "a=1", "alice's row must not be rendered for bob")

			own := actionURL(t, page, shape.child, 0)
			stolen := strings.Replace(own, "a=2", "a=1", 1)
			require.NotEqual(t, own, stolen, "the swap must actually change the URL")

			var conn *vt.Conn
			attack, legit := app.Action(0).Raw(stolen), app.Action(0).Raw(own)
			if shape.live {
				conn = app.Connect()
				attack, legit = attack.Over(conn), legit.Over(conn)
			}

			code, body := attack.Fire()
			assert.Equal(t, http.StatusGone, code, "alice's arg must not dispatch off bob's render")
			assert.NotContains(t, body, "deleted: [1", "alice's row was deleted by an arg swap")

			code, body = legit.Fire()
			require.Contains(t, []int{http.StatusOK, http.StatusNoContent}, code,
				"bob's own row must still dispatch")
			if shape.live {
				body = conn.Await("deleted: [")
			}
			assert.Contains(t, body, "deleted: [2]", "only bob's own row may have been deleted")
		})
	}
}

// rowStore is a list another request edits while a tab holds its old render.
type rowStore struct {
	mu     sync.Mutex
	ids    []string
	picked []string
}

func (s *rowStore) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ids...)
}

func (s *rowStore) setIDs(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = ids
}

func (s *rowStore) pick(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.picked = append(s.picked, id)
}

func (s *rowStore) picks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.picked...)
}

type rowPage struct {
	Store *rowStore
	ids   []string
}

func (p *rowPage) OnInit(ctx *via.Ctx) error {
	p.ids = p.Store.list()
	return nil
}

func (p *rowPage) Pick(ctx *via.Ctx, id string) { p.Store.pick(id) }

func (p *rowPage) row(id string) h.H {
	return h.Li(h.ID("row-"+id), h.Button(on.Click(on.WithArg(p.Pick, id)), h.Str(id)))
}

func (p *rowPage) View() h.H { return h.Ul(via.Each(p.ids, p.row)) }

func TestOnArg_staleRowClickAfterAReorderOrDeleteHitsItsOwnRowOrIsGone(t *testing.T) {
	t.Parallel()
	store := &rowStore{ids: []string{"a", "b", "c"}}
	app := vt.Serve(t, via.Handler(rowPage{Store: store}))
	_, _ = app.Action(0).Fire() // caches the a, b, c render the tab keeps clicking

	store.setIDs("c", "a")
	status, _ := app.Action(0).Fire()
	assert.Less(t, status, 300, "row a moved but still exists")
	status, _ = app.Action(1).Fire()
	assert.Equal(t, http.StatusGone, status, "row b is gone; its click must not land on the row now in its place")
	status, _ = app.Action(2).Fire()
	assert.Less(t, status, 300, "row c moved but still exists")

	assert.Equal(t, []string{"a", "a", "c"}, store.picks())
}
