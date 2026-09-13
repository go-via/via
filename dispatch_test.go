package via_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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

func TestDispatch_redirectFromLiveActionDoesNotShipAScript(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(liveRedirector{}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")

		req, err := http.NewRequest(http.MethodPost, app.URL()+actionURL(t, page, "r", 0), strings.NewReader(withTab(conn.TabID(), "{}")))
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Datastar-Request", "true")
		resp, err := app.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode,
			"a live action's Redirect can no longer navigate; it answers its normal 204")
		assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript")
		assert.Empty(t, resp.Header.Get("datastar-script-attributes"))
	})
}

// embedRedirector is a PLAIN embed (dispatchPlain, not
// liveRunAction) whose action queues a Redirect.
type embedRedirector struct{}

func (r *embedRedirector) Go(ctx *via.Ctx) { ctx.Redirect("/dest") }
func (r *embedRedirector) View() h.H       { return h.Div(h.Button(via.On("click", r.Go))) }

type embedRedirectorParent struct{ I embedRedirector }

func (p *embedRedirectorParent) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_redirectFromPlainEmbedActionDoesNotShipAScript(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(embedRedirectorParent{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	req, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, "0", 0), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript",
		"a plain embed's Redirect can no longer navigate; no script ships")
	assert.Empty(t, resp.Header.Get("datastar-script-attributes"))
}

// sigEmbed's signals are only Bound (never Displayed), so Setting one never
// changes the rendered HTML — the only way a client sees the new value is the
// container's data-signals attribute. Other exists solely so Reset's Set of
// Name has a sibling to leave alone: the response must declare Name and NOT
// Other, proving the patch is restricted to what the action actually wrote
// rather than the whole slot table.
type sigEmbed struct{ Name, Other via.Signal[string] }

func (s *sigEmbed) Reset(ctx *via.Ctx) { s.Name.Set("resetted") }
func (s *sigEmbed) View() h.H {
	return h.Div(s.Name.Bind(), s.Other.Bind(), h.Button(via.On("click", s.Reset)))
}

type sigPage struct{ I sigEmbed }

func (p *sigPage) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_signalSetInEmbedActionReachesClient(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(sigPage{}))

	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "0", 0), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"a Signal.Set with no visible HTML change must still ship a patch, not 204")
	assert.Contains(t, body, `"i__name":"resetted"`, "the Set signal must reach the client")
	assert.NotContains(t, body, `"i__other":`,
		"an untouched sibling signal must not be declared — Set restricts the patch, it doesn't broadcast the whole table")
}

// guardedEmbed is embedded under a parent whose own OnInit session-gates
// the whole mount, replacing the removed guard mechanism.
type guardedEmbed struct{}

func (g *guardedEmbed) Ping(ctx *via.Ctx) {}
func (g *guardedEmbed) View() h.H         { return h.Div(h.Button(via.On("click", g.Ping))) }

type guardedParent struct{ I guardedEmbed }

func (p *guardedParent) OnInit(ctx *via.Ctx) error {
	if _, ok := ctx.Session().Get[acct](); !ok {
		ctx.Redirect("/login")
	}
	return nil
}
func (p *guardedParent) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_embedActionRunsOnInitRedirect(t *testing.T) {
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

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "the parent's OnInit Redirect must gate the embed action route")
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
	srv := serve(t, via.Register(mixedPage{}))

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
		app := vt.Serve(t, via.Register(panicLive{}))
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
func (b *branchy) Extra(ctx *via.Ctx)  {}
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
		app := vt.Serve(t, via.Register(branchy{}))
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

// paramEmbed is embedded under a parametrised mount; Bump changes its
// visible count so the action's response is a real patch, not a 204.
type paramEmbed struct{ n int }

func (k *paramEmbed) Bump(ctx *via.Ctx) { k.n++ }
func (k *paramEmbed) View() h.H         { return h.Div(h.Str(k.n), h.Button(via.On("click", k.Bump))) }

type paramParent struct{ I paramEmbed }

func (p *paramParent) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_pushUnderParamMountRendersConcreteBase(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/thread/{id}", paramParent{})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/thread/7", "")
	assert.Regexp(t, `@post\('/thread/7/_via/a/0/[A-Za-z0-9_-]+'`, page)

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "0", 0), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, body, "{id}", "the re-rendered action URL must not carry the pattern wildcard")
	assert.Contains(t, body, `/thread/7/_via/a/0/`, "the re-rendered action URL must carry the concrete segment")
}

// unsafeRoot and unsafeEmbed both bump a visible counter alongside an
// unsafe Redirect, so a rejected redirect's fallback response is provably a
// normal patch (the counter's new value), not a crash or a hang.
type unsafeRoot struct{ n int }

func (u *unsafeRoot) Go(ctx *via.Ctx) { u.n++; ctx.Redirect("javascript:alert(1)") }
func (u *unsafeRoot) View() h.H       { return h.Div(h.Str(u.n), h.Button(via.On("click", u.Go))) }

type unsafeEmbed struct{ n int }

func (u *unsafeEmbed) Go(ctx *via.Ctx) { u.n++; ctx.Redirect("javascript:alert(1)") }
func (u *unsafeEmbed) View() h.H       { return h.Div(h.Str(u.n), h.Button(via.On("click", u.Go))) }

type unsafeParent struct{ I unsafeEmbed }

func (p *unsafeParent) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_unsafeRedirectFallsBackEverywhere(t *testing.T) {
	t.Parallel()

	t.Run("plain root", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(unsafeRoot{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), "{}")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript")
		assert.Contains(t, body, ">1<", "the mutation must still land in the fallback patch")
	})

	t.Run("plain embed", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(unsafeParent{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "0", 0), "{}")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript")
		assert.Contains(t, body, ">1<", "the mutation must still land in the fallback patch")
	})

	t.Run("native form", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(loginForm{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, "r", 0), "name", "evil")
		assert.NotEqual(t, http.StatusSeeOther, resp.StatusCode, "an unsafe redirect must not 303")
		assert.Empty(t, resp.Header.Get("Location"))
	})
}

// splitActionURL cuts url into its /_via/a/ prefix, {embed}, {act} and any
// query, so a test can forge one segment and keep the rest genuine.
func splitActionURL(t *testing.T, url string) (prefix, embed, act, query string) {
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
// bind, keeping embed, mount prefix and ?a= genuine.
func swapActionID(t *testing.T, url, act string) string {
	t.Helper()
	prefix, embed, _, q := splitActionURL(t, url)
	return prefix + embed + "/" + act + q
}

// swapEmbedIndex rewrites url's {embed} segment, keeping a genuine action id
// on it — an id that resolves on ITS embed must not resolve on another.
func swapEmbedIndex(t *testing.T, url, embed string) string {
	t.Helper()
	prefix, _, act, q := splitActionURL(t, url)
	return prefix + embed + "/" + act + q
}

func TestDispatch_forgedActionIDIsGone(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(counter{count: &store{}}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		url := actionURL(t, page, "r", 0)
		for _, n := range []string{"99", "-1", "________"} {
			resp, _ := do(t, srv, http.MethodPost, swapActionID(t, url, n), "{}")
			assert.Equal(t, http.StatusGone, resp.StatusCode, "forged id=%s must 410, not panic/misroute", n)
		}
	})

	t.Run("live", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			app := vt.Serve(t, via.Register(liveClicker{}))
			conn := app.Connect()
			page := fetchPage(t, app, "/")
			url := actionURL(t, page, "r", 0)
			for _, n := range []string{"99", "-1", "________"} {
				status, _ := app.Action(0).Raw(swapActionID(t, url, n)).Tab(conn.TabID()).Fire()
				assert.Equal(t, http.StatusGone, status, "forged id=%s must 410, not panic/misroute", n)
			}
			// The connection must still be usable — the recovered panic path
			// this replaces must not be the only thing standing between a
			// forged n and a crashed stream.
			status, _ := app.Action(0).Raw(url).Tab(conn.TabID()).Fire()
			assert.Equal(t, http.StatusNoContent, status)
		})
	})
}

func TestDispatch_unknownActionAnswers410OnEveryPath(t *testing.T) {
	t.Parallel()

	t.Run("plain root", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(counter{count: &store{}}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, _ := do(t, srv, http.MethodPost, swapActionID(t, actionURL(t, page, "r", 1), "zzzzzzzz"), "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode)
	})

	t.Run("plain embed", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(sigPage{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, _ := do(t, srv, http.MethodPost, swapActionID(t, actionURL(t, page, "0", 0), "zzzzzzzz"), "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode)
	})

	// The entire body is the synctest bubble, so no t.Parallel() (its *testing.T
	// can't call it — see CONVENTIONS.md's synctest carve-out).
	t.Run("live unit", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			app := vt.Serve(t, via.Register(liveRedirector{}))
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

// toggleState is branchedView's shared, per-connection-independent memory: a
// bool that flips which actions View renders, plus which handler last ran, so
// a test can observe whether a click actually reached a handler.
type toggleState struct {
	mu     sync.Mutex
	locked bool
	ran    string
}

func (s *toggleState) flip()            { s.mu.Lock(); s.locked = !s.locked; s.mu.Unlock() }
func (s *toggleState) mark(name string) { s.mu.Lock(); s.ran = name; s.mu.Unlock() }
func (s *toggleState) snapshot() (locked bool, ran string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locked, s.ran
}

// branchedView's action SET depends on server state: unlocked renders
// Save=0, Delete=1, Flip=2; locked removes Save, so every later index shifts
// down — Delete=0, Flip=1 — the render shape the audit flagged as a live
// misroute hazard (a branched View shifting every following index),
// reproduced here on the plain path.
type branchedView struct{ st *toggleState }

func (b *branchedView) Save(ctx *via.Ctx)   { b.st.mark("save") }
func (b *branchedView) Delete(ctx *via.Ctx) { b.st.mark("delete") }
func (b *branchedView) Flip(ctx *via.Ctx)   { b.st.flip() }
func (b *branchedView) View() h.H {
	locked, ran := b.st.snapshot()
	if locked {
		return h.Div(
			h.Button(via.On("click", b.Delete)), // locked: Delete=0
			h.Button(via.On("click", b.Flip)),   // locked: Flip=1 (Save is gone)
			h.P(h.Str("locked:"), h.Str(ran)),
		)
	}
	return h.Div(
		h.Button(via.On("click", b.Save)),   // unlocked: Save=0
		h.Button(via.On("click", b.Delete)), // unlocked: Delete=1
		h.Button(via.On("click", b.Flip)),   // unlocked: Flip=2
		h.P(h.Str("unlocked:"), h.Str(ran)),
	)
}

// xmEmbed is a live, dep-free embed mountable at any path — the vehicle for
// proving a live tab from one mount can't drive another mount's action table.
type xmEmbed struct {
	fired *int
	n     via.State[int]
}

func (x *xmEmbed) Fire(*via.Ctx) { *x.fired++ }
func (x *xmEmbed) View() h.H {
	return h.Div(x.n.Display(), h.Button(via.On("click", x.Fire)))
}

func TestDispatch_liveActionCannotCrossMounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var aFired, cFired int
		r := via.NewRouter()
		r.Mount("/a", xmEmbed{fired: &aFired})
		r.Mount("/c", xmEmbed{fired: &cFired})
		srv := liveServer(t, r)

		_, aPage := do(t, srv, http.MethodGet, "/a", "")
		aURL := actionURL(t, aPage, "r", 0)

		cLines, cancel := openStreamAt(t, srv, "/c/_via/sse")
		defer cancel()
		cTab := awaitTabID(t, cLines)
		synctest.Wait()

		resp, _ := post(t, srv, aURL, withTab(cTab, "{}"), map[string]string{
			"Sec-Fetch-Site": "same-origin",
		})
		assert.Equal(t, http.StatusGone, resp.StatusCode, "a /c tab must not drive /a's action table")
		assert.Zero(t, aFired, "the /a action must not have run")
	})
}

func TestDispatch_branchedViewCannotMisroute(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(branchedView{st: &toggleState{}}))

	_, unlocked := do(t, srv, http.MethodGet, "/", "")
	staleSave := actionURL(t, unlocked, "r", 0)   // Save, only bound while unlocked
	staleDelete := actionURL(t, unlocked, "r", 1) // Delete, bound in both branches
	flip := actionURL(t, unlocked, "r", 2)

	// Flip to locked: Save leaves the table and every later index shifts down
	// by one — under positional routing the pre-flip Delete URL (index 1)
	// would now land on Flip, silently toggling back to unlocked.
	resp, lockedBody := do(t, srv, http.MethodPost, flip, "{}")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, lockedBody, "locked:", "Flip must have taken effect")

	// The id addresses the handler, so the pre-flip Delete URL still means
	// Delete — the click does what the button it came from said it does.
	resp2, after := do(t, srv, http.MethodPost, staleDelete, "{}")
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	assert.Contains(t, after, "locked:", "the stale click must not have misrouted into Flip (back to unlocked)")
	assert.Contains(t, after, "delete", "it must have run Delete, the handler it named")

	// Save, on the other hand, is not bound by the locked branch at all: 410,
	// never a misroute into whatever now sits at its old index.
	resp3, _ := do(t, srv, http.MethodPost, staleSave, "{}")
	assert.Equal(t, http.StatusGone, resp3.StatusCode,
		"an action the current render does not bind must 410")
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
		app := vt.Serve(t, via.Register(liveForm{calls: &calls}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")
		assert.Contains(t, page, `data-attr:value="$viatab"`,
			"a live unit's PostForm must reactively fill the fallback field from the tab signal")
		formURL := actionURL(t, page, "r", 0)

		status, _ := nativeFormPost(t, app, formURL, map[string]string{
			"name":    "zed",
			"_viatab": conn.TabID(),
		})

		// The returned page is a fresh connection's page (see Embed's godoc on
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
		app := vt.Serve(t, via.Register(nativeFormPanic{boom: boom}))
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

// openStreamWithClient is openStreamAt against a caller-supplied client, so a
// test can open the SSE stream carrying a cookie already sitting in the
// client's jar (a session established by an earlier plain action).
func openStreamWithClient(t *testing.T, srv *httptest.Server, c *http.Client, path string) (<-chan string, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return lines, cancel
}

// sessionLive is a live root — a Live-implementing root's own actions only
// ever route through the tab handshake (dispatchPlain refuses them, see
// dispatch.go), so establishing the session ahead of connecting needs a
// separate, plain mount (loginComp, from sess_test.go) sharing the same
// router-wide session manager. Bump is the live action a stolen tab id would
// try to drive.
type sessionLive struct{ n via.State[int] }

func (s *sessionLive) Bump(ctx *via.Ctx) { s.n.Set(s.n.Get() + 1) }
func (s *sessionLive) Peek(ctx *via.Ctx) { ctx.Session().Get[member]() } // read-only, never mints
func (s *sessionLive) View() h.H {
	return h.Div(s.n.Display(),
		h.Button(via.On("click", s.Bump)), // action 0
		h.Button(via.On("click", s.Peek))) // action 1
}

// liveActionRequest builds a raw dispatch POST against embed/n using the
// page's currently-rendered action URL, with tab as its viatab signal —
// bypassing any cookie jar, so the caller controls exactly what (if any)
// session cookie rides along.
func liveActionRequest(t *testing.T, srv *httptest.Server, page, tab, embed string, n int) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, embed, n), strings.NewReader(withTab(tab, "{}")))
	require.NoError(t, err)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return req
}

// A stream opened under a real session must reject a dispatch that
// doesn't carry that same session — the tab id alone (a leaked/stolen one,
// with no cookie at all, exactly as a cross-origin request would arrive with
// the origin floor open) is no longer a sufficient credential.
func TestDispatch_liveActionUnderASessionRejectsAMismatchedSession(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/login", loginComp{})  // plain — establishes the session cookie
	r.Mount("/live", sessionLive{}) // Live root, shares the router-wide session manager
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	owner := jarClient(t)

	loginResp, err := owner.Get(srv.URL + "/login")
	require.NoError(t, err)
	loginPage, err := io.ReadAll(loginResp.Body)
	require.NoError(t, err)
	loginResp.Body.Close()

	signInReq, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, string(loginPage), "r", 0), strings.NewReader("{}"))
	require.NoError(t, err)
	signInReq.Header.Set("Sec-Fetch-Site", "same-origin")
	signInReq.Header.Set("Datastar-Request", "true")
	signInResp, err := owner.Do(signInReq)
	require.NoError(t, err)
	signInResp.Body.Close()
	require.NotEmpty(t, cookieValue(t, owner, srv.URL, "via_session"))

	lines, cancel := openStreamWithClient(t, srv, owner, "/live/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)

	getResp, err := owner.Get(srv.URL + "/live")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()

	req := liveActionRequest(t, srv, string(page), tab, "r", 0)
	// No cookie at all on this request — the stolen-tab-id, no-session attack.
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a dispatch against a session-bound connection with no session must be rejected")

	// The rightful owner, same tab, same session cookie, must still work —
	// the check rejects a MISMATCH, not the connection itself.
	ownReq := liveActionRequest(t, srv, string(page), tab, "r", 0)
	ownResp, err := owner.Do(ownReq)
	require.NoError(t, err)
	defer ownResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, ownResp.StatusCode,
		"the connecting session's own dispatch must still succeed")
}

// Neighbour of the mismatch rejection: an ANONYMOUS stream (no
// session at any point) must keep dispatching exactly as before — the check
// only applies once a connection is actually bound to a session, so an app
// that never touches Session() sees no behavior change.
func TestDispatch_liveActionOnAnAnonymousConnectionIsUnaffected(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(sessionLive{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	lines, cancel := openStreamAt(t, srv, "/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)

	getResp, err := http.DefaultClient.Get(srv.URL + "/")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()

	req := liveActionRequest(t, srv, string(page), tab, "r", 0) // Bump, no cookie anywhere
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode,
		"an anonymous connection must not be blocked — there is no session to mismatch")
}

// I1: an attacker holding a victim's leaked tab id, but carrying their OWN
// valid session cookie, must not capture the connection by merely running a
// read-only action against it — Ctx.Session() resolves the REQUEST's cookie
// even for a read, so binding on "Session() was touched" (rather than "the
// action minted a session that wasn't there before") would let the attacker's
// pre-existing cookie look identical, after the fact, to a session the
// action just created.
func TestDispatch_liveReadOnlySessionTouchByAForeignCookieDoesNotCaptureTheConnection(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/login", loginComp{})
	r.Mount("/live", sessionLive{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	attacker := jarClient(t)
	loginResp, err := attacker.Get(srv.URL + "/login")
	require.NoError(t, err)
	loginPage, err := io.ReadAll(loginResp.Body)
	require.NoError(t, err)
	loginResp.Body.Close()
	signInReq, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, string(loginPage), "r", 0), strings.NewReader("{}"))
	require.NoError(t, err)
	signInReq.Header.Set("Sec-Fetch-Site", "same-origin")
	signInReq.Header.Set("Datastar-Request", "true")
	signInResp, err := attacker.Do(signInReq)
	require.NoError(t, err)
	signInResp.Body.Close()
	require.NotEmpty(t, cookieValue(t, attacker, srv.URL, "via_session"), "attacker must hold a real session of their own")

	// The victim's tab connects anonymously — nothing about it identifies the
	// attacker; only its id, echoed on the wire, is assumed leaked.
	lines, cancel := openStreamAt(t, srv, "/live/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)

	getResp, err := http.DefaultClient.Get(srv.URL + "/live")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()

	// The attacker dispatches Peek — read-only — against the victim's tab,
	// carrying their own cookie via a plain http.Request (not the jar client,
	// so we control exactly which cookie rides along).
	peekReq := liveActionRequest(t, srv, string(page), tab, "r", 1) // Peek
	peekReq.AddCookie(&http.Cookie{Name: "via_session", Value: cookieValue(t, attacker, srv.URL, "via_session")})
	peekResp, err := http.DefaultClient.Do(peekReq)
	require.NoError(t, err)
	peekResp.Body.Close()

	// The victim's own later cookieless dispatch must still succeed — the
	// connection must NOT have been captured by the attacker's cookie.
	bumpReq := liveActionRequest(t, srv, string(page), tab, "r", 0) // Bump, no cookie
	bumpResp, err := http.DefaultClient.Do(bumpReq)
	require.NoError(t, err)
	defer bumpResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, bumpResp.StatusCode,
		"a read-only action carrying a foreign cookie must not bind the connection to it")
}

// Neighbour of the capture probe: the connection's own (anonymous) owner
// running that same read-only action must not bind it either — the fix must
// distinguish "an action minted a session" from "Session() was merely
// touched", not just "no foreign cookie was involved".
func TestDispatch_liveReadOnlySessionTouchByTheOwnerDoesNotBind(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(sessionLive{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	lines, cancel := openStreamAt(t, srv, "/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)

	getResp, err := http.DefaultClient.Get(srv.URL + "/")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()

	peekReq := liveActionRequest(t, srv, string(page), tab, "r", 1) // Peek, no cookie
	peekResp, err := http.DefaultClient.Do(peekReq)
	require.NoError(t, err)
	peekResp.Body.Close()

	bumpReq := liveActionRequest(t, srv, string(page), tab, "r", 0) // Bump, still no cookie
	bumpResp, err := http.DefaultClient.Do(bumpReq)
	require.NoError(t, err)
	defer bumpResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, bumpResp.StatusCode,
		"a read-only Session().Get by the connection's own owner must not bind it")
}

// liveLoginer is a live root that starts every connection anonymous — Login
// and Rotate are both live actions, so the only way its session gets
// established is on the connection's own goroutine, after connect (see H1).
type liveLoginer struct {
	n via.State[int]
}

func (p *liveLoginer) Bump(ctx *via.Ctx)   { p.n.Set(p.n.Get() + 1) }                 // action 0
func (p *liveLoginer) Login(ctx *via.Ctx)  { ctx.Session().Put(member{Name: "bob"}) } // action 1
func (p *liveLoginer) Rotate(ctx *via.Ctx) { ctx.Session().Rotate() }                 // action 2
func (p *liveLoginer) View() h.H {
	return h.Div(p.n.Display(),
		h.Button(via.On("click", p.Bump)),
		h.Button(via.On("click", p.Login)),
		h.Button(via.On("click", p.Rotate)))
}

// A tab connects anonymously, then a live action logs it in (Session().Put)
// — the connection must stop accepting a cookieless dispatch from that point
// on, closing the gap H1 was filed for.
func TestDispatch_liveActionLoginBindsTheConnectionAgainstALaterCookielessDispatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(liveLoginer{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	lines, cancel := openStreamAt(t, srv, "/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)

	getResp, err := http.DefaultClient.Get(srv.URL + "/")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()

	loginReq := liveActionRequest(t, srv, string(page), tab, "r", 1) // Login
	loginResp, err := http.DefaultClient.Do(loginReq)
	require.NoError(t, err)
	loginResp.Body.Close()
	require.NotEmpty(t, loginResp.Header.Get("Set-Cookie"), "a live Login must still mint the session cookie")

	bumpReq := liveActionRequest(t, srv, string(page), tab, "r", 0) // Bump, no cookie
	bumpResp, err := http.DefaultClient.Do(bumpReq)
	require.NoError(t, err)
	defer bumpResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, bumpResp.StatusCode,
		"the tab id must stop being a bearer credential the instant it logs in")
}

// onConnectLoginer establishes its session in OnInit — the pattern the
// README recommends — rather than through a later action.
type onConnectLoginer struct{ n via.State[int] }

func (o *onConnectLoginer) OnInit(ctx *via.Ctx) error {
	ctx.Session().Put(member{Name: "carol"})
	return nil
}
func (o *onConnectLoginer) Bump(ctx *via.Ctx) { o.n.Set(o.n.Get() + 1) } // action 0
func (o *onConnectLoginer) View() h.H {
	return h.Div(o.n.Display(), h.Button(via.On("click", o.Bump)))
}

// The README-recommended "establish the session in OnInit" pattern must
// bind the connection too — not just a session that already existed at
// connect time.
func TestDispatch_onConnectMintedSessionBindsTheConnection(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(onConnectLoginer{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	lines, cancel := openStreamAt(t, srv, "/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)

	getResp, err := http.DefaultClient.Get(srv.URL + "/")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()

	req := liveActionRequest(t, srv, string(page), tab, "r", 0) // Bump, no cookie
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"an OnInit-minted session must bind the connection exactly like a live-action login")
}

// Neighbour: two tabs open anonymously against the same app — logging one of
// them in through a live action must bind ONLY that connection. A sibling
// tab that never logs in keeps dispatching cookielessly, exactly as before.
func TestDispatch_liveLoginOnOneTabDoesNotBindASiblingTab(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(liveLoginer{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	linesA, cancelA := openStreamAt(t, srv, "/_via/sse")
	defer cancelA()
	tabA := awaitTabID(t, linesA)
	linesB, cancelB := openStreamAt(t, srv, "/_via/sse")
	defer cancelB()
	tabB := awaitTabID(t, linesB)

	getResp, err := http.DefaultClient.Get(srv.URL + "/")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()

	loginReq := liveActionRequest(t, srv, string(page), tabA, "r", 1) // Login on A
	loginResp, err := http.DefaultClient.Do(loginReq)
	require.NoError(t, err)
	loginResp.Body.Close()

	bumpA := liveActionRequest(t, srv, string(page), tabA, "r", 0)
	bumpAResp, err := http.DefaultClient.Do(bumpA)
	require.NoError(t, err)
	bumpAResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, bumpAResp.StatusCode, "the logged-in tab rejects a cookieless dispatch")

	bumpB := liveActionRequest(t, srv, string(page), tabB, "r", 0)
	bumpBResp, err := http.DefaultClient.Do(bumpB)
	require.NoError(t, err)
	defer bumpBResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, bumpBResp.StatusCode,
		"the sibling tab, never logged in, dispatches exactly as before")
}

// Neighbour: a session that rotates AFTER a live login bound the connection
// must keep the binding across the new id — Rotate moves the same
// *sessionData pointer (see sessionStore.reID), which is what dispatch
// compares against.
func TestDispatch_rotateAfterALiveLoginKeepsTheBindingOnTheNewID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(liveLoginer{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)
	owner := jarClient(t)

	lines, cancel := openStreamWithClient(t, srv, owner, "/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)

	getResp, err := owner.Get(srv.URL + "/")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()

	loginReq := liveActionRequest(t, srv, string(page), tab, "r", 1) // Login
	loginResp, err := owner.Do(loginReq)
	require.NoError(t, err)
	loginResp.Body.Close()
	before := cookieValue(t, owner, srv.URL, "via_session")
	require.NotEmpty(t, before)

	rotReq := liveActionRequest(t, srv, string(page), tab, "r", 2) // Rotate
	rotResp, err := owner.Do(rotReq)
	require.NoError(t, err)
	rotResp.Body.Close()
	after := cookieValue(t, owner, srv.URL, "via_session")
	require.NotEqual(t, before, after, "Rotate must mint a fresh id")

	okReq := liveActionRequest(t, srv, string(page), tab, "r", 0) // Bump, new cookie
	okResp, err := owner.Do(okReq)
	require.NoError(t, err)
	okResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, okResp.StatusCode,
		"the post-rotate dispatch with the new cookie must still be bound")

	staleReq := liveActionRequest(t, srv, string(page), tab, "r", 0)
	staleReq.AddCookie(&http.Cookie{Name: "via_session", Value: before})
	staleResp, err := http.DefaultClient.Do(staleReq)
	require.NoError(t, err)
	defer staleResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, staleResp.StatusCode,
		"the pre-rotate id must not drive the connection anymore")
}

// raceLoginer is liveLoginer's Login, but pausable: it blocks on the embed
// goroutine until proceed is signaled, closing started the instant it takes
// hold of that goroutine — the two channels let a test park a concurrent
// cookieless dispatch's own goroutine right at the moment the connection is
// still unbound, then release the login and observe which check ran first.
type raceLoginer struct {
	n       via.State[int]
	started chan struct{}
	proceed chan struct{}
}

func (p *raceLoginer) Bump(ctx *via.Ctx) { p.n.Set(p.n.Get() + 1) } // action 0
func (p *raceLoginer) Login(ctx *via.Ctx) {
	close(p.started)
	<-p.proceed
	ctx.Session().Put(member{Name: "bob"})
} // action 1
func (p *raceLoginer) View() h.H {
	return h.Div(p.n.Display(),
		h.Button(via.On("click", p.Bump)),
		h.Button(via.On("click", p.Login)))
}

// I3: the session-bound check must run on the same serialized goroutine as
// the action it guards, not on the dispatching request's own goroutine
// before the closure is even queued — otherwise a cookieless dispatch that
// passes the check while the connection is still unbound, then actually
// runs after a concurrent login has bound it, is applied anyway.
func TestDispatch_cookielessDispatchRacingAConcurrentLoginIsRejectedNotAppliedStale(t *testing.T) {
	t.Parallel()
	root := raceLoginer{started: make(chan struct{}), proceed: make(chan struct{})}
	srv := httptest.NewServer(via.Register(root, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	lines, cancel := openStreamAt(t, srv, "/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)

	getResp, err := http.DefaultClient.Get(srv.URL + "/")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()

	loginReq := liveActionRequest(t, srv, string(page), tab, "r", 1) // Login
	loginDone := make(chan *http.Response, 1)
	go func() {
		resp, err := http.DefaultClient.Do(loginReq)
		require.NoError(t, err)
		loginDone <- resp
	}()
	<-root.started // Login now holds the stream goroutine, unbound so far

	bumpReq := liveActionRequest(t, srv, string(page), tab, "r", 0) // Bump, no cookie
	bumpDone := make(chan *http.Response, 1)
	go func() {
		resp, err := http.DefaultClient.Do(bumpReq)
		require.NoError(t, err)
		bumpDone <- resp
	}()
	// Give the cookieless dispatch time to reach its own check/enqueue point
	// while the connection is STILL unbound — the exact window I3 closes.
	time.Sleep(50 * time.Millisecond)
	close(root.proceed) // let Login finish and bind

	loginResp := <-loginDone
	loginResp.Body.Close()
	bumpResp := <-bumpDone
	defer bumpResp.Body.Close()

	assert.Equal(t, http.StatusForbidden, bumpResp.StatusCode,
		"a cookieless dispatch racing a concurrent login must be rejected against the connection it actually runs on, not the one that existed when it was queued")
}

// tabGuard is a live root with one ordinary @post action, so a dispatch either
// reaches this connection's instance (bumping hits) or does not.
type tabGuard struct{ hits *int }

func (g *tabGuard) OnInit(ctx *via.Ctx) error { ctx.Tick(time.Hour, g.tick); return nil }
func (g *tabGuard) tick(ctx *via.Ctx)         {}
func (g *tabGuard) Bump(ctx *via.Ctx)         { *g.hits++ }
func (g *tabGuard) View() h.H                 { return h.Div(h.Button(via.On("click", g.Bump))) }

// The tab id moved from the X-Via-Tab header into the viatab SIGNAL. That is a
// wire break, not a policy change: a POST that cannot present the connection's
// tab id must be rejected exactly as the header-based check rejected it, and
// must not mutate the live unit.
func TestDispatch_liveActionWithoutTheTabSignalIsRejected(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
	}{
		{"no signals at all", "{}"},
		{"empty tab", `{"viatab":""}`},
		{"forged tab", `{"viatab":"not-a-real-tab"}`},
		{"tab of the wrong JSON type", `{"viatab":12345}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			app := vt.Serve(t, via.Register(tabGuard{hits: &calls}))
			conn := app.Connect()
			defer conn.Close()

			status, _ := app.Action(0).Body(tc.body).Fire()
			assert.Equal(t, http.StatusGone, status,
				"a live action with no valid tab id must 410, not run against a throwaway instance")
			assert.Zero(t, calls, "and the connection's unit must not have been touched")
		})
	}
}

// The header is gone for good: leaving it honoured would keep a channel the
// browser can be made to attach cross-origin under some configurations, on top
// of the synchronizer token the signal already is.
func TestDispatch_theOldTabHeaderIsNoLongerHonoured(t *testing.T) {
	t.Parallel()
	calls := 0
	app := vt.Serve(t, via.Register(tabGuard{hits: &calls}))
	conn := app.Connect()
	defer conn.Close()

	req, err := http.NewRequest(http.MethodPost, app.URL()+actionURL(t, fetchPage(t, app, "/"), "r", 0), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Via-Tab", conn.TabID())
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusGone, resp.StatusCode, "the header must no longer route to a connection")
	assert.Zero(t, calls)
}

// And the belt-and-braces half: Datastar-Request still gates the JSON path, so
// a form-shaped cross-origin POST cannot be dressed up as a signal action.
func TestDispatch_tabSignalIsIgnoredWithoutTheDatastarRequestHeader(t *testing.T) {
	t.Parallel()
	calls := 0
	app := vt.Serve(t, via.Register(tabGuard{hits: &calls}))
	conn := app.Connect()
	defer conn.Close()

	req, err := http.NewRequest(http.MethodPost, app.URL()+actionURL(t, fetchPage(t, app, "/"), "r", 0),
		strings.NewReader(`{"viatab":"`+conn.TabID()+`"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.NotEqual(t, http.StatusNoContent, resp.StatusCode,
		"without Datastar-Request the body is read as a form, so a JSON tab id must not route")
	assert.Zero(t, calls)
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
// a via.Embed rather than at the root.
type formShell[C any] struct{ Body C }

func (s *formShell[C]) View() h.H { return h.Main(via.Embed(s.Body)) }

// A native form submit inside an Embed must answer with the instance the
// handler MUTATED. The re-render walks from the root, and via.Embed re-copies
// the parent's field on every render — so without carrying the acted instance
// down, the response is a pristine form: the validation error gone and the
// submitted value back to empty, as if the POST had never happened. The root
// case (below) always worked, which is what made this so easy to miss.
func TestNativeForm_insideAnEmbedKeepsTheHandlersMutations(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(formShell[validatedForm]{}))
	_, page := app.Get("/")
	url := actionURL(t, page, "0", 0)

	status, body := nativeFormPost(t, app, url, map[string]string{"name": "Widget"})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "name already taken: Widget", "the handler's validation error was discarded")
	assert.Contains(t, body, `"body__name":"Widget"`, "the submitted value was not re-seeded")
}

// The same form at the root, as the control: it must keep working exactly as
// before, so a fix that broke the root to fix the embed cannot pass.
func TestNativeForm_atTheRootKeepsTheHandlersMutations(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(validatedForm{}))
	_, page := app.Get("/")
	status, body := nativeFormPost(t, app, actionURL(t, page, "r", 0), map[string]string{"name": "Widget"})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "name already taken: Widget")
	assert.Contains(t, body, `"name":"Widget"`)
}

// initCounter counts its own OnInit runs. Substituting the acted instance into
// the response re-render must NOT re-init it — re-initing is exactly what
// would reload the data the handler just changed — while a fresh sibling embed
// still gets its own OnInit.
type initCounter struct {
	inits int
	saved string
}

func (c *initCounter) OnInit(ctx *via.Ctx) error { c.inits++; return nil }
func (c *initCounter) Save(ctx *via.Ctx)         { c.saved = "true" }
func (c *initCounter) View() h.H {
	return via.PostForm(c.Save,
		h.P(h.ID("inits"), h.Str(c.inits)),
		h.P(h.ID("saved"), h.Str(c.saved)),
		h.Button(h.Str("go")))
}

func TestNativeForm_actedEmbedIsNotReInited(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(formShell[initCounter]{}))
	_, page := app.Get("/")
	require.Contains(t, page, `<p id="inits">1</p>`)

	status, body := nativeFormPost(t, app, actionURL(t, page, "0", 0), nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `<p id="saved">true</p>`, "the mutation must survive the re-render")
	assert.Contains(t, body, `<p id="inits">1</p>`, "the acted embed's OnInit must not run a second time")
}

// A live page's native form submit that arrives WITHOUT the tab field is a
// missing-field failure, not a served-plain one — the page was live, which is
// precisely why the field had to be there. The old message asserted the
// opposite and sent people looking at their OnInit.
func TestNativeForm_missingTabFieldSaysWhatActuallyHappened(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(liveNamedForm{}))
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
// via.Each row or an Embed's View is the same shape) — that is the only place a
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
	srv := serve(t, via.Register(signalGatedAdmin{}))

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
	srv := serve(t, via.Register(signalGatedAdmin{}))

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
	srv := serve(t, via.Register(echoedSignal{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), `{"name":"zed"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "saw: zed")
}

// plainRootLiveKid is a PLAIN root (its own View renders no State) carrying a
// LIVE embed, plus a native PostForm at the root. The submit falls to
// dispatchPlain — the root is not a registered live unit — and the full page it
// answers with is what the browser replaces the document with, so it must still
// bootstrap the stream the live embed needs.
type plainRootLiveKid struct {
	Clock embeddedClock
	name  string
}

func (p *plainRootLiveKid) Save(ctx *via.Ctx) { p.name = ctx.Request().FormValue("name") }
func (p *plainRootLiveKid) View() h.H {
	return h.Div(
		h.P(h.ID("name"), h.Str(p.name)),
		via.PostForm(p.Save, h.Input(h.Name("name")), h.Button(h.Str("go"))),
		via.Embed(p.Clock),
	)
}

type embeddedClock struct{ n via.State[int] }

func (c *embeddedClock) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, c.beat)
	return nil
}
func (c *embeddedClock) beat(ctx *via.Ctx) { c.n.Set(c.n.Get() + 1) }
func (c *embeddedClock) View() h.H         { return h.Div(c.n.Display()) }

func TestNativeForm_plainRootKeepsALiveEmbedsBootstrap(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(plainRootLiveKid{}))
	_, page := app.Get("/")
	require.Contains(t, page, "data-init", "the GET must already bootstrap the stream")

	status, body := nativeFormPost(t, app, actionURL(t, page, "r", 0), map[string]string{"name": "zed"})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `<p id="name">zed</p>`)
	assert.Contains(t, body, "data-init", "the live embed is dead after the submit without a bootstrap")
	assert.Contains(t, body, "/_via/sse", "the bootstrap must name the stream URL")
}

// disclosure is the two-pass discovery repro: a Bind()ed Signal (Mode) chosen
// by the user opens a lazy branch holding a second Bind()ed Signal (Name) and
// an in-branch handler. keepalive makes the very same composition live, so both
// transports are driven through one shape; it never changes after Register, so
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

func (d *disclosure) Pick(ctx *via.Ctx)   {}
func (d *disclosure) Save(ctx *via.Ctx)   { d.seen = "saw:" + d.Name.Get() }
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
	app := vt.Serve(t, via.Register(disclosure{}))

	code, body := app.Action(1).Body(`{"mode":"x","name":"bob"}`).Fire()
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "seen: saw:bob",
		"a Bind()ed signal inside a branch another posted signal opens must still reach the handler")
	assert.NotContains(t, body, `"name":""`,
		"the response must not wipe the client's value for that slot")
}

func TestDispatchPlain_inBranchActionStaysUndispatchableWithoutTheServerRender(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(disclosure{}))

	_, open := app.Get("/?mode=x")
	url := actionURL(t, open, "r", 1)

	code, body := app.Action(0).Raw(url).Body(`{"mode":"x"}`).Fire()
	assert.Equal(t, http.StatusGone, code,
		"a handler is dispatchable only if the render the client did not influence bound it")
	assert.Contains(t, body, "does not bind it")
}

func TestDispatchLive_hydratesADisclosedSignalAfterTheBranchIsOpened(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(disclosure{keepalive: true}))
	conn := app.Connect()

	code, _ := app.Action(0).Over(conn).Body(`{"mode":"x"}`).Fire()
	require.Less(t, code, 300, "the disclosure toggle must be accepted")
	conn.Await("reveal")

	code, _ = app.Action(2).Over(conn).Body(`{"mode":"x","name":"bob"}`).Fire()
	require.Less(t, code, 300)
	assert.Contains(t, conn.Await("seen: saw:bob"), "seen: saw:bob")
}

func TestDispatchLive_inBranchActionStaysUndispatchableUntilTheBranchIsPushed(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(disclosure{keepalive: true}))
	conn := app.Connect()

	_, open := app.Get("/?mode=x")
	url := actionURL(t, open, "r", 1)

	code, body := app.Action(0).Over(conn).Raw(url).Body(`{"mode":"x"}`).Fire()
	assert.Equal(t, http.StatusGone, code)
	assert.Contains(t, body, "does not bind it")
}

// --- bounded-fixpoint discovery regressions (one root cause, three symptoms).
//
// The disclosure fixture above has no embed, no session and no OnArg, which is
// exactly why all three slipped through: every one of them needs a SECOND
// discovery pass, which any page with a Bind()ed slot in the posted body takes.

// fixKid is a PLAIN embedded child whose OnInit seeds the state its handler
// reads — the state a later discovery pass used to skip.
type fixKid struct {
	loaded string
	seen   string
}

func (k *fixKid) OnInit(ctx *via.Ctx) error { k.loaded = "init"; return nil }
func (k *fixKid) Save(ctx *via.Ctx)         { k.seen = "seen:" + k.loaded }
func (k *fixKid) View() h.H {
	return h.Div(h.P(h.Str(k.seen)), h.Button(via.On("click", k.Save), h.Str("save")))
}

type fixParent struct {
	Q via.Signal[string]
	K fixKid
}

func (p *fixParent) View() h.H { return h.Div(h.Input(p.Q.Bind()), via.Embed(p.K)) }

func TestDispatchPlain_embedActionRunsOnAnInitedCopyAfterASecondPass(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(fixParent{}))

	code, body := app.EmbedAction("0", 0).Body(`{"q":"x"}`).Fire()
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "<p>seen:init</p>",
		"a posted Bind()ed signal forces a second discovery pass; the embed's action must still run on a copy whose OnInit ran")
}

type sessUser struct{ Name string }

// sessInAction mints the session in OnInit and reads it back in the handler —
// the same request, so the cookie is on the response and never in the request.
type sessInAction struct {
	Q   via.Signal[string]
	who string
}

func (s *sessInAction) OnInit(ctx *via.Ctx) error {
	ctx.Session().Put(sessUser{Name: "ann"})
	return nil
}

func (s *sessInAction) Save(ctx *via.Ctx) {
	u, ok := ctx.Session().Get[sessUser]()
	s.who = fmt.Sprintf("who:%v:%s", ok, u.Name)
}

func (s *sessInAction) View() h.H {
	return h.Div(h.Input(s.Q.Bind()), h.P(h.Str(s.who)), h.Button(via.On("click", s.Save), h.Str("save")))
}

func TestDispatchPlain_sessionMintedInOnInitReachesTheHandlerOnce(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(sessInAction{}))
	page := fetchPage(t, app, "/")

	req, err := http.NewRequest(http.MethodPost, app.URL()+actionURL(t, page, "r", 0), strings.NewReader(`{"q":"x"}`))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Contains(t, string(b), "who:true:ann",
		"a session created in OnInit must be the same session the handler reads, across discovery passes")
	assert.LessOrEqual(t, len(resp.Header.Values("Set-Cookie")), 1,
		"a second pass must not mint a second session and a second Set-Cookie")
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
	app := vt.Serve(t, via.Register(argRows{}))
	_, page := app.Get("/")
	url := strings.Replace(actionURL(t, page, "r", 0), "a=1", "a=3", 1)
	require.Contains(t, url, "a=3")

	code, body := app.Action(0).Raw(url).Body(`{"filter":"all"}`).Fire()
	assert.Equal(t, http.StatusGone, code,
		"the executed render is an intersection with auth per (handler, arg), not per handler")
	assert.NotContains(t, body, "del:3")
}
