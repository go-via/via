package via_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/topic"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fetchPage GETs path on app's client and returns the body — for a test that
// needs the currently-rendered action URL but must inspect response headers
// vt.Action.Fire doesn't expose (Content-Type, datastar-script-attributes).
func fetchPage(t *testing.T, app *vt.App, path string) string {
	t.Helper()
	resp, err := app.Client().Get(app.URL() + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

// liveRedirector is a live root whose action queues a Redirect — which a
// @post action can no longer act on; the action must still answer normally
// rather than hang or crash.
type liveRedirector struct{ n via.State[int] }

func (c *liveRedirector) Go(ctx *via.Ctx) { ctx.Redirect("/dest") }

func (c *liveRedirector) View() h.H {
	return h.Div(c.n.Display(), h.Button(via.On("click", c.Go)))
}

func TestDispatch_redirectFromLiveActionNavigatesTheTab(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(liveRedirector{}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")

		req, err := http.NewRequest(http.MethodPost, app.URL()+actionURL(t, page, "r", 0), strings.NewReader(withTab(conn.TabID(), "{}")))
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Datastar-Request", "true")
		resp, err := app.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/javascript")
		assert.JSONEq(t, `{"data-via-to":"/dest"}`, resp.Header.Get("datastar-script-attributes"))
		assert.Contains(t, string(body), "location.assign")
	})
}

// childRedirector is a PLAIN child (dispatchPlain, not
// liveRunAction) whose action queues a Redirect.
type childRedirector struct{}

func (r *childRedirector) Go(ctx *via.Ctx) { ctx.Redirect("/dest") }

func (r *childRedirector) View() h.H { return h.Div(h.Button(via.On("click", r.Go))) }

type childRedirectorParent struct{ I childRedirector }

func (p *childRedirectorParent) View() h.H { return h.Div(via.Child(p.I)) }

func TestDispatch_redirectFromPlainChildActionNavigatesTheTab(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(childRedirectorParent{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	req, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, "0", 0), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/javascript")
	assert.JSONEq(t, `{"data-via-to":"/dest"}`, resp.Header.Get("datastar-script-attributes"))
	assert.Contains(t, string(body), "location.assign")
}

// sigChild's signals are only Bound (never Displayed), so Setting one never
// changes the rendered HTML — the only way a client sees the new value is the
// container's data-signals attribute. Other exists solely so Reset's Set of
// Name has a sibling to leave alone: the response must declare Name and NOT
// Other, proving the patch is restricted to what the action actually wrote
// rather than the whole slot table.
type sigChild struct{ Name, Other via.Signal[string] }

func (s *sigChild) Reset(ctx *via.Ctx) { s.Name.Set("resetted") }

func (s *sigChild) View() h.H {
	return h.Div(s.Name.Bind(), s.Other.Bind(), h.Button(via.On("click", s.Reset)))
}

type sigPage struct{ I sigChild }

func (p *sigPage) View() h.H { return h.Div(via.Child(p.I)) }

func TestDispatch_signalSetInChildActionReachesClient(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(sigPage{}))

	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "0", 0), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"a Signal.Set with no visible HTML change must still ship a patch, not 204")
	assert.Contains(t, body, `"i__name":"resetted"`, "the Set signal must reach the client")
	assert.NotContains(t, body, `"i__other":`,
		"an untouched sibling signal must not be declared — Set restricts the patch, it doesn't broadcast the whole table")
}

// guardedChild is embedded under a parent whose own OnInit session-gates
// the whole mount, replacing the removed guard mechanism.
type guardedChild struct{}

func (g *guardedChild) Ping(ctx *via.Ctx) {}

func (g *guardedChild) View() h.H { return h.Div(h.Button(via.On("click", g.Ping))) }

type guardedParent struct{ I guardedChild }

func (p *guardedParent) OnInit(ctx *via.Ctx) error {
	if _, ok := ctx.Session().Get[acct](); !ok {
		ctx.Redirect("/login")
	}
	return nil
}

func (p *guardedParent) View() h.H { return h.Div(via.Child(p.I)) }

func TestDispatch_childActionRunsOnInitRedirect(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/g", guardedParent{})
	srv := serve(t, r)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/g/_via/a/0/0", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := (&http.Client{CheckRedirect: noFollow}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "the parent's OnInit Redirect must gate the child action route")
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

// mixedPage carries a @post action and a native PostForm on the same page, so
// their ids must come from the ONE action table dispatch now shares.
type mixedPage struct{ n int }

func (p *mixedPage) Bump(ctx *via.Ctx) { p.n++ }

func (p *mixedPage) Save(ctx *via.Ctx) { ctx.Redirect("/done") }

func (p *mixedPage) View() h.H {
	return h.Div(
		h.Str(p.n),
		h.Button(via.On("click", p.Bump), h.Str("+")), // action 0
		via.PostForm(p.Save, h.Button(h.Str("save"))), // action 1
	)
}

func TestDispatch_nativeFormUsesSameActionTable(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(mixedPage{}))

	_, page := do(t, srv, http.MethodGet, "/", "")
	assert.Contains(t, page, `@post('`+actionURL(t, page, "r", 0)+`'`, "the @post binding claims its own action id")
	assert.Contains(t, page, `action="`+actionURL(t, page, "r", 1)+`"`, "PostForm claims an id in the same table")

	resp, _ := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the @post action must dispatch")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.Close()
	req, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, "r", 1), &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp2, err := (&http.Client{CheckRedirect: noFollow}).Do(req)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp2.StatusCode, "PostForm must dispatch through the same table")
	assert.Equal(t, "/done", resp2.Header.Get("Location"))
}

// panicLive is a live root whose Boom action panics; Ping proves the
// connection is still usable afterward.
type panicLive struct{ n via.State[int] }

func (p *panicLive) Boom(ctx *via.Ctx) { panic("boom") }

func (p *panicLive) Ping(ctx *via.Ctx) {}

func (p *panicLive) View() h.H {
	return h.Div(p.n.Display(), h.Button(via.On("click", p.Boom)), h.Button(via.On("click", p.Ping)))
}

func TestDispatch_liveActionPanicAnswers500NotStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(panicLive{}))
		conn := app.Connect()

		status, _ := app.Action(0).Over(conn).Fire()
		assert.Equal(t, http.StatusInternalServerError, status, "a live action panic must answer 500")

		status2, _ := app.Action(1).Over(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status2, "the connection must survive the panic and keep dispatching")
	})
}

// branchy's second button only exists in the View after Reveal
// fires and its push renders it — the connect-time render and the initial
// plain GET both show only Reveal.
type branchy struct {
	shown via.State[bool]
	n     via.State[int]
}

func (b *branchy) Reveal(ctx *via.Ctx) { b.shown.Set(true) }

func (b *branchy) Extra(ctx *via.Ctx) {}

func (b *branchy) View() h.H {
	kids := []h.H{b.n.Display(), h.Button(via.On("click", b.Reveal))}
	if b.shown.Get() {
		kids = append(kids, h.Button(via.On("click", b.Extra), h.Str("extra")))
	}
	return h.Div(kids...)
}

// TestDispatch_liveActionAfterShapeChangeNeedsThePushedURL is C2: a
// connection's action table can change shape mid-connection (a push adds a
// button), and the URL for the new action only ever appears in what THIS
// connection pushed — never in the plain page vt cached before Connect.
// Sourcing it from Conn's own pushed markup (vt.Action.Live) finds it and
// dispatches successfully; sourcing it from the stale page (plain
// vt.Action) can't find action 1 at all, because the page vt fetched never
// had it.
func TestDispatch_liveActionAfterShapeChangeNeedsThePushedURL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(branchy{}))
		conn := app.Connect()

		status, _ := app.Action(0).Over(conn).Fire()
		require.Equal(t, http.StatusNoContent, status, "Reveal must run and push the new button")

		// The pushed element-patch carrying Extra's URL is read off the real
		// SSE socket by a goroutine synctest.Wait() can't settle (blocking
		// network I/O is not "durably blocked" — see I4); Await blocks for it
		// instead of racing the reader, which is what made this test flaky
		// under -cpu 1.
		conn.Await("extra")

		status, _ = app.Action(1).Over(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status,
			"Extra's URL only exists in what this connection pushed — vt.Action.Live must read it from there")

		// A plain GET is a fresh, unrelated instance (shown resets to
		// false) — it can never carry this connection's action 1, proving
		// plain vt.Action's plain-page lookup could not have found it
		// either; only Conn's own pushed markup has it.
		page := fetchPage(t, app, "/")
		assert.NotContains(t, page, "extra",
			"a fresh plain render never reflects the stream's Reveal")
	})
}

// splitActionURL cuts url into its /_via/a/ prefix, {child}, {act} and any
// query, so a test can forge one segment and keep the rest genuine.
func splitActionURL(t *testing.T, url string) (prefix, child, act, query string) {
	t.Helper()
	if i := strings.Index(url, "?"); i >= 0 {
		url, query = url[:i], url[i:]
	}
	j := strings.LastIndex(url, "/")
	require.GreaterOrEqual(t, j, 0)
	k := strings.LastIndex(url[:j], "/")
	require.GreaterOrEqual(t, k, 0)
	return url[:k+1], url[k+1 : j], url[j+1:], query
}

// swapActionID rewrites url's {act} segment to an id the render does not
// bind, keeping child, mount prefix and ?a= genuine.
func swapActionID(t *testing.T, url, act string) string {
	t.Helper()
	prefix, child, _, q := splitActionURL(t, url)
	return prefix + child + "/" + act + q
}

// swapChildIndex rewrites url's {child} segment, keeping a genuine action id
// on it — an id that resolves on ITS child must not resolve on another.
func swapChildIndex(t *testing.T, url, child string) string {
	t.Helper()
	prefix, _, act, q := splitActionURL(t, url)
	return prefix + child + "/" + act + q
}

func TestDispatch_unknownActionAnswers410OnEveryPath(t *testing.T) {
	t.Parallel()

	t.Run("plain root", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Handler(counter{count: &store{}}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, _ := do(t, srv, http.MethodPost, swapActionID(t, actionURL(t, page, "r", 1), "zzzzzzzz"), "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode)
	})

	t.Run("plain child", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Handler(sigPage{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, _ := do(t, srv, http.MethodPost, swapActionID(t, actionURL(t, page, "0", 0), "zzzzzzzz"), "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode)
	})

	// The entire body is the synctest bubble, so no t.Parallel() (its *testing.T
	// can't call it — see CONVENTIONS.md's synctest carve-out).
	t.Run("live unit", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			app := vt.Serve(t, via.Handler(liveRedirector{}))
			conn := app.Connect()
			page := fetchPage(t, app, "/")

			req, err := http.NewRequest(http.MethodPost, app.URL()+swapActionID(t, actionURL(t, page, "r", 0), "zzzzzzzz"), strings.NewReader(withTab(conn.TabID(), "{}")))
			require.NoError(t, err)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Datastar-Request", "true")
			resp, err := app.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusGone, resp.StatusCode)
		})
	})
}

// liveForm is a live root whose View carries a native PostForm — the case
// with no example coverage: a real browser form submit inside a live unit
// carries neither Datastar's signal store nor its headers, so it depends on PostForm's
// hidden fallback field to route to the connection at all.
type liveForm struct {
	got   via.State[string]
	calls *int
}

func (f *liveForm) Save(ctx *via.Ctx) {
	*f.calls++
	f.got.Set(ctx.Request().FormValue("name"))
}

func (f *liveForm) View() h.H {
	return h.Div(
		via.PostForm(f.Save, h.Input(h.Name("name")), h.Button(h.Str("save"))),
		h.Span(h.Str("got:"), f.got.Display()),
	)
}

// multipartForm builds a multipart/form-data body from fields, returning the
// body and its Content-Type — the encoding every native PostForm submit uses.
func multipartForm(t *testing.T, fields map[string]string) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	require.NoError(t, mw.Close())
	return &buf, mw.FormDataContentType()
}

// nativeFormPost submits a native form POST with no viatab signal — the
// one a real browser sends — carrying fields as its multipart body.
func nativeFormPost(t *testing.T, app *vt.App, url string, fields map[string]string) (int, string) {
	t.Helper()
	body, ctype := multipartForm(t, fields)
	req, err := http.NewRequest(http.MethodPost, app.URL()+url, body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(b)
}

func TestDispatch_liveFormFieldFallbackRunsHandlerAndReturns200(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		app := vt.Serve(t, via.Handler(liveForm{calls: &calls}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")
		assert.Contains(t, page, `data-attr:value="$viatab"`,
			"a live unit's PostForm must reactively fill the fallback field from the tab signal")
		formURL := actionURL(t, page, "r", 0)

		status, _ := nativeFormPost(t, app, formURL, map[string]string{
			"name":    "zed",
			"_viatab": conn.TabID(),
		})

		// The returned page is a fresh connection's page (see Child's godoc on
		// the native-submit contract), not a snapshot of the dying
		// connection's mutated liveForm — so it no longer carries "got:zed".
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, 1, calls, "the handler must still have run against the live unit's own state")
	})
}

func TestDispatch_liveFormFieldFromAnotherMountIsRejected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		aCalls, cCalls := 0, 0
		r := via.NewRouter()
		r.Mount("/a", liveForm{calls: &aCalls})
		r.Mount("/c", liveForm{calls: &cCalls})
		srv := liveServer(t, r)

		_, aPage := do(t, srv, http.MethodGet, "/a", "")
		aFormURL := actionURL(t, aPage, "r", 0)

		cLines, cancel := openStreamAt(t, srv, "/c/_via/sse")
		defer cancel()
		cTab := awaitTabID(t, cLines)
		synctest.Wait()

		body, ctype := multipartForm(t, map[string]string{"name": "zed", "_viatab": cTab})
		req, err := http.NewRequest(http.MethodPost, srv.URL+aFormURL, body)
		require.NoError(t, err)
		req.Header.Set("Content-Type", ctype)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusGone, resp.StatusCode, "a /c tab must not drive /a's form")
		assert.Zero(t, aCalls, "the /a handler must not have run")
	})
}

// nativeFormPanic's View panics on re-render once boom is flipped —
// reproducing a native <form> submit whose mutation succeeds but whose
// fresh-instance re-render (dispatchOverStream, native mode) then panics. boom is
// a shared pointer, not per-instance State: the fresh instance the render
// runs against is a different value from the one Save mutated, so only a
// pointer shared across instances can carry the trigger between them.
type nativeFormPanic struct {
	boom *bool
	n    via.State[int]
}

func (f *nativeFormPanic) Save(ctx *via.Ctx) { *f.boom = true }

func (f *nativeFormPanic) View() h.H {
	if *f.boom {
		panic("via_test: native re-render exploded")
	}
	return h.Div(f.n.Display(), via.PostForm(f.Save, h.Input(h.Name("name")), h.Button(h.Str("save"))))
}

// A panic in a native form's post-mutation re-render must answer 500, not
// hang the POST forever — the render runs on the dispatching POST's own
// goroutine (see dispatch.go's dispatchOverStream), outside liveRunAction's own
// recover, after the mutation already succeeded.
func TestDispatch_liveNativeFormPanicOnRerenderAnswers500NotHang(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := new(bool)
		app := vt.Serve(t, via.Handler(nativeFormPanic{boom: boom}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")
		formURL := actionURL(t, page, "r", 0)

		status, _ := nativeFormPost(t, app, formURL, map[string]string{
			"name":    "zed",
			"_viatab": conn.TabID(),
		})

		assert.Equal(t, http.StatusInternalServerError, status,
			"a panic in the native re-render must answer 500, not hang the POST forever")
	})
}

// validatedForm is a native PostForm whose handler records validation errors
// on ITSELF and re-seeds the submitted values — the ordinary server-rendered
// form. What the response must show is the instance Save mutated.
type validatedForm struct {
	Name via.Signal[string]
	err  string
}

func (f *validatedForm) Save(ctx *via.Ctx) {
	name := ctx.Request().FormValue("name")
	if name == "" {
		f.err = "name is required"
		return
	}
	f.err = "name already taken: " + name
	f.Name.Set(name)
}

func (f *validatedForm) errNote() h.H { return h.P(h.ID("err"), h.Str(f.err)) }

func (f *validatedForm) View() h.H {
	return via.PostForm(f.Save,
		h.Input(h.Name("name"), f.Name.Bind()),
		via.When(f.err != "", f.errNote),
		h.Button(h.Str("save")))
}

// formShell wraps any composition, so the form below is submitted from inside
// a via.Child rather than at the root.
type formShell[C any] struct{ Body C }

func (s *formShell[C]) View() h.H { return h.Main(via.Child(s.Body)) }

// A native form submit inside a Child must answer with the instance the
// handler MUTATED. The re-render walks from the root, and via.Child re-copies
// the parent's field on every render — so without carrying the acted instance
// down, the response is a pristine form: the validation error gone and the
// submitted value back to empty, as if the POST had never happened. The root
// case (below) always worked, which is what made this so easy to miss.
func TestNativeForm_insideAnChildKeepsTheHandlersMutations(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(formShell[validatedForm]{}))
	_, page := app.Get("/")
	url := actionURL(t, page, "0", 0)

	status, body := nativeFormPost(t, app, url, map[string]string{"name": "Widget"})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "name already taken: Widget", "the handler's validation error was discarded")
	assert.Contains(t, body, `"body__name":"Widget"`, "the submitted value was not re-seeded")
}

// The same form at the root, as the control: it must keep working exactly as
// before, so a fix that broke the root to fix the child cannot pass.
func TestNativeForm_atTheRootKeepsTheHandlersMutations(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(validatedForm{}))
	_, page := app.Get("/")
	status, body := nativeFormPost(t, app, actionURL(t, page, "r", 0), map[string]string{"name": "Widget"})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "name already taken: Widget")
	assert.Contains(t, body, `"name":"Widget"`)
}

// initCounter counts its own OnInit runs. Substituting the acted instance into
// the response re-render must NOT re-init it — re-initing is exactly what
// would reload the data the handler just changed — while a fresh sibling child
// still gets its own OnInit.
type initCounter struct {
	inits int
	saved string
}

func (c *initCounter) OnInit(ctx *via.Ctx) error { c.inits++; return nil }

func (c *initCounter) Save(ctx *via.Ctx) { c.saved = "true" }

func (c *initCounter) View() h.H {
	return via.PostForm(c.Save,
		h.P(h.ID("inits"), h.Str(c.inits)),
		h.P(h.ID("saved"), h.Str(c.saved)),
		h.Button(h.Str("go")))
}

func TestNativeForm_actedChildIsNotReInited(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(formShell[initCounter]{}))
	_, page := app.Get("/")
	require.Contains(t, page, `<p id="inits">1</p>`)

	status, body := nativeFormPost(t, app, actionURL(t, page, "0", 0), nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `<p id="saved">true</p>`, "the mutation must survive the re-render")
	assert.Contains(t, body, `<p id="inits">1</p>`, "the acted child's OnInit must not run a second time")
}

// A live page's native form submit that arrives WITHOUT the tab field is a
// missing-field failure, not a served-plain one — the page was live, which is
// precisely why the field had to be there. The old message asserted the
// opposite and sent people looking at their OnInit.
func TestNativeForm_missingTabFieldSaysWhatActuallyHappened(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(liveNamedForm{}))
		conn := app.Connect()
		status, body := nativeFormPost(t, app, conn.ActionURL("r", 0), map[string]string{"name": "zed"})
		assert.Equal(t, http.StatusGone, status)
		assert.Contains(t, body, "_viatab", "the answer must name the field that was missing")
		assert.NotContains(t, body, "served plain", "the page was live; that diagnosis is wrong")
	})
}

// liveNamedForm is a live unit whose only action is a native PostForm.
type liveNamedForm struct{ n via.State[int] }

func (f *liveNamedForm) Save(ctx *via.Ctx) { f.n.Set(f.n.Get() + 1) }

func (f *liveNamedForm) View() h.H {
	return via.PostForm(f.Save, f.n.Display(), h.Input(h.Name("name")), h.Button(h.Str("go")))
}

// signalGatedAdmin is the security repro: a Signal an OnInit fills from
// server-side identity, used as the condition of a via.When that renders the
// privileged rows. The POST body is fully attacker-controlled, so a discovery
// render hydrated from it would let an anonymous client open the branch, bind
// the row's (handler, arg) pair, and be authorized by the render it just forged.
//
// The gate sits inside a lazily-rendered builder (via.When's build here; an
// via.Each row or a Child's View is the same shape) — that is the only place a
// condition is evaluated DURING the render rather than while View() is being
// constructed, and therefore the only place a hydration that runs mid-render
// can reach it.
type signalGatedAdmin struct {
	Admin  via.Signal[bool]
	loaded bool
	gone   []int
}

func (a *signalGatedAdmin) OnInit(ctx *via.Ctx) error {
	a.Admin.Set(ctx.Request().Header.Get("X-Admin") == "yes")
	a.loaded = true
	return nil
}

func (a *signalGatedAdmin) Delete(ctx *via.Ctx, id int) { a.gone = append(a.gone, id) }

func (a *signalGatedAdmin) rows() h.H {
	return h.Ul(h.Li(h.Button(via.OnArg("click", a.Delete, 7), h.Str("delete 7"))))
}

func (a *signalGatedAdmin) panel() h.H { return via.When(a.Admin.Get(), a.rows) }

func (a *signalGatedAdmin) View() h.H {
	return h.Div(
		h.P(h.Str("deleted: "+fmt.Sprint(a.gone))),
		h.Input(a.Admin.Bind()),
		via.When(a.loaded, a.panel),
	)
}

// adminPost fires the delete action with body, optionally claiming admin via
// the header OnInit actually trusts.
func adminPost(t *testing.T, srv *httptest.Server, url, body string, admin bool) (*http.Response, string) {
	t.Helper()
	hdr := map[string]string{"Sec-Fetch-Site": "same-origin"}
	if admin {
		hdr["X-Admin"] = "yes"
	}
	return post(t, srv, url, body, hdr)
}

func TestDispatchPlain_postedSignalCannotOpenAServerGatedBranch(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(signalGatedAdmin{}))

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("X-Admin", "yes")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	page := readAll(t, resp)
	url := actionURL(t, page, "r", 0)
	require.Contains(t, url, "a=7")

	anon, body := adminPost(t, srv, url, `{"admin":true}`, false)
	assert.Equal(t, http.StatusGone, anon.StatusCode,
		"a posted signal must not open the branch that authorizes the action")
	assert.NotContains(t, body, "deleted: [7]")
}

func TestDispatchPlain_serverGatedBranchStillDispatchesForAnAuthorizedCaller(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(signalGatedAdmin{}))

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("X-Admin", "yes")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	url := actionURL(t, readAll(t, resp), "r", 0)

	ok, body := adminPost(t, srv, url, `{"admin":true}`, true)
	assert.Equal(t, http.StatusOK, ok.StatusCode)
	assert.Contains(t, body, "deleted: [7]")
}

// echoedSignal is the control for the hydration reorder: a plain action must
// still SEE the value the client posted, it just must not let that value
// rewrite the render that authorized it.
type echoedSignal struct {
	Name via.Signal[string]
	saw  string
}

func (e *echoedSignal) Save(ctx *via.Ctx) { e.saw = e.Name.Get() }

func (e *echoedSignal) View() h.H {
	return h.Div(h.P(h.Str("saw: "+e.saw)), h.Input(e.Name.Bind()), h.Button(via.On("click", e.Save)))
}

func TestDispatchPlain_actionStillReadsThePostedSignal(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(echoedSignal{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), `{"name":"zed"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "saw: zed")
}

// plainRootLiveKid is a PLAIN root (its own View renders no State) carrying a
// LIVE child, plus a native PostForm at the root. The submit falls to
// dispatchPlain — the root is not a registered live unit — and the full page it
// answers with is what the browser replaces the document with, so it must still
// bootstrap the stream the live child needs.
type plainRootLiveKid struct {
	Clock embeddedClock
	name  string
}

func (p *plainRootLiveKid) Save(ctx *via.Ctx) { p.name = ctx.Request().FormValue("name") }

func (p *plainRootLiveKid) View() h.H {
	return h.Div(
		h.P(h.ID("name"), h.Str(p.name)),
		via.PostForm(p.Save, h.Input(h.Name("name")), h.Button(h.Str("go"))),
		via.Child(p.Clock),
	)
}

type embeddedClock struct{ n via.State[int] }

func (c *embeddedClock) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, c.beat)
	return nil
}

func (c *embeddedClock) beat(ctx *via.Ctx) { c.n.Set(c.n.Get() + 1) }

func (c *embeddedClock) View() h.H { return h.Div(c.n.Display()) }

func TestNativeForm_plainRootKeepsALiveChildsBootstrap(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(plainRootLiveKid{}))
	_, page := app.Get("/")
	require.Contains(t, page, "data-init", "the GET must already bootstrap the stream")

	status, body := nativeFormPost(t, app, actionURL(t, page, "r", 0), map[string]string{"name": "zed"})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `<p id="name">zed</p>`)
	assert.Contains(t, body, "data-init", "the live child is dead after the submit without a bootstrap")
	assert.Contains(t, body, "/_via/sse", "the bootstrap must name the stream URL")
}

// disclosure is the two-pass discovery repro: a Bind()ed Signal (Mode) chosen
// by the user opens a lazy branch holding a second Bind()ed Signal (Name) and
// an in-branch handler. keepalive makes the very same composition live, so both
// transports are driven through one shape; it never changes after Handler, so
// the liveness verdict stays render-invariant. OnInit seeds Mode from the query
// only so a test can capture the URL of an in-branch action from an open
// render — an action POST carries no query, so the discovery render reopens
// that branch from the posted signal or not at all.
type disclosure struct {
	Mode      via.Signal[string]
	Name      via.Signal[string]
	keepalive bool
	n         via.State[int]
	seen      string
	revealed  bool
}

func (d *disclosure) OnInit(ctx *via.Ctx) error {
	d.Mode.Set(ctx.Request().URL.Query().Get("mode"))
	return nil
}

func (d *disclosure) Pick(ctx *via.Ctx) {}

func (d *disclosure) Save(ctx *via.Ctx) { d.seen = "saw:" + d.Name.Get() }

func (d *disclosure) Reveal(ctx *via.Ctx) { d.revealed = true }

func (d *disclosure) form() h.H {
	return h.Div(h.Input(d.Name.Bind()), h.Button(via.On("click", d.Reveal), h.Str("reveal")))
}

func (d *disclosure) body() h.H { return via.When(d.Mode.Get() == "x", d.form) }

func (d *disclosure) beat() h.H { return d.n.Display() }

func (d *disclosure) View() h.H {
	return h.Div(
		h.Select(d.Mode.Bind(), via.On("change", d.Pick)),
		via.When(true, d.body),
		h.P(h.Str("seen: "+d.seen)),
		h.P(h.Str("revealed: "+fmt.Sprint(d.revealed))),
		h.Button(via.On("click", d.Save), h.Str("save")),
		via.When(d.keepalive, d.beat),
	)
}

func TestDispatchPlain_hydratesASignalInABranchAnotherPostedSignalOpens(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(disclosure{}))

	code, body := app.Action(1).Body(`{"mode":"x","name":"bob"}`).Fire()
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "seen: saw:bob",
		"a Bind()ed signal inside a branch another posted signal opens must still reach the handler")
	assert.NotContains(t, body, `"name":""`,
		"the response must not wipe the client's value for that slot")
}

func TestDispatchPlain_inBranchActionStaysUndispatchableWithoutTheServerRender(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(disclosure{}))

	_, open := app.Get("/?mode=x")
	url := actionURL(t, open, "r", 1)

	code, body := app.Action(0).Raw(url).Body(`{"mode":"x"}`).Fire()
	assert.Equal(t, http.StatusGone, code,
		"a handler is dispatchable only if the render the client did not influence bound it")
	assert.Contains(t, body, "does not bind it")
}

func TestDispatchLive_hydratesADisclosedSignalAfterTheBranchIsOpened(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(disclosure{keepalive: true}))
	conn := app.Connect()

	code, _ := app.Action(0).Over(conn).Body(`{"mode":"x"}`).Fire()
	require.Less(t, code, 300, "the disclosure toggle must be accepted")
	conn.Await("reveal")

	code, _ = app.Action(2).Over(conn).Body(`{"mode":"x","name":"bob"}`).Fire()
	require.Less(t, code, 300)
	assert.Contains(t, conn.Await("seen: saw:bob"), "seen: saw:bob")
}

// Renamed from ...UntilTheBranchIsPushed: a push no longer authorizes it
// either. The branch is opened by a Bind()ed signal, so it is the client's
// render, not the server's — see the posted-signal tests at the end of this
// file.
func TestDispatchLive_inBranchActionStaysUndispatchable(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(disclosure{keepalive: true}))
	conn := app.Connect()

	_, open := app.Get("/?mode=x")
	url := actionURL(t, open, "r", 1)

	code, body := app.Action(0).Over(conn).Raw(url).Body(`{"mode":"x"}`).Fire()
	assert.Equal(t, http.StatusGone, code)
	assert.Contains(t, body, "does not bind it")
}

// --- bounded-fixpoint discovery regressions (one root cause, three symptoms).
//
// The disclosure fixture above has no child, no session and no OnArg, which is
// exactly why all three slipped through: every one of them needs a SECOND
// discovery pass, which any page with a Bind()ed slot in the posted body takes.

// fixKid is a PLAIN embedded child whose OnInit seeds the state its handler
// reads — the state a later discovery pass used to skip.
type fixKid struct {
	loaded string
	seen   string
}

func (k *fixKid) OnInit(ctx *via.Ctx) error { k.loaded = "init"; return nil }

func (k *fixKid) Save(ctx *via.Ctx) { k.seen = "seen:" + k.loaded }

func (k *fixKid) View() h.H {
	return h.Div(h.P(h.Str(k.seen)), h.Button(via.On("click", k.Save), h.Str("save")))
}

type fixParent struct {
	Q via.Signal[string]
	K fixKid
}

func (p *fixParent) View() h.H { return h.Div(h.Input(p.Q.Bind()), via.Child(p.K)) }

func TestDispatchPlain_childActionRunsOnAnInitedCopyAfterASecondPass(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(fixParent{}))

	code, body := app.ChildAction("0", 0).Body(`{"q":"x"}`).Fire()
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "<p>seen:init</p>",
		"a posted Bind()ed signal forces a second discovery pass; the child's action must still run on a copy whose OnInit ran")
}

// argRows widens its row set from a Bind()ed signal, so a posted body can
// conjure a row the auth render never produced.
type argRows struct {
	Filter via.Signal[string]
	del    string
}

func (r *argRows) Del(ctx *via.Ctx, id int) { r.del = "del:" + strconv.Itoa(id) }

func (r *argRows) ids() []int {
	if r.Filter.Get() == "all" {
		return []int{1, 2, 3}
	}
	return []int{1}
}

func (r *argRows) View() h.H {
	return h.Div(
		h.Input(r.Filter.Bind()),
		h.Ul(via.Each(r.ids(), func(id int) h.H {
			return h.Li(h.Button(via.OnArg("click", r.Del, id), h.Str("del")))
		})),
		h.P(h.Str(r.del)),
	)
}

func TestDispatchPlain_postedSignalCannotWidenAnActionsArgSet(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(argRows{}))
	_, page := app.Get("/")
	url := strings.Replace(actionURL(t, page, "r", 0), "a=1", "a=3", 1)
	require.Contains(t, url, "a=3")

	code, body := app.Action(0).Raw(url).Body(`{"filter":"all"}`).Fire()
	assert.Equal(t, http.StatusGone, code,
		"the executed render is an intersection with auth per (handler, arg), not per handler")
	assert.NotContains(t, body, "del:3")
}

// shiftA and shiftB promote Hit from a shared embedded base at offset 0, so
// both mint the SAME content-addressed action id (the id hashes the Go func
// name plus the receiver's offset, and here both are identical). That is the
// only arrangement in which a child key denoting different types in the auth
// and the bind render gets past the action lookup at all.
type shiftBase struct{ hit bool }

func (s *shiftBase) Hit(ctx *via.Ctx) { s.hit = true }

type shiftA struct{ shiftBase }

func (a *shiftA) View() h.H { return h.Div(h.Str("A"), h.Button(via.On("click", a.Hit))) }

type shiftB struct{ shiftBase }

func (b *shiftB) View() h.H { return h.Div(h.Str("B"), h.Button(via.On("click", b.Hit))) }

// Flip is Bind()ed, so the POST body can flip it — and the When around Child(A)
// then shifts B from key "1" up to key "0". via.Child's docs forbid exactly
// this When; the dispatcher must still fail closed rather than run A's
// authorized action against B.
type shiftPage struct {
	Flip via.Signal[bool]
	A    shiftA
	B    shiftB
}

func (p *shiftPage) View() h.H {
	return h.Div(
		h.Input(p.Flip.Bind()),
		via.When(!p.Flip.Get(), func() h.H { return via.Child(p.A) }),
		via.Child(p.B),
	)
}

func TestDispatchPlain_childKeyThatChangesTypeBetweenPassesIsGone(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(shiftPage{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	aURL, bURL := actionURL(t, page, "0", 0), actionURL(t, page, "1", 0)
	require.Equal(t, aURL[strings.LastIndexByte(aURL, '/'):], bURL[strings.LastIndexByte(bURL, '/'):],
		"the fixture only bites if both children mint the same action id")

	resp, body := do(t, srv, http.MethodPost, aURL, `{"flip":true}`)
	assert.Equal(t, http.StatusGone, resp.StatusCode,
		"a key whose type changed between the auth and bind renders must not dispatch")
	assert.Contains(t, body, "no such child")
}

// scopeKid's OnInit reads the REQUEST, and its View Bind()s a slot the POST
// body carries — so the discovery loop takes a second pass and re-inits this
// child off the cloned Ctx. A pass-2 Ctx with no request scope is the defect
// class the clone exists to close.
type scopeKid struct {
	Q    via.Signal[string]
	from string
	seen string
}

func (k *scopeKid) OnInit(ctx *via.Ctx) error { k.from = ctx.Request().Method; return nil }

func (k *scopeKid) Save(ctx *via.Ctx) { k.seen = "method:" + k.from }

func (k *scopeKid) View() h.H {
	return h.Div(h.Input(k.Q.Bind()), h.P(h.Str(k.seen)), h.Button(via.On("click", k.Save), h.Str("save")))
}

type scopePage struct{ K scopeKid }

func (p *scopePage) View() h.H { return h.Div(via.Child(p.K)) }

func TestDispatchPlain_laterPassCarriesTheRequestScopeIntoChildOnInit(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(scopePage{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "0", 0), `{"k__q":"x"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"a hydration pass >= 2 must carry the request, not re-init children against a nil one")
	assert.Contains(t, body, "method:POST")
}

var actionIDRe = regexp.MustCompile(`/_via/a/([^')]+)`)

func actionID(t *testing.T, body string) string {
	t.Helper()
	m := actionIDRe.FindStringSubmatch(body)
	require.NotNil(t, m, "no action endpoint in page")
	return m[1]
}

// liveReqEchoer is a live child whose action copies a header off the request
// that triggered it into State.
type liveReqEchoer struct{ echo via.State[string] }

func (e *liveReqEchoer) Grab(ctx *via.Ctx) { e.echo.Set(ctx.Request().Header.Get("X-Echo")) }

func (e *liveReqEchoer) View() h.H {
	return h.Div(h.P(h.Str("echo: "), e.echo.Display()), h.Button(via.On("click", e.Grab), h.Str("x")))
}

// A live action runs on the stream goroutine, yet it must still see the request
// that TRIGGERED it — the action POST. That POST carried X-Echo; the connect
// request never did, so the value surfacing over the SSE proves the triggering
// action request is threaded through (not the connect request).
func TestLiveAction_seesTheTriggeringActionRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(liveReqEchoer{}))
		conn := app.Connect()

		// X-Echo has no vt.Action builder method, so this posts by hand — but
		// the URL and tab still come off the connection, not a separate GET.
		req, err := http.NewRequest(http.MethodPost, app.URL()+conn.ActionURL("r", 0), strings.NewReader(withTab(conn.TabID(), "{}")))
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Datastar-Request", "true")
		req.Header.Set("X-Echo", "from-the-action-post")
		resp, err := app.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		conn.Await("echo: from-the-action-post")
	})
}

// renderCounter counts its own View calls so a test can assert on renders
// without reaching into via's internals — views is a pointer so it survives
// Handler's per-connection value copy.
type renderCounter struct {
	views *atomic.Int64
	count via.State[int]
}

func (r *renderCounter) Bump(*via.Ctx) { r.count.Set(r.count.Get() + 1) }

func (r *renderCounter) View() h.H {
	r.views.Add(1)
	return h.Div(h.P(h.Str("count: "), r.count.Display()), h.Button(via.On("click", r.Bump)))
}

// A live action must run against the last render's table, not a fresh one of
// its own: connect renders once, and the action's own push renders once —
// never a bind render in between just to locate the action.
func TestLive_actionRunsWithoutPreRender(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		views := &atomic.Int64{}
		srv := liveServer(t, via.Handler(renderCounter{views: views}))

		lines, cancel := openStream(t, srv)
		defer cancel()

		tab := awaitTabID(t, lines)
		require.NotEmpty(t, tab)
		require.EqualValues(t, 1, views.Load(), "the connect render is the only render so far")

		// A throwaway instance (its own views counter) discovers the current
		// action URL without adding a render to the instance under test — the
		// whole point of this test is counting THAT instance's View calls.
		digestSrv := liveServer(t, via.Handler(renderCounter{views: &atomic.Int64{}}))
		_, page := do(t, digestSrv, http.MethodGet, "/", "")

		resp, _ := post(t, srv, actionURL(t, page, "r", 0), withTab(tab, "{}"), map[string]string{
			"Sec-Fetch-Site": "same-origin",
		})
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		awaitLine(t, lines, "count: 1")
		assert.EqualValues(t, 2, views.Load(), "a live action renders once — for the push — not twice")
	})
}

// An action id this render does not bind (a click racing a push that closed
// the branch) must 410, not silently do nothing — the client can then
// re-bootstrap instead of a click quietly having no effect.
func TestLive_unknownActionAnswers410(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(clicker{}))
		conn := app.Connect()
		require.NotEmpty(t, conn.TabID())

		url := swapActionID(t, conn.ActionURL("r", 0), "zzzzzzzz")
		status, _ := app.Action(0).Raw(url).Over(conn).Fire()
		assert.Equal(t, http.StatusGone, status)
	})
}

// liveArg is a live child with one value-carrying action, so a malformed
// ?a= can be exercised on the live dispatch path too (dispatchPlain has
// its own via_test coverage).
type liveArg struct{ last via.State[int] }

func (l *liveArg) Set(ctx *via.Ctx, v int) { l.last.Set(v) }

func (l *liveArg) View() h.H {
	return h.Div(l.last.Display(), h.Button(via.OnArg("click", l.Set, 7)))
}

// A malformed ?a= on a LIVE action must answer 400, not run the handler with
// a zero value nor 500 — and the stream goroutine must survive to answer a
// later, well-formed action normally.
func TestLive_malformedActionArgAnswers400(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(liveArg{}))
		conn := app.Connect()

		url := strings.Replace(conn.ActionURL("r", 0), "a=7", "a=%22bad%22", 1)
		status, _ := app.Action(0).Raw(url).Over(conn).Fire()
		assert.Equal(t, http.StatusBadRequest, status)

		status, _ = app.Action(0).Over(conn).Fire() // well-formed, same slot
		assert.Equal(t, http.StatusNoContent, status, "the stream goroutine must still be alive")
		conn.Await("7")
	})
}

// Neighbour of the malformed-arg case: a missing ?a= (empty, or "null") on a
// LIVE action must also answer 400, not run Set with the zero value.
func TestLive_missingActionArgAnswers400(t *testing.T) {
	tests := []struct {
		name string
		a    string
	}{
		{"empty", "a="},
		{"null", "a=null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				app := vt.Serve(t, via.Handler(liveArg{}))
				conn := app.Connect()

				url := strings.Replace(conn.ActionURL("r", 0), "a=7", tt.a, 1)
				status, _ := app.Action(0).Raw(url).Over(conn).Fire()
				assert.Equal(t, http.StatusBadRequest, status)

				status, _ = app.Action(0).Over(conn).Fire() // well-formed, same slot
				assert.Equal(t, http.StatusNoContent, status, "the stream goroutine must still be alive")
				conn.Await("7")
			})
		})
	}
}

// --- F-1: a live action that FAILS after hydration must not leave the posted
// values on the instance.
//
// liveRunAction hydrates every posted slot into the instance BEFORE running the
// handler, and the only other restore is livePush's — which never runs when the
// action returns without a push. A malformed ?a= is the attacker-reachable way
// in: the arg decode panics from INSIDE act.fn, after hydration. The sibling
// property for the push path is
// TestConnect_aTickHandlerNeverSeesThePostedSignalValue.

type tickAfterBadArg struct {
	Idx  via.Signal[int]
	seen int
}

func (p *tickAfterBadArg) OnInit(ctx *via.Ctx) error {
	ctx.Tick(5*time.Millisecond, func(*via.Ctx) { p.seen = p.Idx.Get() })
	return nil
}

func (p *tickAfterBadArg) Bump(ctx *via.Ctx, v int) {}

func (p *tickAfterBadArg) View() h.H {
	return h.Div(
		h.Input(p.Idx.Bind()),
		h.Button(via.OnArg("click", p.Bump, 7)),
		p.Idx.Display(),
		h.P(h.Str("seen: "+strconv.Itoa(p.seen))),
	)
}

func TestDispatchLive_aFailedActionLeavesNoPostedValueOnTheInstance(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(tickAfterBadArg{}))
	conn := app.Connect()
	require.Contains(t, conn.Await("seen: "), "seen: 0")

	url := strings.Replace(conn.ActionURL("r", 0), "a=7", "a=%22bad%22", 1)
	status, _ := app.Action(0).Raw(url).Body(`{"idx":99}`).Over(conn).Fire()
	require.Equal(t, http.StatusBadRequest, status)

	// Drained rather than counted: the 5ms tick keeps frames in the pipe that
	// were rendered BEFORE the dispatch, and under load there are more than the
	// two this used to assume — so "the second frame" was sometimes a stale one
	// that had never seen the posted value. The invariant under test holds on
	// every frame regardless; only the display value needs the first
	// post-dispatch frame.
	var frame string
	for range 20 {
		frame = conn.Await("seen: ")
		assert.Contains(t, frame, "seen: 0", "a Tick handler read the posted value off the instance: %s", frame)
		if strings.Contains(frame, ">99<") {
			break
		}
	}
	// The client still sees what it posted — the display render is unchanged.
	assert.Contains(t, frame, ">99<", "the display render must still show what the client posted")
}

// abandonedAction is dispatched with a request context already canceled
// before ServeHTTP is called, isolating tabStream.run's first select
// (pulse-send vs. reqCtx.Done, both ready at once) from real round-trip
// timing noise.
type abandonedAction struct {
	applied *atomic.Int32 // shared across Handler's per-connection copy and the test's own handle
	n       via.State[int]
}

func (a *abandonedAction) Act(ctx *via.Ctx) { a.applied.Add(1) }

func (a *abandonedAction) View() h.H {
	return h.Div(a.n.Display(), h.Button(via.On("click", a.Act)))
}

// A live action must never mutate state once its caller has given up on it.
// Before the fix, a closure handed to the stream goroutine (tabStream.run)
// ran to completion regardless of whether the request's context was already
// done when the goroutine picked it up.
func TestLiveAction_abandonedRequestNeverAppliesAfterClientGivesUp(t *testing.T) {
	t.Parallel()
	root := &abandonedAction{applied: new(atomic.Int32)}
	handler := via.Handler(*root)
	srv := liveServer(t, handler)

	lines, cancel := openStream(t, srv)
	defer cancel()
	tab := awaitTabID(t, lines)

	_, page := do(t, srv, http.MethodGet, "/", "")
	actURL := actionURL(t, page, "r", 0)

	const n = 3000
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // abandoned before the request is even dispatched
			req := httptest.NewRequest(http.MethodPost, actURL, strings.NewReader(withTab(tab, "{}"))).WithContext(ctx)
			req.Header.Set("Datastar-Request", "true")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			handler.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(0), root.applied.Load(),
		"an action dispatched with an already-canceled request context must never apply")
}

// withTab splices the tab id into a JSON signal body. The tab id is an
// ordinary Datastar signal now, so a live action carries it in the POST body
// exactly as the browser's signal store does — not as a header.
func withTab(tab, body string) string {
	sig := map[string]json.RawMessage{}
	if body != "" {
		_ = json.Unmarshal([]byte(body), &sig)
	}
	sig["viatab"], _ = json.Marshal(tab)
	out, _ := json.Marshal(sig)
	return string(out)
}

// --- F1: the live path's action table is the render the client did not touch.
//
// A live unit outlives the request, so a Bind()ed signal's posted value used to
// land in the instance and stay there: it opened a gated branch, the branch's
// handlers entered the connection's table, and they dispatched. The plain path
// has always refused this (auth ∩ bind); these prove the live path now does too
// — while still SHOWING the client the branch its own signals opened, which is
// what keeps two-way binding usable.

type livePriv struct {
	live     bool
	IsAdmin  via.Signal[bool]
	nuked    bool
	nukedArg int
	safeN    int
}

func (p *livePriv) OnInit(ctx *via.Ctx) error {
	// The only authority on privilege: server state, never the posted signal.
	p.IsAdmin.Set(ctx.Request().URL.Query().Get("admin") == "1")
	if p.live {
		ctx.Tick(time.Hour, func(*via.Ctx) {}) // liveness without a push of its own
	}
	return nil
}

// Safe counts its calls and the View renders the count: an action whose render
// is byte-identical to the last frame now ships no frame at all (see
// skipUnchanged), so a test that reads its assertions off the NEXT frame has to
// give the render something that actually moves.
func (p *livePriv) Safe(ctx *via.Ctx)            { p.safeN++ }
func (p *livePriv) Nuke(ctx *via.Ctx)            { p.nuked = true }
func (p *livePriv) NukeArg(ctx *via.Ctx, id int) { p.nukedArg = id }

func (p *livePriv) admin() h.H {
	return h.Div(
		h.Button(via.On("click", p.Nuke), h.Str("nuke")),
		h.Button(via.OnArg("click", p.NukeArg, 42), h.Str("nukearg")),
	)
}

func (p *livePriv) View() h.H {
	return h.Div(
		h.Input(p.IsAdmin.Bind()),
		h.Button(via.On("click", p.Safe), h.Str("safe")),
		via.When(p.IsAdmin.Get(), p.admin),
		h.P(h.Str("nuked: "+fmt.Sprint(p.nuked)+"/"+fmt.Sprint(p.nukedArg)+" safe: "+fmt.Sprint(p.safeN))),
	)
}

func TestConnect_postedSignalsCannotOpenAGatedBranchsActions(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(livePriv{live: true}))
	conn := app.ConnectWith(`{"isAdmin":true}`)

	// Connect frames no elements, so drive one push: the display render is
	// where the connect body is allowed to show.
	code, _ := app.Action(0).Over(conn).Fire()
	require.Equal(t, http.StatusNoContent, code, "the ungated action must still work")
	require.Contains(t, conn.Await("nuke"), "nuke", "the client may still SEE what its own signals opened")

	for n, what := range map[int]string{1: "the gated action", 2: "its gated OnArg arg"} {
		url := conn.ActionURL("r", n)
		code, body := app.Action(0).Over(conn).Raw(url).Body(`{"isAdmin":true}`).Fire()
		assert.Equal(t, http.StatusGone, code, what+" must not be dispatchable: "+url)
		assert.Contains(t, body, "does not bind it")
	}
	// One more legitimate action, so there is a fresh frame to read the flags off.
	require.Equal(t, http.StatusNoContent, mustFire(t, app.Action(0).Over(conn).Body(`{"isAdmin":true}`)))
	assert.Contains(t, conn.Await("nuked:"), "nuked: false/0", "no gated handler may have run")
}

func TestDispatchLive_postedSignalsCannotOpenAGatedBranchsActions(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(livePriv{live: true}))
	conn := app.Connect()

	// A clean connect, then the forgery rides on an action the client IS
	// allowed to call — the second door, and the one a connect-only fix misses.
	code, _ := app.Action(0).Over(conn).Body(`{"isAdmin":true}`).Fire()
	require.Equal(t, http.StatusNoContent, code)
	require.Contains(t, conn.Await("nuke"), "nuke")

	for n := 1; n <= 2; n++ {
		url := conn.ActionURL("r", n)
		code, body := app.Action(0).Over(conn).Raw(url).Body(`{"isAdmin":true}`).Fire()
		assert.Equal(t, http.StatusGone, code, "action %d must not be dispatchable: %s", n, url)
		assert.Contains(t, body, "does not bind it")
	}
	require.Equal(t, http.StatusNoContent, mustFire(t, app.Action(0).Over(conn).Body(`{"isAdmin":true}`)))
	assert.Contains(t, conn.Await("nuked:"), "nuked: false/0")
}

// mustFire fires a, returning only the status: the live path answers 204 with
// no body, and the frame is what carries the render.
func mustFire(t *testing.T, a *vt.Action) int {
	t.Helper()
	code, _ := a.Fire()
	return code
}

// The plain control: the identical attack, already answered 410 before this
// change, and the behaviour the live path is now held to.
func TestDispatchPlain_postedSignalsCannotOpenAGatedBranchsActions(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(livePriv{}))
	_, privileged := app.Get("/?admin=1")

	for n := 1; n <= 2; n++ {
		url := actionURL(t, privileged, "r", n)
		code, body := app.Action(0).Raw(url).Body(`{"isAdmin":true}`).Fire()
		assert.Equal(t, http.StatusGone, code, "action %d must not be dispatchable: %s", n, url)
		assert.Contains(t, body, "does not bind it")
	}
}

// --- F3: a Param that no longer decodes is a 404 on either path, never a 500.

type paramLive struct{ live bool }

func (p *paramLive) OnInit(ctx *via.Ctx) error {
	if p.live {
		ctx.Tick(time.Hour, func(*via.Ctx) {})
	}
	return nil
}
func (p *paramLive) Boom(ctx *via.Ctx) { _ = ctx.Param[int]("id") }
func (p *paramLive) View() h.H         { return h.Div(h.Button(via.On("click", p.Boom), h.Str("b"))) }

func TestDispatch_paramMissIsA404OnBothPaths(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/p/{id}", paramLive{})
	r.Mount("/l/{id}", paramLive{live: true})
	app := vt.Serve(t, r)

	_, plain := app.Get("/p/notanint")
	code, body := app.Action(0).Raw(actionURL(t, plain, "r", 0)).Fire()
	require.Equal(t, http.StatusNotFound, code)
	require.Contains(t, body, "not found")

	_, live := app.Get("/l/notanint")
	conn := app.ConnectAt("/l/notanint", "{}")
	code, body = app.Action(0).Over(conn).Raw(actionURL(t, live, "r", 0)).Fire()
	assert.Equal(t, http.StatusNotFound, code, "the live path must answer a paramMiss the way the plain path does")
	assert.Contains(t, body, "not found")
}

// --- F1 regression: two live units on one connection.
//
// The revert set used to be reallocated per push and stored on the CONNECTION,
// while a hydrator closure notes its undo into the rev of the Ctx that bound
// the signal. So a SECOND live unit pushing in between left the first unit
// pointing at a set nothing restores: its next action's posted value survived
// into the AUTHORITY render and opened the gated branch for real. The
// single-unit tests above all pass against that bug — this shape is the one
// that catches it, and it is the README's canonical composition.

type livePrivClock struct{ n int }

func (c *livePrivClock) OnInit(ctx *via.Ctx) error {
	ctx.Tick(5*time.Millisecond, c.tick)
	return nil
}
func (c *livePrivClock) tick(*via.Ctx) { c.n++ }
func (c *livePrivClock) View() h.H     { return h.Div(h.Str("beat: " + fmt.Sprint(c.n))) }

type twoLivePage struct {
	Priv  livePriv
	Clock livePrivClock
}

func (p *twoLivePage) View() h.H { return h.Div(via.Child(p.Priv), via.Child(p.Clock)) }

var beatRE = regexp.MustCompile(`beat: (\d+)`)

// awaitBeat blocks until the Clock has pushed a beat >= n, discarding Priv's
// own frames on the way — the point is only that the sibling kept pushing
// between two of Priv's attempts, not which exact frame carried it.
func awaitBeat(t *testing.T, conn *vt.Conn, n int) {
	t.Helper()
	for {
		m := beatRE.FindStringSubmatch(conn.Await("beat: "))
		require.NotNil(t, m)
		got, err := strconv.Atoi(m[1])
		require.NoError(t, err)
		if got >= n {
			return
		}
	}
}

func TestDispatchLive_aSiblingUnitsPushCannotStrandTheRevertSet(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(twoLivePage{Priv: livePriv{live: true}}))
	conn := app.Connect()

	// Looped rather than fired once: the stranding bug is a race between the
	// sibling's push swapping the connection's revert set out and Priv's own
	// hydration noting its undo into it, so pinning ONE interleaving pins a
	// schedule, not the invariant. Three rounds, each with a fresh beat in
	// between, is the same attack against three different orderings.
	for beat := 1; beat <= 3; beat++ {
		awaitBeat(t, conn, beat)

		// The forgery rides on an action Priv IS allowed to call.
		code, _ := app.ChildAction("0", 0).Over(conn).Body(`{"priv__isAdmin":true}`).Fire()
		require.Equal(t, http.StatusNoContent, code)
		require.Contains(t, conn.Await("nuke"), "nuke", "the client may still SEE the branch its own signals opened")

		for n, what := range map[int]string{1: "the gated action", 2: "its gated OnArg arg"} {
			url := conn.ActionURL("0", n)
			code, body := app.Action(0).Over(conn).Raw(url).Body(`{"priv__isAdmin":true}`).Fire()
			assert.Equal(t, http.StatusGone, code, "round %d: %s must not be dispatchable: %s", beat, what, url)
			assert.Contains(t, body, "does not bind it")
		}
	}
	require.Equal(t, http.StatusNoContent,
		mustFire(t, app.ChildAction("0", 0).Over(conn).Body(`{"priv__isAdmin":true}`)))
	assert.Contains(t, conn.Await("nuked:"), "nuked: false/0", "no gated handler may have run")
}

// --- F2: a Tick/Listen handler sees the SERVER's value, not the client's.
//
// The handler runs between a display render and the next push, straight against
// the live instance. The display render applies the client's posted signals to
// that instance, so without a restore at the END of a push the handler read
// attacker-controlled data as input.

type tickReadsSignal struct {
	Name via.Signal[string]
	seen string
	beat int // rendered so consecutive tick frames differ; see skipUnchanged
}

func (p *tickReadsSignal) OnInit(ctx *via.Ctx) error {
	ctx.Tick(5*time.Millisecond, func(*via.Ctx) { p.seen, p.beat = p.Name.Get(), p.beat+1 })
	return nil
}
func (p *tickReadsSignal) View() h.H {
	return h.Div(h.Input(p.Name.Bind()), p.Name.Display(),
		h.P(h.Str("seen: ["+p.seen+"] beat "+strconv.Itoa(p.beat))))
}

func TestConnect_aTickHandlerNeverSeesThePostedSignalValue(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(tickReadsSignal{}))
	conn := app.ConnectWith(`{"name":"ATTACKER"}`)

	// The first tick pushes; the second is the one that reads the instance
	// AFTER a display render has applied the connect body to it.
	require.Contains(t, conn.Await("seen: ["), "seen: []")
	frame := conn.Await("seen: [")
	assert.Contains(t, frame, "seen: []", "the handler read the client's value: %s", frame)
	// The client must still SEE what it posted — the display render is unchanged.
	assert.Contains(t, frame, ">ATTACKER<")
}

// --- F-1: a panic in the DISPLAY render must not strand the client's values
// on the live instance.
//
// runPushItem swallows the panic and the stream survives, so the restore that
// undoes the display render's hydration has to be a defer, not a trailing call.
// Otherwise the next Tick handler reads the posted value straight off the
// instance — the F2 class, through a crashing View instead of a push.

type tickAfterDisplayPanic struct {
	Idx   via.Signal[int]
	seen  int
	beat  int  // rendered so consecutive tick frames differ; see skipUnchanged
	blown bool // the display render panics ONCE, so a later push can frame what the handler saw
}

func (p *tickAfterDisplayPanic) OnInit(ctx *via.Ctx) error {
	ctx.Tick(5*time.Millisecond, func(*via.Ctx) { p.seen, p.beat = p.Idx.Get(), p.beat+1 })
	return nil
}

func (p *tickAfterDisplayPanic) View() h.H {
	if p.Idx.Get() != 0 && !p.blown {
		p.blown = true
		panic("via_test: index out of range in the display render")
	}
	return h.Div(h.Input(p.Idx.Bind()),
		h.P(h.Str("seen: "+strconv.Itoa(p.seen)+" beat "+strconv.Itoa(p.beat))))
}

func TestConnect_aPanickingDisplayRenderLeavesNoPostedValueOnTheInstance(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(tickAfterDisplayPanic{}))
	conn := app.ConnectWith(`{"idx":99}`)

	// The first push's display render panics (frame lost); the ticks after it
	// are the ones that read the instance the panic unwound out of.
	frame := conn.Await("seen: ")
	assert.Contains(t, frame, "seen: 0", "a Tick handler read the posted value off the instance: %s", frame)
	frame = conn.Await("seen: ")
	assert.Contains(t, frame, "seen: 0", "a Tick handler read the posted value off the instance: %s", frame)
}

// --- A Set inside a Tick handler on a Bind()ed slot must reach the client.
//
// The display render re-applies lc.client over the instance, so the push has to
// drop the slot the server just wrote and emit its patch-signals frame — the
// same thing a live action does.

type tickSetsBoundSignal struct{ N via.Signal[int] }

func (p *tickSetsBoundSignal) OnInit(ctx *via.Ctx) error {
	ctx.Tick(5*time.Millisecond, func(*via.Ctx) { p.N.Set(p.N.Get() + 1) })
	return nil
}
func (p *tickSetsBoundSignal) View() h.H { return h.Div(h.Input(p.N.Bind()), p.N.Display()) }

func TestConnect_aTickHandlersSetReachesTheClient(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(tickSetsBoundSignal{}))
	conn := app.ConnectWith(`{"n":7}`)

	// The signal patch carries the server's value...
	assert.Contains(t, conn.Await(`"n":3`), `"n":3`)
	// ...and the display render no longer paints the client's 7 back over it.
	assert.Contains(t, conn.Await(">3<"), ">3<")
}

// --- F1: a live action's Set must survive its own panic into the NEXT push.
//
// liveRunAction used to reset unit.dirty to a fresh map before running the
// handler, unconditionally. A handler that Set a signal and then panicked (or
// whose reloadUnit errored) never reached a push, so its dirty entry sat on
// the instance unflushed — and the reset ahead of the NEXT action wiped it
// before that action's own push could ship it. flushDirty's clearDirty is the
// only thing meant to own clearing it, and only right after an actual flush.

type dirtyAfterPanic struct{ X via.Signal[int] }

func (p *dirtyAfterPanic) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Hour, func(*via.Ctx) {}) // liveness without a push of its own
	return nil
}
func (p *dirtyAfterPanic) SetAndBoom(ctx *via.Ctx) {
	p.X.Set(1)
	panic("via_test: action panic after Set")
}
func (p *dirtyAfterPanic) Noop(ctx *via.Ctx) {}

func (p *dirtyAfterPanic) View() h.H {
	return h.Div(
		h.Input(p.X.Bind()),
		h.Button(via.On("click", p.SetAndBoom)),
		h.Button(via.On("click", p.Noop)),
		p.X.Display(),
	)
}

func TestDispatchLive_aPanickingActionsSetSurvivesIntoTheNextPush(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(dirtyAfterPanic{}))
	conn := app.ConnectWith(`{"x":0}`)

	status, _ := app.Action(0).Over(conn).Fire()
	require.Equal(t, http.StatusInternalServerError, status, "the handler must panic")

	status, _ = app.Action(1).Over(conn).Fire()
	require.Equal(t, http.StatusNoContent, status)

	assert.Contains(t, conn.Await(`"x":1`), `"x":1`,
		"the panicking action's Set must still reach the client on the next push")
	frame := conn.Await(">1<")
	assert.Contains(t, frame, ">1<",
		"the display render must show the server's value, not paint the client's stale 0 back: %s", frame)
}

// --- F1 matrix: the shapes the root-level When above does not cover.
//
// The gate is the same one livePriv uses — a client-writable Signal whose only
// authority is OnInit — but the thing it gates moves: an Each row, a Child
// inside the branch, and that embeds one level deeper. The check funnels through
// u.actions[act] today, so one of these may look redundant; they are not, since
// the per-child intersection (pruneToAuthority) and the unit lookup are
// separate sites and a refactor splits them.

// gatedHits is shared by pointer so a child taken BY VALUE into a Child can
// still report, from the root's always-rendered markup, that it ran.
type gatedHits = atomic.Int64

type gatedEach struct {
	live    bool
	hits    *gatedHits
	IsAdmin via.Signal[bool]
}

// gateIsOpen reads the ONLY authority the gate has: this request. The path
// form exists because a stream connect is a POST to <mount>/_via/sse and
// carries no query, so the genuinely-privileged control below needs a mount.
func gateIsOpen(ctx *via.Ctx) bool {
	req := ctx.Request()
	return req.URL.Query().Get("admin") == "1" || strings.HasPrefix(req.URL.Path, "/admin")
}

func (g *gatedEach) OnInit(ctx *via.Ctx) error {
	g.IsAdmin.Set(gateIsOpen(ctx))
	if g.live {
		ctx.Tick(time.Hour, func(*via.Ctx) {})
	}
	return nil
}

func (g *gatedEach) Safe(ctx *via.Ctx)         {}
func (g *gatedEach) Nuke(ctx *via.Ctx, id int) { g.hits.Add(int64(id)) }
func (g *gatedEach) secret() []int {
	if g.IsAdmin.Get() {
		return []int{7}
	}
	return nil
}
func (g *gatedEach) row(id int) h.H {
	return h.Li(h.Button(via.OnArg("click", g.Nuke, id), h.Str("nuke")))
}

func (g *gatedEach) View() h.H {
	return h.Div(
		h.Input(g.IsAdmin.Bind()),
		h.Button(via.On("click", g.Safe), h.Str("safe")),
		h.Ul(via.Each(g.secret(), g.row)),
		h.P(h.Str("nuked: "+fmt.Sprint(g.hits.Load()))),
	)
}

type gatedChild struct{ hits *gatedHits }

func (c *gatedChild) Nuke(ctx *via.Ctx) { c.hits.Add(7) }
func (c *gatedChild) View() h.H         { return h.Div(h.Button(via.On("click", c.Nuke), h.Str("nuke"))) }

type gatedMid struct{ Leaf gatedChild }

func (m *gatedMid) View() h.H { return h.Div(h.Str("mid"), via.Child(m.Leaf)) }

// gatedChildPage puts the gated action inside a Child the root's When wraps.
// The Clock rides in its OWN When so the live and plain variants each keep a
// stable ordinal for the life of a connection — p.live is fixed by the field
// literal, which is the only kind of condition Child's TRAP allows.
type gatedChildPage struct {
	live    bool
	deep    bool
	hits    *gatedHits
	IsAdmin via.Signal[bool]
	Clock   livePrivClock
	Child   gatedChild
	Mid     gatedMid
}

func (p *gatedChildPage) OnInit(ctx *via.Ctx) error {
	p.IsAdmin.Set(gateIsOpen(ctx))
	return nil
}

func (p *gatedChildPage) Safe(ctx *via.Ctx) {}
func (p *gatedChildPage) clock() h.H        { return via.Child(p.Clock) }
func (p *gatedChildPage) gated() h.H {
	if p.deep {
		return via.Child(p.Mid)
	}
	return via.Child(p.Child)
}

func (p *gatedChildPage) View() h.H {
	return h.Div(
		h.Input(p.IsAdmin.Bind()),
		h.Button(via.On("click", p.Safe), h.Str("safe")),
		via.When(p.live, p.clock),
		via.When(p.IsAdmin.Get(), p.gated),
		h.P(h.Str("nuked: "+fmt.Sprint(p.hits.Load()))),
	)
}

// gatedShape is one cell's geometry: how to build the two apps, and where the
// gated action sits once the branch is open on each of them.
type gatedShape struct {
	name        string
	plain, live func(hits *gatedHits) http.Handler
	// mountLive re-registers the same live unit on a router, so the genuinely
	// privileged control can reach it at a path OnInit reads as admin. It is a
	// closure because Router.Mount is generic over the unit type.
	mountLive  func(r *via.Router, path string, hits *gatedHits)
	plainChild string
	liveChild  string
	gatedN     int
}

var gatedShapes = []gatedShape{{
	name:  "Each row",
	plain: func(hits *gatedHits) http.Handler { return via.Handler(gatedEach{hits: hits}) },
	live:  func(hits *gatedHits) http.Handler { return via.Handler(gatedEach{hits: hits, live: true}) },
	mountLive: func(r *via.Router, path string, hits *gatedHits) {
		r.Mount(path, gatedEach{hits: hits, live: true})
	},
	plainChild: "r", liveChild: "r", gatedN: 1,
}, {
	name: "Child inside a When",
	plain: func(hits *gatedHits) http.Handler {
		return via.Handler(gatedChildPage{hits: hits, Child: gatedChild{hits: hits}})
	},
	live: func(hits *gatedHits) http.Handler {
		return via.Handler(gatedChildPage{hits: hits, live: true, Child: gatedChild{hits: hits}})
	},
	mountLive: func(r *via.Router, path string, hits *gatedHits) {
		r.Mount(path, gatedChildPage{hits: hits, live: true, Child: gatedChild{hits: hits}})
	},
	plainChild: "0", liveChild: "1", gatedN: 0,
}, {
	name: "depth-2 child",
	plain: func(hits *gatedHits) http.Handler {
		return via.Handler(gatedChildPage{hits: hits, deep: true, Mid: gatedMid{Leaf: gatedChild{hits: hits}}})
	},
	live: func(hits *gatedHits) http.Handler {
		return via.Handler(gatedChildPage{hits: hits, deep: true, live: true, Mid: gatedMid{Leaf: gatedChild{hits: hits}}})
	},
	mountLive: func(r *via.Router, path string, hits *gatedHits) {
		r.Mount(path, gatedChildPage{hits: hits, deep: true, live: true, Mid: gatedMid{Leaf: gatedChild{hits: hits}}})
	},
	plainChild: "0-0", liveChild: "1-0", gatedN: 0,
}}

const forgedAdmin = `{"isAdmin":true}`

// assertGateHeld is the triple every cell asserts: the refusal is a 410, the
// handler did not run, and nothing the attacker posted survived into the
// server's own account of what happened.
func assertGateHeld(t *testing.T, hits *gatedHits, code int, body string) {
	t.Helper()
	assert.Equal(t, http.StatusGone, code, "a gated action must not be dispatchable: %s", body)
	assert.Zero(t, hits.Load(), "a gated handler ran")
	// The refusal has to be the authorization answer — this render binds no
	// such action, or no such child — and not a routing or parse miss, which
	// would pass this test while proving nothing about the gate.
	assert.True(t,
		strings.Contains(body, "does not bind it") ||
			strings.Contains(body, "no such action") ||
			strings.Contains(body, "no such child"),
		"the 410 must name the closed branch, not a routing miss: %s", body)
}

// The other half of every cell: with the branch genuinely open — the gate set
// by OnInit from the request, never by a posted signal — the SAME url must
// dispatch. A fix that 410s everything would satisfy the assertions above.
func TestDispatchLive_aGatedActionStillDispatchesWhenTheBranchIsGenuinelyOpen(t *testing.T) {
	t.Parallel()
	for _, shape := range gatedShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			hits := &gatedHits{}
			r := via.NewRouter()
			shape.mountLive(r, "/admin", hits)
			app := vt.Serve(t, r)

			_, page := app.Get("/admin")
			require.Contains(t, page, ">nuke<", "the privileged mount must bind the gated action")
			url := actionURL(t, page, shape.liveChild, shape.gatedN)
			conn := app.ConnectAt("/admin", "{}")

			code, body := app.Action(0).Over(conn).Raw(url).Fire()
			require.Equal(t, http.StatusNoContent, code, "the privileged dispatch was refused: %s", body)
			assert.NotZero(t, hits.Load(), "the gated handler must run for a genuinely privileged connection")
		})
	}
}

func TestDispatchPlain_postedSignalsCannotOpenAGatedActionInAnyShape(t *testing.T) {
	t.Parallel()
	for _, shape := range gatedShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			hits := &gatedHits{}
			app := vt.Serve(t, shape.plain(hits))

			_, privileged := app.Get("/?admin=1")
			url := actionURL(t, privileged, shape.plainChild, shape.gatedN)

			code, body := app.Action(0).Raw(url).Body(forgedAdmin).Fire()
			assertGateHeld(t, hits, code, body)
			assert.NotContains(t, body, ">nuke<", "the refused render must not ship the gated branch")

			_, after := app.Get("/")
			assert.Contains(t, after, "nuked: 0")
		})
	}
}

// gatedURL is the URL a privileged render ships for the gated action — the
// same id an unprivileged connection would have to call, since an action id is
// content-addressed on its handler. Scraped rather than pushed because a When
// that a posted signal opens around a Child never reaches the client frame at
// all (see TestDispatchLive_anChildOpenedByAPostedSignalIsNeverPushed), so the
// realistic attacker here is one replaying a URL he saw while privileged.
func gatedURL(t *testing.T, app *vt.App, child string, n int) string {
	t.Helper()
	_, privileged := app.Get("/?admin=1")
	require.Contains(t, privileged, ">nuke<", "the privileged render must bind the gated action")
	return actionURL(t, privileged, child, n)
}

func TestConnect_postedSignalsCannotOpenAGatedActionInAnyShape(t *testing.T) {
	t.Parallel()
	for _, shape := range gatedShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			hits := &gatedHits{}
			app := vt.Serve(t, shape.live(hits))
			url := gatedURL(t, app, shape.liveChild, shape.gatedN)
			conn := app.ConnectWith(forgedAdmin)

			// Connect frames no elements, so one ungated action with a CLEAN
			// body forces the display render the forged connect body rode in for.
			require.Equal(t, http.StatusNoContent, mustFire(t, app.Action(0).Over(conn)))

			code, body := app.Action(0).Over(conn).Raw(url).Fire()
			assertGateHeld(t, hits, code, body)

			assert.Equal(t, http.StatusNoContent, mustFire(t, app.Action(0).Over(conn)),
				"the stream goroutine must still be alive")
			assert.Zero(t, hits.Load(), "no gated handler may have run")
		})
	}
}

func TestDispatchLive_postedSignalsCannotOpenAGatedActionInAnyShape(t *testing.T) {
	t.Parallel()
	for _, shape := range gatedShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			hits := &gatedHits{}
			app := vt.Serve(t, shape.live(hits))
			url := gatedURL(t, app, shape.liveChild, shape.gatedN)
			conn := app.Connect()

			// A clean connect: the forgery rides on an action the client IS
			// allowed to call, which is the door a connect-only fix misses.
			require.Equal(t, http.StatusNoContent, mustFire(t, app.Action(0).Over(conn).Body(forgedAdmin)))

			code, body := app.Action(0).Over(conn).Raw(url).Body(forgedAdmin).Fire()
			assertGateHeld(t, hits, code, body)

			assert.Equal(t, http.StatusNoContent, mustFire(t, app.Action(0).Over(conn).Body(forgedAdmin)),
				"the stream goroutine must still be alive")
			assert.Zero(t, hits.Load(), "no gated handler may have run")
		})
	}
}

// --- F2, the other handler kind: Listen runs on the same live instance
// between pushes that Tick does (dispatch.go names both), so the restore that
// undoes a display render's hydration has to cover it identically. There is no
// plain cell to fill here by construction — ctx.Listen is valid only on a live
// unit — so the axis is root vs. child.

type listenReadsSignal struct {
	bus  *topic.Topic[string]
	Name via.Signal[string]
	seen string
}

func (p *listenReadsSignal) OnInit(ctx *via.Ctx) error {
	ctx.Listen(p.bus, func(_ *via.Ctx, v string) { p.seen = p.Name.Get() + "/" + v })
	return nil
}

func (p *listenReadsSignal) View() h.H {
	return h.Div(h.Input(p.Name.Bind()), p.Name.Display(), h.P(h.Str("seen: ["+p.seen+"]")))
}

type listenChildPage struct{ Child listenReadsSignal }

func (p *listenChildPage) View() h.H { return h.Div(h.Str("page"), via.Child(p.Child)) }

func TestConnect_aListenHandlerNeverSeesThePostedSignalValue(t *testing.T) {
	t.Parallel()
	shapes := []struct {
		name        string
		handler     func(bus *topic.Topic[string]) http.Handler
		connectBody string
	}{{
		name:        "root",
		handler:     func(bus *topic.Topic[string]) http.Handler { return via.Handler(listenReadsSignal{bus: bus}) },
		connectBody: `{"name":"ATTACKER"}`,
	}, {
		name: "child",
		handler: func(bus *topic.Topic[string]) http.Handler {
			return via.Handler(listenChildPage{Child: listenReadsSignal{bus: bus}})
		},
		connectBody: `{"child__name":"ATTACKER"}`,
	}}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			bus := topic.New[string]()
			app := vt.Serve(t, shape.handler(bus))
			conn := app.ConnectWith(shape.connectBody)

			require.Eventually(t, func() bool { return bus.NumSubs() == 1 }, time.Second, time.Millisecond,
				"the Listen must be subscribed before anything is published to it")

			// Connect frames no elements, so the FIRST publish is only there to
			// drive a push: that push's display render is what applies the
			// connect body to the live instance. The second is the one whose
			// handler reads the instance afterwards.
			bus.Publish("one")
			require.Contains(t, conn.Await(">ATTACKER<"), ">ATTACKER<",
				"the client must still SEE what it posted")

			bus.Publish("two")
			frame := conn.Await("/two]")
			assert.Contains(t, frame, "seen: [/two]", "the Listen handler read the client's value: %s", frame)
			assert.NotContains(t, frame, "ATTACKER/two")
		})
	}
}

// pinnedLive holds the connection's single goroutine hostage inside a Tick
// handler — the exact shape a blocking call in user code produces: keepalives
// stop too, so the connection is torn down by a proxy and the goroutine leaks
// for the life of the process.
type pinnedLive struct {
	n      via.State[int]
	block  chan struct{}
	pinned chan struct{} // buffered: signalled without blocking, on every beat
}

func (p *pinnedLive) OnInit(ctx *via.Ctx) error {
	ctx.Tick(5*time.Millisecond, p.hold)
	return nil
}

func (p *pinnedLive) hold(ctx *via.Ctx) {
	select {
	case p.pinned <- struct{}{}:
	default:
	}
	<-p.block
}

func (p *pinnedLive) Bump(ctx *via.Ctx) { p.n.Set(p.n.Get() + 1) }

func (p *pinnedLive) View() h.H {
	return h.Div(p.n.Display(), h.Button(via.On("click", p.Bump), h.Str("+")))
}

// A blocked Tick/Listen/action handler owns the connection's goroutine, so the
// dispatch never reaches it. Answering 410 "stream closed" blamed the client
// for a server-side hang and told the operator nothing — the tab and its stream
// are both fine. It must be a 503 (the condition is transient and server-side)
// and it must say so in the log, once, with the tab id and the unit type.
func TestDispatch_pinnedStreamGoroutineAnswers503AndLogsOnce(t *testing.T) {
	restore := via.SetPinnedDeadlineForTest(150 * time.Millisecond)
	defer restore()

	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prev)

	p := pinnedLive{block: make(chan struct{}), pinned: make(chan struct{}, 1)}
	defer close(p.block)
	app := vt.Serve(t, via.Handler(p))
	conn := app.Connect()
	defer conn.Close()
	select {
	case <-p.pinned:
	case <-time.After(3 * time.Second):
		t.Fatal("precondition: the tick handler never took the connection goroutine")
	}

	for range 3 {
		status, body := app.Action(0).Over(conn).Fire()
		assert.Equal(t, http.StatusServiceUnavailable, status,
			"a dispatch onto a pinned goroutine is a server-side stall, not a closed stream")
		assert.Contains(t, body, "stream busy")
	}

	out := logs.String()
	assert.Contains(t, out, "queue not drained",
		"a pinned connection must be named in the log — nothing else reports it")
	assert.Contains(t, out, "tab="+conn.TabID())
	assert.Contains(t, out, "via_test.pinnedLive", "the log must name the unit type to go read")
	assert.Equal(t, 1, strings.Count(out, "queue not drained"),
		"a pinned goroutine stays pinned; one line per connection, not one per click")
}

// A dispatch on a session-bound connection compares the request's session with
// the connection's. When the STORE cannot answer, the resolve yields no session
// — which used to read as "wrong session" and answer 403, reporting a backend
// blip as a security rejection while the only log line was sess.go's "Load
// failed" with nothing tying it to the refusal. A store outage is a 503, the
// same answer the connect already gives.
func TestDispatch_sessionStoreOutageAnswers503NotForbidden(t *testing.T) {
	t.Parallel()
	store := &outageStore{SessionStore: via.NewMemorySessionStore()}
	app := vt.Serve(t, via.Handler(sessBoundLive{},
		via.WithSessionStore(store),
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	app.Client().Jar = jar

	conn := app.Connect()
	defer conn.Close()

	status, _ := app.Action(0).Over(conn).Fire()
	require.Less(t, status, 300, "precondition: the bound connection dispatches while the store is up")

	store.mu.Lock()
	store.down = true
	store.mu.Unlock()

	status, body := app.Action(0).Over(conn).Fire()
	assert.Equal(t, http.StatusServiceUnavailable, status,
		"a store that cannot answer is an outage, not a session mismatch")
	assert.Contains(t, body, "session store unavailable")
	assert.NotContains(t, body, "session mismatch")
}

// sessBoundLive establishes a session in OnInit so its stream is bound to one,
// which is what puts a dispatch on the session-compare path.
type sessBoundLive struct{ n via.State[int] }

func (s *sessBoundLive) OnInit(ctx *via.Ctx) error {
	ctx.Session().Put(member{Name: "bob"})
	return nil
}
func (s *sessBoundLive) Bump(ctx *via.Ctx) { s.n.Set(s.n.Get() + 1) }
func (s *sessBoundLive) View() h.H {
	return h.Div(s.n.Display(), h.Button(via.On("click", s.Bump), h.Str("+")))
}

// The unknown-action log prints the render's whole action table — every bound
// id and the Go method name behind it. Unthrottled, a client posting garbage
// ids dumps that table at line rate: a log-flood amplifier and a disclosure
// channel at once. One line per distinct unknown id, like warnNoChange.
func TestDispatch_unknownActionLogsTheActionTableOncePerID(t *testing.T) {
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prev)

	app := vt.Serve(t, via.Handler(bumpLive{}))
	conn := app.Connect()
	defer conn.Close()

	for range 5 {
		status, _ := app.Action(0).Over(conn).Raw("/_via/a/r/deadbeef").Fire()
		require.Equal(t, http.StatusGone, status)
	}
	for range 5 {
		status, _ := app.Action(0).Over(conn).Raw("/_via/a/r/cafebabe").Fire()
		require.Equal(t, http.StatusGone, status)
	}

	out := logs.String()
	assert.Equal(t, 1, strings.Count(out, "no such action deadbeef"),
		"a repeated garbage id must not re-dump the action table")
	assert.Equal(t, 1, strings.Count(out, "no such action cafebabe"),
		"the dedupe is per id, so a genuinely new mistake is still reported")
}

// bumpLive is the minimal live, session-free unit with one action.
type bumpLive struct{ n via.State[int] }

func (b *bumpLive) Bump(ctx *via.Ctx) { b.n.Set(b.n.Get() + 1) }
func (b *bumpLive) View() h.H {
	return h.Div(b.n.Display(), h.Button(via.On("click", b.Bump), h.Str("+")))
}

// A 503 at the connection cap told nobody anything: the operator saw tabs fail
// to go live with no line in the log naming the wall they hit. Rate-limited
// rather than deduped forever — being at capacity comes and goes, unlike a
// wiring mistake.
func TestDispatch_atCapacityLogsWhichWallWasHit(t *testing.T) {
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prev)

	restore := via.SetMaxSSEConnForTest(1)
	app := vt.Serve(t, via.Handler(bumpLive{}))
	restore()

	conn := app.Connect()
	defer conn.Close()

	for range 3 {
		status, body := app.Action(0).Over(conn).Raw("/_via/sse").Fire()
		require.Equal(t, http.StatusServiceUnavailable, status)
		require.Contains(t, body, "stream capacity reached")
	}

	out := logs.String()
	assert.Contains(t, out, "1 of 1 live streams", "the log must name the current count and the cap")
	assert.Contains(t, out, "maxSSEConn", "the log must name the knob that moves the wall")
	assert.Equal(t, 1, strings.Count(out, "refusing an SSE connect"),
		"a refusal storm must not become the loudest thing in the log")
}

// A panic's stack says where, never which tab or which unit. On a busy deploy
// that is the difference between grouping the failures and reading them one by
// one.
func TestDispatch_liveActionPanicLogNamesTheTabAndUnit(t *testing.T) {
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prev)

	app := vt.Serve(t, via.Handler(panicLive{}))
	conn := app.Connect()
	defer conn.Close()

	status, _ := app.Action(0).Over(conn).Fire()
	require.Equal(t, http.StatusInternalServerError, status)

	out := logs.String()
	assert.Contains(t, out, "tab="+conn.TabID(), "a panic log must carry the tab it came from")
	assert.Contains(t, out, "unit=*via_test.panicLive", "a panic log must name the unit type")
	assert.Contains(t, out, "panicLive).Boom", "a panic log must name the action that blew up")
}
