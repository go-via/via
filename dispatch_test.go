package via_test

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
type liveRedirector struct{}

func (c *liveRedirector) OnConnect(ctx *via.Ctx) error { return nil }
func (c *liveRedirector) Go(ctx *via.Ctx)              { ctx.Redirect("/dest") }
func (c *liveRedirector) View() h.H                    { return h.Div(h.Button(via.On("click", c.Go))) }

func TestDispatch_redirectFromLiveActionDoesNotShipAScript(t *testing.T) {
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

		assert.Equal(t, http.StatusNoContent, resp.StatusCode,
			"a live action's Redirect can no longer navigate; it answers its normal 204")
		assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript")
		assert.Empty(t, resp.Header.Get("datastar-script-attributes"))
	})
}

// islandRedirector is a STATELESS island (dispatchStateless, not
// liveRunAction) whose action queues a Redirect.
type islandRedirector struct{}

func (r *islandRedirector) Go(ctx *via.Ctx) { ctx.Redirect("/dest") }
func (r *islandRedirector) View() h.H       { return h.Div(h.Button(via.On("click", r.Go))) }

type islandRedirectorParent struct{ I islandRedirector }

func (p *islandRedirectorParent) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_redirectFromStatelessIslandActionDoesNotShipAScript(t *testing.T) {
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

	assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript",
		"a stateless island's Redirect can no longer navigate; no script ships")
	assert.Empty(t, resp.Header.Get("datastar-script-attributes"))
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
	return h.Div(s.Name.Bind(), s.Other.Bind(), h.Button(via.On("click", s.Reset)))
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

// guardedIsland is embedded under a parent whose own OnInit session-gates
// the whole mount, replacing the removed guard mechanism.
type guardedIsland struct{}

func (g *guardedIsland) Ping(ctx *via.Ctx) {}
func (g *guardedIsland) View() h.H         { return h.Div(h.Button(via.On("click", g.Ping))) }

type guardedParent struct{ I guardedIsland }

func (p *guardedParent) OnInit(ctx *via.Ctx) error {
	if _, ok := ctx.Session().Get[acct](); !ok {
		ctx.Redirect("/login")
	}
	return nil
}
func (p *guardedParent) View() h.H { return h.Div(via.Embed(p.I)) }

func TestDispatch_islandActionRunsOnInitRedirect(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/g", guardedParent{})
	srv := serve(t, r)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/g/_via/a/1/0", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := (&http.Client{CheckRedirect: noFollow}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "the parent's OnInit Redirect must gate the island action route")
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
	return h.Div(h.Button(via.On("click", p.Boom)), h.Button(via.On("click", p.Ping)))
}

func TestDispatch_liveActionPanicAnswers500NotStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(panicLive{}))
		conn := app.Connect()

		status, _ := app.Action(0).Live(conn).Fire()
		assert.Equal(t, http.StatusInternalServerError, status, "a live action panic must answer 500")

		status2, _ := app.Action(1).Live(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status2, "the connection must survive the panic and keep dispatching")
	})
}

// branchy's second button (action 1) only exists in the View after Reveal
// fires and its push renders it — the connect-time render and the initial
// stateless GET both show only Reveal.
type branchy struct{ shown via.State[bool] }

func (b *branchy) OnConnect(ctx *via.Ctx) error { return nil }
func (b *branchy) Reveal(ctx *via.Ctx)          { b.shown.Set(true) }
func (b *branchy) Extra(ctx *via.Ctx)           {}
func (b *branchy) View() h.H {
	kids := []h.H{h.Button(via.On("click", b.Reveal))}
	if b.shown.Get() {
		kids = append(kids, h.Button(via.On("click", b.Extra)))
	}
	return h.Div(kids...)
}

// TestDispatch_liveActionAfterShapeChangeNeedsThePushedURL is C2: a
// connection's action table can change shape mid-connection (a push adds a
// button), and the URL for the new action only ever appears in what THIS
// connection pushed — never in the stateless page vt cached before Connect.
// Sourcing it from Conn's own pushed markup (vt.Action.Live) finds it and
// dispatches successfully; sourcing it from the stale page (plain
// vt.Action) can't find action 1 at all, because the page vt fetched never
// had it.
func TestDispatch_liveActionAfterShapeChangeNeedsThePushedURL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Register(branchy{}))
		conn := app.Connect()

		status, _ := app.Action(0).Live(conn).Fire()
		require.Equal(t, http.StatusNoContent, status, "Reveal must run and push the new button")

		// The pushed element-patch carrying Extra's URL is read off the real
		// SSE socket by a goroutine synctest.Wait() can't settle (blocking
		// network I/O is not "durably blocked" — see I4); Await blocks for it
		// instead of racing the reader, which is what made this test flaky
		// under -cpu 1.
		conn.Await("_via/a/0/1")

		status, _ = app.Action(1).Live(conn).Fire()
		assert.Equal(t, http.StatusNoContent, status,
			"Extra's URL only exists in what this connection pushed — vt.Action.Live must read it from there")

		// A stateless GET is a fresh, unrelated instance (shown resets to
		// false) — it can never carry this connection's action 1, proving
		// plain vt.Action's stateless-page lookup could not have found it
		// either; only Conn's own pushed markup has it.
		page := fetchPage(t, app, "/")
		assert.NotContains(t, page, "/_via/a/0/1",
			"a fresh stateless render never reflects the live connection's Reveal")
	})
}

// paramIsland is embedded under a parametrised mount; Bump changes its
// visible count so the action's response is a real patch, not a 204.
type paramIsland struct{ n int }

func (k *paramIsland) Bump(ctx *via.Ctx) { k.n++ }
func (k *paramIsland) View() h.H         { return h.Div(h.Str(k.n), h.Button(via.On("click", k.Bump))) }

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
func (u *unsafeRoot) View() h.H       { return h.Div(h.Str(u.n), h.Button(via.On("click", u.Go))) }

type unsafeIsland struct{ n int }

func (u *unsafeIsland) Go(ctx *via.Ctx) { u.n++; ctx.Redirect("javascript:alert(1)") }
func (u *unsafeIsland) View() h.H       { return h.Div(h.Str(u.n), h.Button(via.On("click", u.Go))) }

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

// xmIsland is a live, dep-free island mountable at any path — the vehicle for
// proving a live tab from one mount can't drive another mount's action table.
type xmIsland struct{ fired *int }

func (x *xmIsland) OnConnect(*via.Ctx) error { return nil }
func (x *xmIsland) Fire(*via.Ctx)            { *x.fired++ }
func (x *xmIsland) View() h.H                { return h.Div(h.Button(via.On("click", x.Fire))) }

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

func TestDispatch_liveFormFieldFallbackRunsHandlerAndReturns200(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		app := vt.Serve(t, via.Register(liveForm{calls: &calls}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")
		assert.Contains(t, page, `data-attr:value="$_viatab"`,
			"a live unit's PostForm must reactively fill the fallback field from the tab signal")
		formURL := actionURL(t, page, 0, 0)

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

// nativeFormPanic's View panics on re-render once boom is flipped —
// reproducing a native <form> submit whose mutation succeeds but whose
// fresh-instance re-render (dispatchLive, native mode) then panics. boom is
// a shared pointer, not per-instance State: the fresh instance the render
// runs against is a different value from the one Save mutated, so only a
// pointer shared across instances can carry the trigger between them.
type nativeFormPanic struct{ boom *bool }

func (f *nativeFormPanic) OnConnect(*via.Ctx) error { return nil }
func (f *nativeFormPanic) Save(ctx *via.Ctx)        { *f.boom = true }
func (f *nativeFormPanic) View() h.H {
	if *f.boom {
		panic("via_test: native re-render exploded")
	}
	return h.Div(via.PostForm(f.Save, h.Input(h.Name("name")), h.Button(h.Str("save"))))
}

// A panic in a native form's post-mutation re-render must answer 500, not
// hang the POST forever — the render runs on the dispatching POST's own
// goroutine (see dispatch.go's dispatchLive), outside liveRunAction's own
// recover, after the mutation already succeeded.
func TestDispatch_liveNativeFormPanicOnRerenderAnswers500NotHang(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := new(bool)
		app := vt.Serve(t, via.Register(nativeFormPanic{boom: boom}))
		conn := app.Connect()
		page := fetchPage(t, app, "/")
		formURL := actionURL(t, page, 0, 0)

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
// client's jar (a session established by an earlier stateless action).
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
// ever route through the tab handshake (dispatchStateless refuses them, see
// dispatch.go), so establishing the session ahead of connecting needs a
// separate, stateless mount (loginComp, from sess_test.go) sharing the same
// router-wide session manager. Bump is the live action a stolen tab id would
// try to drive.
type sessionLive struct{ n via.State[int] }

func (s *sessionLive) OnConnect(*via.Ctx) error { return nil }
func (s *sessionLive) Bump(ctx *via.Ctx)        { s.n.Set(s.n.Get() + 1) }
func (s *sessionLive) Peek(ctx *via.Ctx)        { ctx.Session().Get[member]() } // read-only, never mints
func (s *sessionLive) View() h.H {
	return h.Div(s.n.Display(),
		h.Button(via.On("click", s.Bump)), // action 0
		h.Button(via.On("click", s.Peek))) // action 1
}

// liveActionRequest builds a raw dispatch POST against island/n using the
// page's currently-rendered action URL, with tab as its X-Via-Tab header —
// bypassing any cookie jar, so the caller controls exactly what (if any)
// session cookie rides along.
func liveActionRequest(t *testing.T, srv *httptest.Server, page, tab string, island, n int) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, island, n), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Via-Tab", tab)
	return req
}

// A live connection opened under a real session must reject a dispatch that
// doesn't carry that same session — the tab id alone (a leaked/stolen one,
// with no cookie at all, exactly as a cross-origin request would arrive with
// the origin floor open) is no longer a sufficient credential.
func TestDispatch_liveActionUnderASessionRejectsAMismatchedSession(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/login", loginComp{})  // stateless — establishes the session cookie
	r.Mount("/live", sessionLive{}) // Live root, shares the router-wide session manager
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	owner := jarClient(t)

	loginResp, err := owner.Get(srv.URL + "/login")
	require.NoError(t, err)
	loginPage, err := io.ReadAll(loginResp.Body)
	require.NoError(t, err)
	loginResp.Body.Close()

	signInReq, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, string(loginPage), 0, 0), strings.NewReader("{}"))
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

	req := liveActionRequest(t, srv, string(page), tab, 0, 0)
	// No cookie at all on this request — the stolen-tab-id, no-session attack.
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a dispatch against a session-bound connection with no session must be rejected")

	// The rightful owner, same tab, same session cookie, must still work —
	// the check rejects a MISMATCH, not the connection itself.
	ownReq := liveActionRequest(t, srv, string(page), tab, 0, 0)
	ownResp, err := owner.Do(ownReq)
	require.NoError(t, err)
	defer ownResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, ownResp.StatusCode,
		"the connecting session's own dispatch must still succeed")
}

// Neighbour of the mismatch rejection: an ANONYMOUS live connection (no
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

	req := liveActionRequest(t, srv, string(page), tab, 0, 0) // Bump, no cookie anywhere
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
	signInReq, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, string(loginPage), 0, 0), strings.NewReader("{}"))
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
	peekReq := liveActionRequest(t, srv, string(page), tab, 0, 1) // Peek
	peekReq.AddCookie(&http.Cookie{Name: "via_session", Value: cookieValue(t, attacker, srv.URL, "via_session")})
	peekResp, err := http.DefaultClient.Do(peekReq)
	require.NoError(t, err)
	peekResp.Body.Close()

	// The victim's own later cookieless dispatch must still succeed — the
	// connection must NOT have been captured by the attacker's cookie.
	bumpReq := liveActionRequest(t, srv, string(page), tab, 0, 0) // Bump, no cookie
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

	peekReq := liveActionRequest(t, srv, string(page), tab, 0, 1) // Peek, no cookie
	peekResp, err := http.DefaultClient.Do(peekReq)
	require.NoError(t, err)
	peekResp.Body.Close()

	bumpReq := liveActionRequest(t, srv, string(page), tab, 0, 0) // Bump, still no cookie
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

func (p *liveLoginer) OnConnect(*via.Ctx) error { return nil }
func (p *liveLoginer) Bump(ctx *via.Ctx)        { p.n.Set(p.n.Get() + 1) }                 // action 0
func (p *liveLoginer) Login(ctx *via.Ctx)       { ctx.Session().Put(member{Name: "bob"}) } // action 1
func (p *liveLoginer) Rotate(ctx *via.Ctx)      { ctx.Session().Rotate() }                 // action 2
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

	loginReq := liveActionRequest(t, srv, string(page), tab, 0, 1) // Login
	loginResp, err := http.DefaultClient.Do(loginReq)
	require.NoError(t, err)
	loginResp.Body.Close()
	require.NotEmpty(t, loginResp.Header.Get("Set-Cookie"), "a live Login must still mint the session cookie")

	bumpReq := liveActionRequest(t, srv, string(page), tab, 0, 0) // Bump, no cookie
	bumpResp, err := http.DefaultClient.Do(bumpReq)
	require.NoError(t, err)
	defer bumpResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, bumpResp.StatusCode,
		"the tab id must stop being a bearer credential the instant it logs in")
}

// onConnectLoginer establishes its session in OnConnect — the pattern the
// README recommends — rather than through a later action.
type onConnectLoginer struct{ n via.State[int] }

func (o *onConnectLoginer) OnConnect(ctx *via.Ctx) error {
	ctx.Session().Put(member{Name: "carol"})
	return nil
}
func (o *onConnectLoginer) Bump(ctx *via.Ctx) { o.n.Set(o.n.Get() + 1) } // action 0
func (o *onConnectLoginer) View() h.H {
	return h.Div(o.n.Display(), h.Button(via.On("click", o.Bump)))
}

// The README-recommended "establish the session in OnConnect" pattern must
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

	req := liveActionRequest(t, srv, string(page), tab, 0, 0) // Bump, no cookie
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"an OnConnect-minted session must bind the connection exactly like a live-action login")
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

	loginReq := liveActionRequest(t, srv, string(page), tabA, 0, 1) // Login on A
	loginResp, err := http.DefaultClient.Do(loginReq)
	require.NoError(t, err)
	loginResp.Body.Close()

	bumpA := liveActionRequest(t, srv, string(page), tabA, 0, 0)
	bumpAResp, err := http.DefaultClient.Do(bumpA)
	require.NoError(t, err)
	bumpAResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, bumpAResp.StatusCode, "the logged-in tab rejects a cookieless dispatch")

	bumpB := liveActionRequest(t, srv, string(page), tabB, 0, 0)
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

	loginReq := liveActionRequest(t, srv, string(page), tab, 0, 1) // Login
	loginResp, err := owner.Do(loginReq)
	require.NoError(t, err)
	loginResp.Body.Close()
	before := cookieValue(t, owner, srv.URL, "via_session")
	require.NotEmpty(t, before)

	rotReq := liveActionRequest(t, srv, string(page), tab, 0, 2) // Rotate
	rotResp, err := owner.Do(rotReq)
	require.NoError(t, err)
	rotResp.Body.Close()
	after := cookieValue(t, owner, srv.URL, "via_session")
	require.NotEqual(t, before, after, "Rotate must mint a fresh id")

	okReq := liveActionRequest(t, srv, string(page), tab, 0, 0) // Bump, new cookie
	okResp, err := owner.Do(okReq)
	require.NoError(t, err)
	okResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, okResp.StatusCode,
		"the post-rotate dispatch with the new cookie must still be bound")

	staleReq := liveActionRequest(t, srv, string(page), tab, 0, 0)
	staleReq.AddCookie(&http.Cookie{Name: "via_session", Value: before})
	staleResp, err := http.DefaultClient.Do(staleReq)
	require.NoError(t, err)
	defer staleResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, staleResp.StatusCode,
		"the pre-rotate id must not drive the connection anymore")
}

// raceLoginer is liveLoginer's Login, but pausable: it blocks on the island
// goroutine until proceed is signaled, closing started the instant it takes
// hold of that goroutine — the two channels let a test park a concurrent
// cookieless dispatch's own goroutine right at the moment the connection is
// still unbound, then release the login and observe which check ran first.
type raceLoginer struct {
	n       via.State[int]
	started chan struct{}
	proceed chan struct{}
}

func (p *raceLoginer) OnConnect(*via.Ctx) error { return nil }
func (p *raceLoginer) Bump(ctx *via.Ctx)        { p.n.Set(p.n.Get() + 1) } // action 0
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

	loginReq := liveActionRequest(t, srv, string(page), tab, 0, 1) // Login
	loginDone := make(chan *http.Response, 1)
	go func() {
		resp, err := http.DefaultClient.Do(loginReq)
		require.NoError(t, err)
		loginDone <- resp
	}()
	<-root.started // Login now holds the island goroutine, unbound so far

	bumpReq := liveActionRequest(t, srv, string(page), tab, 0, 0) // Bump, no cookie
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
