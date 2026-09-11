package via_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

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

// liveRedirector is a live root whose action queues a Redirect — before A2 a
// live action's Redirect was silently dropped (fire-and-forget, no response
// to carry it on).
type liveRedirector struct{}

func (c *liveRedirector) OnConnect(ctx *via.Ctx) error { return nil }
func (c *liveRedirector) Go(ctx *via.Ctx)              { ctx.Redirect("/dest") }
func (c *liveRedirector) View() h.H                    { return h.Div(h.Button(via.OnClick(c.Go))) }

func TestDispatch_redirectFromLiveActionShipsScript(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(liveRedirector{}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")

		req, err := http.NewRequest(http.MethodPost, app.URL()+actionURL(t, page, 0, 0), strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Datastar-Request", "true")
		req.Header.Set("X-Via-Tab", conn.TabID())
		resp, err := app.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Contains(t, resp.Header.Get("Content-Type"), "text/javascript",
			"a live action's Redirect must ship an executable script, not a silent 204")
		assert.Contains(t, resp.Header.Get("datastar-script-attributes"), `"data-via-to":"/dest"`)
	})
}

// islandRedirector is a STATELESS island (dispatchStateless, not
// liveRunAction) whose action queues a Redirect.
type islandRedirector struct{}

func (r *islandRedirector) Go(ctx *via.Ctx) { ctx.Redirect("/dest") }
func (r *islandRedirector) View() h.H       { return h.Div(h.Button(via.OnClick(r.Go))) }

type islandRedirectorParent struct{ I islandRedirector }

func (p *islandRedirectorParent) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_redirectFromStatelessIslandActionShipsScript(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(islandRedirectorParent{}))
	_, page := do(t, srv, http.MethodGet, "/", "")

	req, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, 1, 0), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Contains(t, resp.Header.Get("Content-Type"), "text/javascript",
		"a stateless island's Redirect must ship an executable script, not a silent 204")
	assert.Contains(t, resp.Header.Get("datastar-script-attributes"), `"data-via-to":"/dest"`)
}

// sigIsland's signals are only Bound (never Displayed), so Setting one never
// changes the rendered HTML — the only way a client sees the new value is the
// container's data-signals attribute. Other exists solely so Reset's Set of
// Name has a sibling to leave alone: the response must declare Name and NOT
// Other, proving the patch is restricted to what the action actually wrote
// rather than the whole slot table.
type sigIsland struct{ Name, Other via.Signal[string] }

func (s *sigIsland) Reset(ctx *via.Ctx) { s.Name.Set("resetted") }
func (s *sigIsland) View() h.H {
	return h.Div(s.Name.Bind(), s.Other.Bind(), h.Button(via.OnClick(s.Reset)))
}

type sigPage struct{ I sigIsland }

func (p *sigPage) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_signalSetInIslandActionReachesClient(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(sigPage{}))

	_, page := do(t, srv, http.MethodGet, "/", "")
	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, 1, 0), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"a Signal.Set with no visible HTML change must still ship a patch, not 204")
	assert.Contains(t, body, `"i0_s0":"resetted"`, "the Set signal must reach the client")
	assert.NotContains(t, body, `"i0_s1":`,
		"an untouched sibling signal must not be declared — Set restricts the patch, it doesn't broadcast the whole table")
}

// guardedIsland is embedded behind a RequireSession guard on its mount.
type guardedIsland struct{}

func (g *guardedIsland) Ping(ctx *via.Ctx) {}
func (g *guardedIsland) View() h.H         { return h.Div(h.Button(via.OnClick(g.Ping))) }

type guardedParent struct{ I guardedIsland }

func (p *guardedParent) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_islandActionRunsGuards(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/g", guardedParent{}, via.RequireSession[acct]("/login"))
	srv := serve(t, r)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/g/_via/a/1/0", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := (&http.Client{CheckRedirect: noFollow}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "the guard must gate the island action route")
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
		h.Button(via.OnClick(p.Bump), h.Str("+")),     // action 0
		via.PostForm(p.Save, h.Button(h.Str("save"))), // action 1
	)
}

func TestDispatch_nativeFormUsesSameActionTable(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(mixedPage{}))

	_, page := do(t, srv, http.MethodGet, "/", "")
	assert.Contains(t, page, `@post('/_via/a/0/0?v=`, "the @post binding claims action 0")
	assert.Contains(t, page, `action="/_via/a/0/1?v=`, "PostForm claims the next slot in the same table")

	resp, _ := do(t, srv, http.MethodPost, actionURL(t, page, 0, 0), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the @post action must dispatch")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.Close()
	req, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, 0, 1), &buf)
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
type panicLive struct{}

func (p *panicLive) OnConnect(ctx *via.Ctx) error { return nil }
func (p *panicLive) Boom(ctx *via.Ctx)            { panic("boom") }
func (p *panicLive) Ping(ctx *via.Ctx)            {}
func (p *panicLive) View() h.H {
	return h.Div(h.Button(via.OnClick(p.Boom)), h.Button(via.OnClick(p.Ping)))
}

func TestDispatch_liveActionPanicAnswers500NotStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(panicLive{}))
		conn := app.Connect()

		status, _ := app.Action(0).Tab(conn.TabID()).Fire()
		assert.Equal(t, http.StatusInternalServerError, status, "a live action panic must answer 500")

		status2, _ := app.Action(1).Tab(conn.TabID()).Fire()
		assert.Equal(t, http.StatusNoContent, status2, "the connection must survive the panic and keep dispatching")
	})
}

// paramIsland is embedded under a parametrised mount; Bump changes its
// visible count so the action's response is a real patch, not a 204.
type paramIsland struct{ n int }

func (k *paramIsland) Bump(ctx *via.Ctx) { k.n++ }
func (k *paramIsland) View() h.H         { return h.Div(h.Str(k.n), h.Button(via.OnClick(k.Bump))) }

type paramParent struct{ I paramIsland }

func (p *paramParent) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_pushUnderParamMountRendersConcreteBase(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/thread/{id}", paramParent{})
	srv := serve(t, r)

	_, page := do(t, srv, http.MethodGet, "/thread/7", "")
	assert.Contains(t, page, `@post('/thread/7/_via/a/1/0?v=`)

	resp, body := do(t, srv, http.MethodPost, actionURL(t, page, 1, 0), "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, body, "{id}", "the re-rendered action URL must not carry the pattern wildcard")
	assert.Contains(t, body, `/thread/7/_via/a/1/0`, "the re-rendered action URL must carry the concrete segment")
}

// unsafeRoot and unsafeIsland both bump a visible counter alongside an
// unsafe Redirect, so a rejected redirect's fallback response is provably a
// normal patch (the counter's new value), not a crash or a hang.
type unsafeRoot struct{ n int }

func (u *unsafeRoot) Go(ctx *via.Ctx) { u.n++; ctx.Redirect("javascript:alert(1)") }
func (u *unsafeRoot) View() h.H       { return h.Div(h.Str(u.n), h.Button(via.OnClick(u.Go))) }

type unsafeIsland struct{ n int }

func (u *unsafeIsland) Go(ctx *via.Ctx) { u.n++; ctx.Redirect("javascript:alert(1)") }
func (u *unsafeIsland) View() h.H       { return h.Div(h.Str(u.n), h.Button(via.OnClick(u.Go))) }

type unsafeParent struct{ I unsafeIsland }

func (p *unsafeParent) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_unsafeRedirectFallsBackEverywhere(t *testing.T) {
	t.Parallel()

	t.Run("stateless root", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(unsafeRoot{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, body := do(t, srv, http.MethodPost, actionURL(t, page, 0, 0), "{}")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript")
		assert.Contains(t, body, ">1<", "the mutation must still land in the fallback patch")
	})

	t.Run("stateless island", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(unsafeParent{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, body := do(t, srv, http.MethodPost, actionURL(t, page, 1, 0), "{}")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript")
		assert.Contains(t, body, ">1<", "the mutation must still land in the fallback patch")
	})

	t.Run("native form", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(loginForm{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, 0, 0), "name", "evil")
		assert.NotEqual(t, http.StatusSeeOther, resp.StatusCode, "an unsafe redirect must not 303")
		assert.Empty(t, resp.Header.Get("Location"))
	})
}

// staleDigest rewrites url's ?v=/&v= shape digest to an obviously wrong
// value, simulating a client whose page has gone stale (or a forged one).
func staleDigest(t *testing.T, url string) string {
	t.Helper()
	i := strings.LastIndex(url, "v=")
	require.GreaterOrEqual(t, i, 0, "url %q carries no shape digest", url)
	return url[:i] + "v=00000000"
}

// swapActionIndex rewrites url's trailing {n} path segment to n, keeping its
// ?v= shape digest intact — a valid digest with a forged out-of-range index,
// the shape a shape-digest check alone cannot catch (the digest fixes the
// action COUNT, not which index was requested).
func swapActionIndex(t *testing.T, url, n string) string {
	t.Helper()
	i := strings.Index(url, "?")
	require.GreaterOrEqual(t, i, 0, "url %q carries no ?v=", url)
	path, q := url[:i], url[i:]
	j := strings.LastIndex(path, "/")
	require.GreaterOrEqual(t, j, 0)
	return path[:j+1] + n + q
}

// swapIslandIndex rewrites url's {island} path segment to island, keeping its
// {n} segment and ?v= shape digest intact — a valid digest with a forged
// out-of-range island id, the same "digest fixes the count, not the index"
// gap swapActionIndex covers for the action segment.
func swapIslandIndex(t *testing.T, url, island string) string {
	t.Helper()
	i := strings.Index(url, "?")
	require.GreaterOrEqual(t, i, 0, "url %q carries no ?v=", url)
	path, q := url[:i], url[i:]
	j := strings.LastIndex(path, "/")
	require.GreaterOrEqual(t, j, 0)
	k := strings.LastIndex(path[:j], "/")
	require.GreaterOrEqual(t, k, 0)
	return path[:k+1] + island + path[j:] + q
}

func TestDispatch_forgedActionIndexWithValidDigestIsGone(t *testing.T) {
	t.Run("stateless", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(counter{count: &store{}}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		url := actionURL(t, page, 0, 0)
		for _, n := range []string{"99", "-1"} {
			resp, _ := do(t, srv, http.MethodPost, swapActionIndex(t, url, n), "{}")
			assert.Equal(t, http.StatusGone, resp.StatusCode, "forged n=%s must 410, not panic/misroute", n)
		}
	})

	t.Run("live", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			app := vt.Serve(t, via.Register(liveClicker{}))
			conn := app.Connect()
			page := fetchPage(t, app, "/")
			url := actionURL(t, page, 0, 0)
			for _, n := range []string{"99", "-1"} {
				status, _ := app.Action(0).Raw(swapActionIndex(t, url, n)).Tab(conn.TabID()).Fire()
				assert.Equal(t, http.StatusGone, status, "forged n=%s must 410, not panic/misroute", n)
			}
			// The connection must still be usable — the recovered panic path
			// this replaces must not be the only thing standing between a
			// forged n and a crashed stream.
			status, _ := app.Action(0).Raw(url).Tab(conn.TabID()).Fire()
			assert.Equal(t, http.StatusNoContent, status)
		})
	})
}

func TestDispatch_staleShapeAnswers410OnEveryPath(t *testing.T) {
	t.Parallel()

	t.Run("stateless root", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(counter{count: &store{}}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, _ := do(t, srv, http.MethodPost, staleDigest(t, actionURL(t, page, 0, 1)), "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode)
	})

	t.Run("stateless island", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Register(sigPage{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, _ := do(t, srv, http.MethodPost, staleDigest(t, actionURL(t, page, 1, 0)), "{}")
		assert.Equal(t, http.StatusGone, resp.StatusCode)
	})

	// The entire body is the synctest bubble, so no t.Parallel() (its *testing.T
	// can't call it — see CONVENTIONS.md's synctest carve-out).
	t.Run("live unit", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			app := vt.Serve(t, via.Register(liveRedirector{}))
			conn := app.Connect()
			page := fetchPage(t, app, "/")

			req, err := http.NewRequest(http.MethodPost, app.URL()+staleDigest(t, actionURL(t, page, 0, 0)), strings.NewReader("{}"))
			require.NoError(t, err)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Datastar-Request", "true")
			req.Header.Set("X-Via-Tab", conn.TabID())
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
// reproduced here on the stateless path.
type branchedView struct{ st *toggleState }

func (b *branchedView) Save(ctx *via.Ctx)   { b.st.mark("save") }
func (b *branchedView) Delete(ctx *via.Ctx) { b.st.mark("delete") }
func (b *branchedView) Flip(ctx *via.Ctx)   { b.st.flip() }
func (b *branchedView) View() h.H {
	locked, ran := b.st.snapshot()
	if locked {
		return h.Div(
			h.Button(via.OnClick(b.Delete)), // locked: Delete=0
			h.Button(via.OnClick(b.Flip)),   // locked: Flip=1 (Save is gone)
			h.P(h.Str("locked:"), h.Str(ran)),
		)
	}
	return h.Div(
		h.Button(via.OnClick(b.Save)),   // unlocked: Save=0
		h.Button(via.OnClick(b.Delete)), // unlocked: Delete=1
		h.Button(via.OnClick(b.Flip)),   // unlocked: Flip=2
		h.P(h.Str("unlocked:"), h.Str(ran)),
	)
}

// xmIsland is a live, dep-free island mountable at any path — the vehicle for
// proving a live tab from one mount can't drive another mount's action table.
type xmIsland struct{ fired *int }

func (x *xmIsland) OnConnect(*via.Ctx) error { return nil }
func (x *xmIsland) Fire(*via.Ctx)            { *x.fired++ }
func (x *xmIsland) View() h.H                { return h.Div(h.Button(via.OnClick(x.Fire))) }

func TestDispatch_liveActionCannotCrossMounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var aFired, cFired int
		r := via.NewRouter()
		r.Mount("/a", xmIsland{fired: &aFired})
		r.Mount("/c", xmIsland{fired: &cFired})
		srv := liveServer(t, r)

		_, aPage := do(t, srv, http.MethodGet, "/a", "")
		aURL := actionURL(t, aPage, 0, 0)

		cLines, cancel := openStreamAt(t, srv, "/c/_via/sse")
		defer cancel()
		cTab := awaitTabID(t, cLines)
		synctest.Wait()

		resp, _ := post(t, srv, aURL, "{}", map[string]string{
			"Sec-Fetch-Site": "same-origin",
			"X-Via-Tab":      cTab,
		})
		assert.Equal(t, http.StatusGone, resp.StatusCode, "a /c tab must not drive /a's action table")
		assert.Zero(t, aFired, "the /a action must not have run")
	})
}

func TestDispatch_branchedViewCannotMisroute(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Register(branchedView{st: &toggleState{}}))

	_, unlocked := do(t, srv, http.MethodGet, "/", "")
	staleDelete := actionURL(t, unlocked, 0, 1) // Delete, while unlocked
	flip := actionURL(t, unlocked, 0, 2)

	// Flip to locked: Save disappears from the action table, so every later
	// index shifts down by one — index 1 (Delete, under the old shape) is
	// now Flip.
	resp, lockedBody := do(t, srv, http.MethodPost, flip, "{}")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, lockedBody, "locked:", "Flip must have taken effect")

	// Replay the pre-flip Delete URL. It still names index 1, which under the
	// NEW (locked) shape is Flip — a real misroute (toggling back to
	// unlocked) if dispatched. The digest baked into the URL no longer
	// matches the post-flip render, so this must 410, not run Flip.
	resp2, _ := do(t, srv, http.MethodPost, staleDelete, "{}")
	assert.Equal(t, http.StatusGone, resp2.StatusCode,
		"a stale action URL from before a branch flip must 410, not misroute")

	_, after := do(t, srv, http.MethodGet, "/", "")
	assert.Contains(t, after, "locked:", "the stale click must not have misrouted into Flip (back to unlocked)")
	assert.NotContains(t, after, "delete", "Delete must not have run either — the click must not have run at all")
}

// liveForm is a live root whose View carries a native PostForm — the case
// with no example coverage: a real browser form submit inside a live unit
// can't set X-Via-Tab (that's fetch-only), so it depends on PostForm's
// hidden fallback field to route to the connection at all.
type liveForm struct {
	got   via.State[string]
	calls *int
}

func (f *liveForm) OnConnect(*via.Ctx) error { return nil }
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

// nativeFormPost submits a native form POST with no X-Via-Tab header — the
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

func TestDispatch_liveFormFieldFallbackRunsHandlerAndRerendersFullPage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		app := vt.Serve(t, via.Register(liveForm{calls: &calls}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")
		assert.Contains(t, page, `data-attr-value="$_viatab"`,
			"a live unit's PostForm must reactively fill the fallback field from the tab signal")
		formURL := actionURL(t, page, 0, 0)

		status, body := nativeFormPost(t, app, formURL, map[string]string{
			"name":    "zed",
			"_viatab": conn.TabID(),
		})

		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, "got:zed", "the handler must have run against the live unit's own state")
		assert.Equal(t, 1, calls)
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
		aFormURL := actionURL(t, aPage, 0, 0)

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
