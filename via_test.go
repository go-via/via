package via_test

import (
	"bytes"
	"encoding/json"
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
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// store is the in-server state the counter tracks — a plain app dependency.
type store struct {
	mu sync.Mutex
	n  int
}

func (s *store) Value() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }
func (s *store) Add(d int)  { s.mu.Lock(); s.n += d; s.mu.Unlock() }

// counter is the slice-1 component under test, exercised through the public via
// API. It mirrors internal/example/counter: server-authoritative state in an injected
// dependency, element-patched on each action.
type counter struct{ count *store }

func (c *counter) Inc(ctx *via.Ctx) { c.count.Add(1) }
func (c *counter) Dec(ctx *via.Ctx) { c.count.Add(-1) }

func (c *counter) View() h.H {
	return h.Div(
		h.H1(h.Str(c.count.Value())),
		h.Button(via.On("click", c.Dec), h.Str("-")),
		h.Button(via.On("click", c.Inc), h.Str("+")),
	)
}

// newCounter starts one httptest server whose counter shares a single store, so
// sequential requests observe persisted server state.
func newCounter(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(via.Handler(counter{count: &store{}}))
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (*http.Response, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	require.NoError(t, err, "build request")
	// Simulate a same-origin browser fetch so the origin floor (on the action
	// POST and the SSE GET) admits the request; tests that exercise the floor
	// itself build their own requests (see post()/sseStatus in security_test.go).
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if method == http.MethodPost {
		// The bundled Datastar client sends this on every @post; dispatch reads
		// it to tell a JSON action apart from a native form submit.
		req.Header.Set("Datastar-Request", "true")
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err, "request failed")
	t.Cleanup(func() { resp.Body.Close() })
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read body")
	return resp, string(b)
}

// The raw-httptest helpers below back the tests that assert on response headers
// or SSE frame structure (csp, theme, live, and via's own Content-Type checks),
// which the vt harness deliberately does not expose; the behavior-only tests
// (signals, compose, security, state) drive vt instead.
func serve(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// post issues a POST to the action endpoint with exactly the given headers and
// no defaults, so a test pins precisely the origin signal it intends to send
// (do() injects same-origin, which would mask the origin floor).
func post(t *testing.T, srv *httptest.Server, path, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	// A JSON action POST — every caller of post() carries a Datastar signals
	// body, never a native form submit.
	req.Header.Set("Datastar-Request", "true")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp, readAll(t, resp)
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

func sameOrigin() map[string]string { return map[string]string{"Sec-Fetch-Site": "same-origin"} }

// actionURL extracts the n-th action URL child rendered, in document order,
// out of html. The wire id is a hash of the handler's func name (and any ?a=
// row datum rides with it), so a test posts the URL the page actually
// shipped rather than a hand-built path.
func actionURL(t *testing.T, html string, child string, n int) string {
	t.Helper()
	pat := `(?:@post\('|action=")([^'"]*_via/a/` + child + `/[A-Za-z0-9_-]+(?:[?&][^'"]*)?)['"]`
	m := regexp.MustCompile(pat).FindAllStringSubmatch(html, -1)
	require.Greaterf(t, len(m), n, "action %s/%d not found on rendered page:\n%s", child, n, html)
	return m[n][1]
}

func TestPage_shipsServerRenderedSkeleton(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/", "")

	ct := resp.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/html"), "page Content-Type = %q, want text/html", ct)
	for _, want := range []string{
		`<div id="root"`,
		`<h1>0</h1>`, // value rendered server-side, not a signal
		`data-on:click="@post('` + actionURL(t, body, "r", 0) + `'`, // Dec, declared first
		`data-on:click="@post('` + actionURL(t, body, "r", 1) + `'`, // Inc, declared second
		`src="/_via/datastar.js">`,                                  // module script tag (external, admitted by 'self')
	} {
		assert.Contains(t, body, want, "page missing skeleton fragment")
	}
}

func TestEventBinding_usesDatastarColonSyntaxNotDeadDashForm(t *testing.T) {
	t.Parallel()
	_, body := do(t, newCounter(t), http.MethodGet, "/", "")

	assert.Contains(t, body, `data-on:click="@post(`, "page is missing the v1 colon event binding data-on:click")
	assert.NotContains(t, body, "data-on-click", "page ships the dead v0.x dash form data-on-click (silently ignored by Datastar v1.0.2)")
}

func TestAction_elementPatchesAndPersists(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	dec, inc := actionURL(t, page, "r", 0), actionURL(t, page, "r", 1)

	resp, body := do(t, srv, http.MethodPost, inc, "{}") // Inc: 0 -> 1
	ct := resp.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/html"), "Content-Type = %q, want text/html (element-patch)", ct)
	for _, want := range []string{`<div id="root"`, `<h1>1</h1>`} {
		assert.Contains(t, body, want, "patch missing fragment")
	}

	_, body = do(t, srv, http.MethodPost, inc, "{}") // 1 -> 2
	assert.Contains(t, body, `<h1>2</h1>`, "second Inc did not persist to 2")
	_, body = do(t, srv, http.MethodPost, dec, "{}") // 2 -> 1
	assert.Contains(t, body, `<h1>1</h1>`, "Dec did not bring state back to 1")
}

func TestUnknownAction_isGone(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	url := swapActionID(t, actionURL(t, page, "r", 0), "zzzzzzzz")

	resp, _ := do(t, srv, http.MethodPost, url, "{}")
	assert.Equal(t, http.StatusGone, resp.StatusCode, "want 410 Gone")
}

func TestAction_invalidUTF8InSignalBodyIsNotA400(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(boundForm{}))
	status, _ := app.Action(0).Body("{\"name\":\"\xff\"}").Fire()
	assert.Equal(t, http.StatusOK, status)
}

func TestAction_duplicateSignalKeyLastWins(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(boundForm{}))
	status, frag := app.Action(0).Body(`{"name":"one","name":"two"}`).Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, frag, "two")
	assert.NotContains(t, frag, "one")
}

func TestEmbeddedDatastarClient_isServedAsJS(t *testing.T) {
	t.Parallel()
	resp, body := do(t, newCounter(t), http.MethodGet, "/_via/datastar.js", "")

	require.Equal(t, http.StatusOK, resp.StatusCode, "want 200")
	ct := resp.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/javascript"), "Content-Type = %q, want text/javascript", ct)
	assert.NotEmpty(t, body, "served datastar.js was empty")
}

func TestAction_respondsWithElementPatchNotSignalPatch(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 1), "{}")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	ct := resp.Header.Get("Content-Type")
	assert.Truef(t, strings.HasPrefix(ct, "text/html"), "action must respond text/html, got %q", ct)
	assert.NotContains(t, ct, "application/json")
	assert.Contains(t, body, `<div id="root"`, "element-patch must carry the #root morph target")
}

// noopComp's action is a pure side effect that changes nothing the View reads —
// the kind of action that logs, enqueues a job, or marks something the UI has
// already reflected.
type noopComp struct{}

func (n *noopComp) Ping(*via.Ctx) {}
func (n *noopComp) View() h.H {
	return h.Div(h.Button(via.On("click", n.Ping), h.Str("ping")))
}

func TestAction_returns204WhenViewIsUnchanged(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(noopComp{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := post(t, srv, actionURL(t, page, "r", 0), "{}", sameOrigin())
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Empty(t, body)
}

type formComp struct{ q via.Signal[string] }

func (c *formComp) Go(ctx *via.Ctx) {}
func (c *formComp) View() h.H {
	return h.Form(via.On("submit", c.Go), h.Input(c.q.Bind()))
}

func TestOn_submitWiresAPostAction(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Handler(formComp{})), http.MethodGet, "/", "")
	assert.Contains(t, body, `data-on:submit="@post('`+actionURL(t, body, "r", 0)+`'`)
	assert.NotContains(t, body, "data-on-submit", "must use the colon form, not the dead dash form")
}

// reqEchoer is a plain component whose action copies a header off the
// triggering request into a rendered field.
type reqEchoer struct{ echo string }

func (r *reqEchoer) Grab(ctx *via.Ctx) { r.echo = ctx.Request().Header.Get("X-Echo") }
func (r *reqEchoer) View() h.H {
	return h.Div(h.Button(via.On("click", r.Grab), h.Str("x")), h.P(h.Str(r.echo)))
}

func TestAction_canReadTheTriggeringRequest(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(reqEchoer{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	_, body := post(t, srv, actionURL(t, page, "r", 0), "{}", map[string]string{
		"Sec-Fetch-Site": "same-origin",
		"X-Echo":         "hello-from-header",
	})
	assert.Contains(t, body, "hello-from-header", "the action must see the triggering request via ctx.Request()")
}

// digestEchoer renders a bound signal's raw value straight into the page, so a
// test can post hostile text through the signal channel (a header can't carry
// a literal NUL byte; a JSON body can).
type digestEchoer struct {
	q    via.Signal[string]
	echo string
}

func (c *digestEchoer) Grab(ctx *via.Ctx) { c.echo = c.q.Get() }
func (c *digestEchoer) View() h.H {
	return h.Div(h.Input(c.q.Bind()), h.Button(via.On("click", c.Grab), h.Str("x")), h.P(h.Str(c.echo)))
}

func TestAction_digestPlaceholderCannotBeForgedByUserText(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(digestEchoer{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	slot := attrValue(t, page, "data-bind")
	nul := string(byte(0))
	hostile := nul + "vD0" + nul
	reqBody, err := json.Marshal(map[string]string{slot: hostile})
	require.NoError(t, err)
	url := actionURL(t, page, "r", 0)
	digest := url[strings.Index(url, "v=")+2:]
	_, body := post(t, srv, url, string(reqBody), sameOrigin())
	p := regexp.MustCompile(`<p>(.*?)</p>`).FindStringSubmatch(body)
	require.Len(t, p, 2, "rendered paragraph not found:\n%s", body)
	assert.NotContains(t, p[1], digest, "hostile text carrying the raw digest-placeholder token got the real shape digest spliced into it")
}

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

// changePicker exercises On("change", ...): a select whose commit posts an action,
// with no value carried (that's OnArg's job, tested elsewhere).
type changePicker struct{ got string }

func (a *changePicker) Pick(ctx *via.Ctx) { a.got = "picked" }
func (a *changePicker) View() h.H {
	return h.Div(
		h.El("select", via.On("change", a.Pick)),
		h.P(h.Str(a.got)),
	)
}

func TestOn_changeFiresHandlerOnCommit(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(changePicker{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	assert.Contains(t, page, `data-on:change`, "OnChange must bind the change event")

	_, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), "{}")
	assert.Contains(t, body, "picked", "OnChange's handler did not run")
}

// tickSessionWriter starts a Tick in OnInit that writes to the session on
// every beat — the pattern I2 covers: a Tick/Listen handler's Ctx is the same
// Ctx OnInit held, so its sessW was set (for OnInit's own use) and, if
// never cleared, silently survives long past the point the response it
// pointed at was flushed.
type tickSessionWriter struct{ n via.State[int] }

func (t *tickSessionWriter) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, t.beat)
	return nil
}
func (t *tickSessionWriter) beat(ctx *via.Ctx) {
	t.n.Set(t.n.Get() + 1)
	ctx.Session().Put(member{Name: "orphan"})
}
func (t *tickSessionWriter) View() h.H { return h.Div(t.n.Display()) }

func TestConnectUnit_tickSessionWriteWarnsInsteadOfWritingADeadResponse(t *testing.T) {
	// Sequential: it captures the global log output.
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(tickSessionWriter{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
		lines, cancel := openStream(t, srv)
		defer cancel()
		_ = awaitTabID(t, lines)
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
	})

	assert.Contains(t, buf.String(), "no cookie can be set",
		"a Tick-minted session must warn instead of silently orphaning")
}

// tickSessionWriterOnConnectRead is tickSessionWriter with a read in OnInit,
// the README's recommended pattern: the handle cached there must not keep the
// connect writer live past the flush.
type tickSessionWriterOnConnectRead struct{ n via.State[int] }

func (t *tickSessionWriterOnConnectRead) OnInit(ctx *via.Ctx) error {
	ctx.Session().Get[member]()
	ctx.Tick(10*time.Millisecond, t.beat)
	return nil
}
func (t *tickSessionWriterOnConnectRead) beat(ctx *via.Ctx) {
	t.n.Set(t.n.Get() + 1)
	ctx.Session().Put(member{Name: "orphan"})
}
func (t *tickSessionWriterOnConnectRead) View() h.H { return h.Div(t.n.Display()) }

func TestConnectUnit_tickSessionWriteWarnsEvenAfterOnInitReadTheSession(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(tickSessionWriterOnConnectRead{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
		lines, cancel := openStream(t, srv)
		defer cancel()
		_ = awaitTabID(t, lines)
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
	})

	assert.Contains(t, buf.String(), "no cookie can be set",
		"OnInit's own read must not leave the cached handle's writer pointed at the flushed connect response")
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

// collidePage mints "a_b" twice: once for the nested A.B (nested struct names
// join with "_") and once for the sibling field A_b. Two spans bind one slot,
// one slot is declared, and the hydrator map keeps whichever came last — so a
// POST writes the wrong field, silently.
type collidePage struct {
	A   struct{ B via.Signal[int] }
	A_b via.Signal[int]
}

func (p *collidePage) View() h.H { return h.Div(p.A.B.Bind(), p.A_b.Bind()) }

// childCollidePage's field A__b mints "a__b" in the parent, which is exactly
// the slot the child of field A gives its own signal B ("a__" + "b").
type childKidB struct{ B via.Signal[int] }

func (k *childKidB) View() h.H { return k.B.Bind() }

type childCollidePage struct {
	A    childKidB
	A__b via.Signal[int]
}

func (p *childCollidePage) View() h.H { return h.Div(via.Child(p.A), p.A__b.Bind()) }

func assertSlotPanic(t *testing.T, app http.Handler, want string) {
	t.Helper()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, logs.String(), want)
}

// assertMountPanic drives the fail-at-boot claim: a composition whose signals
// cannot be named must be rejected where the app is wired, not once per request
// for the life of the process.
func assertMountPanic(t *testing.T, want string, mount func()) {
	t.Helper()
	var rec any
	func() {
		defer func() { rec = recover() }()
		mount()
	}()
	require.NotNil(t, rec, "expected a panic at Mount containing %q", want)
	assert.Contains(t, fmt.Sprint(rec), want)
}

func TestSignal_duplicateSlotNamePanicsAtMount(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, "signal slot a_b is minted twice", func() { via.Handler(collidePage{}) })
}

type tagBareKey struct {
	Open via.SignalCS[bool] `via:"true"`
}

func (p *tagBareKey) View() h.H { return h.Div() }

type tagUnknownKey struct {
	Count via.Signal[int] `via:"seed=1"`
}

func (p *tagUnknownKey) View() h.H { return h.Div(p.Count.Display()) }

type tagKeyNoValue struct {
	Count via.Signal[int] `via:"init"`
}

func (p *tagKeyNoValue) View() h.H { return h.Div(p.Count.Display()) }

func TestMount_panicsOnAnUnknownTagKey(t *testing.T) {
	t.Parallel()
	t.Run("no key at all", func(t *testing.T) {
		t.Parallel()
		assertMountPanic(t, `tagBareKey.Open has via:"true": the only key is init=<json>`,
			func() { via.Handler(tagBareKey{}) })
	})
	t.Run("a key via does not know", func(t *testing.T) {
		t.Parallel()
		assertMountPanic(t, `tagUnknownKey.Count has via:"seed=1": the only key is init=<json>`,
			func() { via.Handler(tagUnknownKey{}) })
	})
	t.Run("the key with no value", func(t *testing.T) {
		t.Parallel()
		assertMountPanic(t, `tagKeyNoValue.Count has via:"init": the only key is init=<json>`,
			func() { via.Handler(tagKeyNoValue{}) })
	})
}

type tagOnPlainField struct {
	Name string `via:"init=\"x\""`
}

func (p *tagOnPlainField) View() h.H { return h.Div() }

type tagOnNestedField struct {
	Chat struct{ X int } `via:"init=1"`
}

func (p *tagOnNestedField) View() h.H { return h.Div() }

func TestMount_panicsOnASeedTagOnAPlainField(t *testing.T) {
	t.Parallel()
	want := ` has a via:"…" tag, but only a Signal, SignalCS, State or List field reads it`
	t.Run("plain field", func(t *testing.T) {
		t.Parallel()
		assertMountPanic(t, "tagOnPlainField.Name"+want, func() { via.Handler(tagOnPlainField{}) })
	})
	t.Run("nested struct field", func(t *testing.T) {
		t.Parallel()
		assertMountPanic(t, "tagOnNestedField.Chat"+want, func() { via.Handler(tagOnNestedField{}) })
	})
}

// boxedSignal reaches its signal through a pointer field, so the handle lives
// outside the composition struct and has no field offset to name itself by.
type boxedSignal struct{ S *via.Signal[string] }

func (b *boxedSignal) View() h.H { return h.Div(h.Input(b.S.Bind())) }

// valueReceiverView binds a stack copy: every signal offsets from the wrong
// base, so its writes land on a struct the render throws away.
type valueReceiverView struct{ S via.Signal[int] }

type slicedSignal struct{ S []via.Signal[string] }

func (b *slicedSignal) View() h.H { return h.Div() }

type mappedSignal struct{ S map[string]via.Signal[string] }

func (b *mappedSignal) View() h.H { return h.Div() }

type valueReceiverChildParent struct{ C valueReceiverView }

func (p *valueReceiverChildParent) View() h.H { return h.Div(via.Child(p.C)) }

func (v valueReceiverView) View() h.H { return h.Div(v.S.Display()) }

func TestSignal_behindAPointerFieldPanicsAtMount(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, "holds a via.Signal behind a ptr", func() {
		via.Handler(boxedSignal{S: &via.Signal[string]{}})
	})
}

func TestSignal_behindASliceFieldPanicsAtMount(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, "holds a via.Signal behind a slice", func() { via.Handler(slicedSignal{}) })
}

func TestSignal_behindAMapFieldPanicsAtMount(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, "holds a via.Signal behind a map", func() { via.Handler(mappedSignal{}) })
}

func TestSignal_valueReceiverViewPanicsAtMount(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, "View has a VALUE receiver", func() { via.Handler(valueReceiverView{}) })
}

func TestSignal_valueReceiverChildPanicsAtRender(t *testing.T) {
	// Not Parallel: it captures the process-global log, which every other test writes to.
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	rec := httptest.NewRecorder()
	via.Handler(valueReceiverChildParent{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, logs.String(), "View has a VALUE receiver")
}

func TestSignal_slotCollidingWithAChildPrefixPanics(t *testing.T) {
	assertSlotPanic(t, via.Handler(childCollidePage{}), "collides with the child prefix of field a")
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

type phantomChild struct {
	Load via.Signal[[]int]  `via:"init=[]"`
	Open via.SignalCS[bool] `via:"init=true"`
}

func (c *phantomChild) View() h.H { return h.Div() }

type phantomRoot struct{ Sub phantomChild }

func (p *phantomRoot) View() h.H { return h.Div(via.Child(p.Sub)) }

func TestMount_mintsNoSlotForAChildCompositionsSignals(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Handler(phantomRoot{})).Get("/")

	assert.NotContains(t, body, `"sub_load"`)
	assert.NotContains(t, body, `"_sub_open"`)
	assert.Contains(t, body, `"sub__load":[]`)
	assert.Contains(t, body, `"_sub__open":true`)
}

type tagChildSub struct{ N via.Signal[int] }

func (s *tagChildSub) View() h.H { return h.Div() }

type tagOnChildComposition struct {
	Sub tagChildSub `via:"init=1"`
}

func (p *tagOnChildComposition) View() h.H { return h.Div(via.Child(p.Sub)) }

func TestMount_panicsOnASeedTagOnAChildComposition(t *testing.T) {
	t.Parallel()
	assertMountPanic(t, `tagOnChildComposition.Sub has a via:"…" tag, but only a Signal, SignalCS, State or List field reads it`,
		func() { via.Handler(tagOnChildComposition{}) })
}

type bareChildSub struct{ Name via.Signal[string] }

func (s *bareChildSub) View() h.H { return h.Div(h.Input(s.Name.Bind())) }

type bareChildParent struct{ Sub bareChildSub }

func (p *bareChildParent) View() h.H { return h.Div(p.Sub.View()) }

func TestChild_renderedWithoutChildPanicsNamingViaChild(t *testing.T) {
	assertSlotPanic(t, via.Handler(bareChildParent{}),
		"a child composition must be rendered through via.Child, not by calling its View")
}
