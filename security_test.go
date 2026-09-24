package via_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sseStatus opens the SSE stream (POST /_via/sse) with exactly the given headers
// and returns the status, cancelling immediately so a 200 stream's child
// goroutine tears down.
func sseStatus(t *testing.T, srv *httptest.Server, headers map[string]string) int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestSSE_enforcementRejectsCrossSiteOrigin(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}, via.WithTrustedOrigin("https://childder.example")))
	assert.Equal(t, http.StatusForbidden,
		sseStatus(t, srv, map[string]string{"Sec-Fetch-Site": "cross-site"}))
}

func TestSSE_enforcementFailsClosedWithoutAnyOriginSignal(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}, via.WithTrustedOrigin("https://childder.example")))
	assert.Equal(t, http.StatusForbidden, sseStatus(t, srv, nil))
}

func TestSSE_enforcementAllowsSameOrigin(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}, via.WithTrustedOrigin("https://childder.example")))
	assert.Equal(t, http.StatusOK,
		sseStatus(t, srv, map[string]string{"Sec-Fetch-Site": "same-origin"}))
}

func TestSSE_defaultAllowsCrossSite(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}))
	assert.Equal(t, http.StatusOK,
		sseStatus(t, srv, map[string]string{"Sec-Fetch-Site": "cross-site"}))
}

func TestSSE_allowsTrustedCrossOrigin(t *testing.T) {
	t.Parallel()
	const childder = "https://childder.example"
	srv := serve(t, via.Handler(quietChild{}, via.WithTrustedOrigin(childder)))
	assert.Equal(t, http.StatusOK,
		sseStatus(t, srv, map[string]string{"Origin": childder, "Sec-Fetch-Site": "cross-site"}))
}

func TestSSE_overTheConnectionCapIsRefused(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}, via.WithMaxSSEConn(1)))

	_, release := openStream(t, srv) // takes the only slot; asserts it connected (200)
	defer release()

	assert.Equal(t, http.StatusServiceUnavailable, sseStatus(t, srv, sameOrigin()),
		"a connect past the cap must be refused with 503")
}

func TestSSE_disconnectFreesACapSlot(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}, via.WithMaxSSEConn(1)))

	_, release := openStream(t, srv)
	require.Equal(t, http.StatusServiceUnavailable, sseStatus(t, srv, sameOrigin()),
		"precondition: the single slot is taken")

	release() // disconnect frees the slot (the server observes it asynchronously)

	require.Eventually(t, func() bool {
		return sseStatus(t, srv, sameOrigin()) == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond, "a freed slot must admit a new connection")
}

func TestSSE_capIsPerRegister(t *testing.T) {
	t.Parallel()
	a := serve(t, via.Handler(quietChild{}, via.WithMaxSSEConn(1)))
	b := serve(t, via.Handler(quietChild{}, via.WithMaxSSEConn(1)))

	_, release := openStream(t, a) // fills A's only slot
	defer release()

	assert.Equal(t, http.StatusOK, sseStatus(t, b, sameOrigin()),
		"a second Handler must have an independent cap")
}

// panicComp's action panics, to prove a buggy handler returns 500 and does not
// crash the server.
type panicComp struct{}

func (p *panicComp) Boom(*via.Ctx) { panic("boom") }
func (p *panicComp) View() h.H {
	return h.Div(h.Button(via.On("click", p.Boom), h.Str("x")))
}

func TestAction_enforcementRejectsCrossSiteOriginAndDoesNotMutate(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin("https://childder.example")))

	status, _ := app.Action(1).SecFetch("cross-site").Fire()
	assert.Equal(t, http.StatusForbidden, status)

	_, body := app.Get("/")
	assert.Contains(t, body, "<h1>0</h1>", "cross-site POST must not have mutated the store")
}

func TestAction_enforcementRejectsSameSiteOrigin(t *testing.T) {
	t.Parallel()
	status, _ := vt.Serve(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin("https://childder.example"))).Action(1).SecFetch("same-site").Fire()
	assert.Equal(t, http.StatusForbidden, status)
}

func TestAction_enforcementFailsClosedWithoutAnyOriginSignal(t *testing.T) {
	t.Parallel()
	status, _ := vt.Serve(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin("https://childder.example"))).Action(1).NoOrigin().Fire()
	assert.Equal(t, http.StatusForbidden, status)
}

type csrfForm struct{ hits *atomic.Int64 }

func (p *csrfForm) Save(ctx *via.Ctx) { p.hits.Add(1) }
func (p *csrfForm) View() h.H         { return via.PostForm(p.Save, h.Button(h.Str("save"))) }

func TestAction_plainFormWithoutTrustedOriginAdmitsOnlySameOrigin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		headers map[string]string // "{self}" stands for the test server's origin
		want    int
	}{
		{"cross-site fetch metadata", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, http.StatusForbidden},
		{"foreign Origin", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"opaque Origin", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"foreign Referer", map[string]string{"Referer": "https://evil.example/page"}, http.StatusForbidden},
		{"no origin signal", nil, http.StatusForbidden},
		{"same-origin fetch metadata", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"matching Origin", map[string]string{"Origin": "{self}"}, http.StatusOK},
		{"matching Referer", map[string]string{"Referer": "{self}/"}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hits := &atomic.Int64{}
			srv := serve(t, via.Handler(csrfForm{hits: hits}))
			_, page := do(t, srv, http.MethodGet, "/", "")
			body, ctype := multipartForm(t, nil)
			req, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, "r", 0), body)
			require.NoError(t, err)
			req.Header.Set("Content-Type", ctype)
			for k, v := range tt.headers {
				req.Header.Set(k, strings.ReplaceAll(v, "{self}", srv.URL))
			}
			resp, err := srv.Client().Do(req)
			require.NoError(t, err)
			resp.Body.Close()

			assert.Equal(t, tt.want, resp.StatusCode)
			wantHits := int64(0)
			if tt.want == http.StatusOK {
				wantHits = 1
			}
			assert.Equal(t, wantHits, hits.Load(), "a rejected submit must not run the handler")
		})
	}
}

func TestAction_allowsSameOriginViaMatchingOriginHeader(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(counter{count: &store{}}))
	status, body := app.Action(1).Origin(app.URL()).Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

func TestAction_rejectsOversizeBody(t *testing.T) {
	t.Parallel()
	big := `{"f0":"` + strings.Repeat("a", 2<<20) + `"}`
	status, _ := vt.Serve(t, via.Handler(counter{count: &store{}})).Action(1).Body(big).Fire()
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
}

func TestAction_rejectsMalformedBody(t *testing.T) {
	t.Parallel()
	status, _ := vt.Serve(t, via.Handler(counter{count: &store{}})).Action(1).Body("{not valid json").Fire()
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestAction_treatsEmptyBodyAsNoSignals(t *testing.T) {
	t.Parallel()
	status, body := vt.Serve(t, via.Handler(counter{count: &store{}})).Action(1).Body("").Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

func TestAction_panicIsRecoveredAs500AndServerStaysUp(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(panicComp{}))

	status, _ := app.Action(0).Fire()
	assert.Equal(t, http.StatusInternalServerError, status)

	status2, _ := app.Get("/")
	assert.Equal(t, http.StatusOK, status2, "server must keep serving after a panicking action")
}

func TestAction_defaultRejectsCrossSitePlainAction(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(counter{count: &store{}}))
	status, _ := app.Action(1).SecFetch("cross-site").Fire()
	assert.Equal(t, http.StatusForbidden, status)

	_, body := app.Get("/")
	assert.Contains(t, body, "<h1>0</h1>", "cross-site POST must not have mutated the store")
}

func TestAction_defaultRejectsPlainActionWithoutAnyOriginSignal(t *testing.T) {
	t.Parallel()
	status, _ := vt.Serve(t, via.Handler(counter{count: &store{}})).Action(1).NoOrigin().Fire()
	assert.Equal(t, http.StatusForbidden, status, "a plain action has no tab id, so an unprovable source fails closed")
}

func TestWithTrustedOrigin_allowsNamedCrossOrigin(t *testing.T) {
	t.Parallel()
	const trusted = "https://trusted.example"
	app := vt.Serve(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin(trusted)))
	status, body := app.Action(1).Origin(trusted).SecFetch("cross-site").Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

func TestOriginFloor_matchesHostCaseAndDefaultPortInsensitively(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, host, origin string }{
		{"case-insensitive host", "app.example", "http://APP.Example"},
		{"explicit http default port", "app.example", "http://app.example:80"},
		{"explicit https default port", "app.example", "https://app.example:443"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			app := vt.Serve(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin("https://childder.example")))
			status, body := app.Action(1).Host(c.host).Origin(c.origin).Fire()
			assert.Equal(t, http.StatusOK, status, "a normalized same-origin request must be allowed")
			assert.Contains(t, body, "<h1>1</h1>", "and must mutate server state")
		})
	}
}

func TestOriginFloor_rejectsDifferentHostOrPort(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, host, origin string }{
		{"different host", "app.example", "http://evil.example"},
		{"different port", "app.example:8080", "http://app.example:9090"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			app := vt.Serve(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin("https://childder.example")))
			status, _ := app.Action(1).Host(c.host).Origin(c.origin).Fire()
			assert.Equal(t, http.StatusForbidden, status)
		})
	}
}

func TestOriginFloor_enforcesSchemeOnTLSRequests(t *testing.T) {
	t.Parallel()
	app := vt.ServeTLS(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin("https://childder.example")))

	down, _ := app.Action(1).Host("app.example").Origin("http://app.example").Fire()
	assert.Equal(t, http.StatusForbidden, down, "http Origin on a TLS request is a scheme downgrade")

	ok, body := app.Action(1).Host("app.example").Origin("https://app.example").Fire()
	assert.Equal(t, http.StatusOK, ok, "https Origin on a TLS request to the same host matches")
	assert.Contains(t, body, "<h1>1</h1>")
}

// liveSessStream is a live child (its State makes the root a live unit) that
// establishes its session in OnInit, so the connect response carries the cookie.
type liveSessStream struct{ n via.State[int] }

func (c *liveSessStream) OnInit(ctx *via.Ctx) error {
	ctx.Session().Put(member{Name: "bob"})
	return nil
}
func (c *liveSessStream) View() h.H { return h.Div(h.Str("live"), c.n.Display()) }

// outageStore is a real store that can be switched to failing Load, so a test
// can open a stream while the backend is healthy and then blip it.
type outageStore struct {
	via.SessionStore
	mu   sync.Mutex
	down bool
}

func (s *outageStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	down := s.down
	s.mu.Unlock()
	if down {
		return nil, false, errors.New("session store down")
	}
	return s.SessionStore.Load(ctx, id)
}

func TestSSE_refusesToConnectWhileTheSessionStoreIsDown(t *testing.T) {
	t.Parallel()
	store := &outageStore{SessionStore: via.NewMemorySessionStore()}
	srv := serve(t, via.Handler(liveSessStream{},
		via.WithSessionStore(store),
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))

	ck := sseSessionCookie(t, srv)
	require.NotNil(t, ck, "precondition: a healthy connect establishes the session cookie")

	store.mu.Lock()
	store.down = true
	store.mu.Unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(ck)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
		"a connect during a session-store outage must be refused, not admitted unbound")
	assert.Contains(t, string(body), "session store unavailable")
	assert.NotContains(t, resp.Header.Get("Content-Type"), "text/event-stream",
		"no stream may be opened when the session behind it could not be resolved")
	assert.NotContains(t, string(body), "viatab",
		"a refused connect must not hand out a tab id")
}

// sseSessionCookie opens one healthy stream and returns the session cookie its
// OnInit established, cancelling the request.
func sseSessionCookie(t *testing.T, srv *httptest.Server) *http.Cookie {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	for _, ck := range resp.Cookies() {
		if ck.Name == "via_session" {
			return ck
		}
	}
	return nil
}

// unsafeRoot and unsafeChild both bump a visible counter alongside an
// unsafe Redirect, so a rejected redirect's fallback response is provably a
// normal patch (the counter's new value), not a crash or a hang.
type unsafeRoot struct{ n int }

func (u *unsafeRoot) Go(ctx *via.Ctx) { u.n++; ctx.Redirect("javascript:alert(1)") }

func (u *unsafeRoot) View() h.H { return h.Div(h.Str(u.n), h.Button(via.On("click", u.Go))) }

type unsafeChild struct{ n int }

func (u *unsafeChild) Go(ctx *via.Ctx) { u.n++; ctx.Redirect("javascript:alert(1)") }

func (u *unsafeChild) View() h.H { return h.Div(h.Str(u.n), h.Button(via.On("click", u.Go))) }

type unsafeParent struct{ I unsafeChild }

func (p *unsafeParent) View() h.H { return h.Div(via.Child(p.I)) }

func TestDispatch_unsafeRedirectFallsBackEverywhere(t *testing.T) {
	t.Parallel()

	t.Run("plain root", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Handler(unsafeRoot{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "r", 0), "{}")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript")
		assert.Contains(t, body, ">1<", "the mutation must still land in the fallback patch")
	})

	t.Run("plain child", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Handler(unsafeParent{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp, body := do(t, srv, http.MethodPost, actionURL(t, page, "0", 0), "{}")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotContains(t, resp.Header.Get("Content-Type"), "text/javascript")
		assert.Contains(t, body, ">1<", "the mutation must still land in the fallback patch")
	})

	t.Run("native form", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Handler(loginForm{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		resp := postForm(&http.Client{CheckRedirect: noFollow}, t, srv.URL+actionURL(t, page, "r", 0), "name", "evil")
		assert.NotEqual(t, http.StatusSeeOther, resp.StatusCode, "an unsafe redirect must not 303")
		assert.Empty(t, resp.Header.Get("Location"))
	})
}

func TestDispatch_forgedActionIDIsGone(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, via.Handler(counter{count: &store{}}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		url := actionURL(t, page, "r", 0)
		for _, n := range []string{"99", "-1", "________"} {
			resp, _ := do(t, srv, http.MethodPost, swapActionID(t, url, n), "{}")
			assert.Equal(t, http.StatusGone, resp.StatusCode, "forged id=%s must 410, not panic/misroute", n)
		}
	})

	t.Run("live", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			app := vt.Serve(t, via.Handler(liveClicker{}))
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

// toggleState is branchedView's shared, per-connection-independent memory: a
// bool that flips which actions View renders, plus which handler last ran, so
// a test can observe whether a click actually reached a handler.
type toggleState struct {
	mu     sync.Mutex
	locked bool
	ran    string
}

func (s *toggleState) flip() { s.mu.Lock(); s.locked = !s.locked; s.mu.Unlock() }

func (s *toggleState) mark(name string) { s.mu.Lock(); s.ran = name; s.mu.Unlock() }

func (s *toggleState) snapshot() (locked bool, ran string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locked, s.ran
}

// branchedView's action set depends on server state: unlocked renders
// Save=0, Delete=1, Flip=2; locked removes Save, so every later index shifts
// down — Delete=0, Flip=1 — the render shape the audit flagged as a live
// misroute hazard (a branched View shifting every following index),
// reproduced here on the plain path.
type branchedView struct{ st *toggleState }

func (b *branchedView) Save(ctx *via.Ctx) { b.st.mark("save") }

func (b *branchedView) Delete(ctx *via.Ctx) { b.st.mark("delete") }

func (b *branchedView) Flip(ctx *via.Ctx) { b.st.flip() }

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

// xmChild is a live, dep-free child mountable at any path — the vehicle for
// proving a live tab from one mount can't drive another mount's action table.
type xmChild struct {
	fired *int
	n     via.State[int]
}

func (x *xmChild) Fire(*via.Ctx) { *x.fired++ }

func (x *xmChild) View() h.H {
	return h.Div(x.n.Display(), h.Button(via.On("click", x.Fire)))
}

func TestDispatch_liveActionCannotCrossMounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var aFired, cFired int
		r := via.NewRouter()
		via.Mount(r, "/a", xmChild{fired: &aFired})
		via.Mount(r, "/c", xmChild{fired: &cFired})
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
	srv := serve(t, via.Handler(branchedView{st: &toggleState{}}))

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

// tabGuard is a live root with one ordinary @post action, so a dispatch either
// reaches this connection's instance (bumping hits) or does not.
type tabGuard struct{ hits *int }

func (g *tabGuard) OnInit(ctx *via.Ctx) error { ctx.Tick(time.Hour, g.tick); return nil }

func (g *tabGuard) tick(ctx *via.Ctx) {}

func (g *tabGuard) Bump(ctx *via.Ctx) { *g.hits++ }

func (g *tabGuard) View() h.H { return h.Div(h.Button(via.On("click", g.Bump))) }

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
			app := vt.Serve(t, via.Handler(tabGuard{hits: &calls}))
			conn := app.Connect()
			defer conn.Close()

			status, _ := app.Action(0).Body(tc.body).Fire()
			assert.Equal(t, http.StatusGone, status,
				"a live action with no valid tab id must 410, not run against a throwaway instance")
			assert.Zero(t, calls, "and the connection's unit must not have been touched")
		})
	}
}

func TestDispatch_ignoresTheXViaTabHeader(t *testing.T) {
	t.Parallel()
	calls := 0
	app := vt.Serve(t, via.Handler(tabGuard{hits: &calls}))
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

func TestDispatch_tabSignalIsIgnoredWithoutTheDatastarRequestHeader(t *testing.T) {
	t.Parallel()
	calls := 0
	app := vt.Serve(t, via.Handler(tabGuard{hits: &calls}))
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

// liveBoundRoot is tabGuard with a Bind()ed slot, so a POST carrying that
// signal takes a second discovery pass — the pass whose bind Ctx never ran the
// root's OnInit and so used to read live=false.
type liveBoundRoot struct {
	Q    via.Signal[string]
	hits *int
}

func (g *liveBoundRoot) OnInit(ctx *via.Ctx) error { ctx.Tick(time.Hour, g.tick); return nil }

func (g *liveBoundRoot) tick(ctx *via.Ctx) {}

func (g *liveBoundRoot) Bump(ctx *via.Ctx) { *g.hits++ }

func (g *liveBoundRoot) View() h.H {
	return h.Div(h.Input(g.Q.Bind()), h.Button(via.On("click", g.Bump)))
}

func TestDispatch_staleTabOnALiveRootFailsClosedOnALaterHydratePass(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{}`, `{"q":"x"}`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			calls := 0
			app := vt.Serve(t, via.Handler(liveBoundRoot{hits: &calls}))
			conn := app.Connect()
			defer conn.Close()

			status, _ := app.Action(0).Body(body).Fire()
			assert.Equal(t, http.StatusGone, status,
				"liveness is decided by the auth render; a posted signal that adds a discovery pass must not bypass it")
			assert.Zero(t, calls, "and the live unit must not have been touched")
		})
	}
}

func TestWithMaxBody_refusesAnActionBodyOverTheCap(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithMaxBody(1024))
	via.Mount(r, "/p", counter{count: &store{}})
	srv := serve(t, r)

	// Valid JSON, one byte over: an unparseable body would answer 400 before
	// the reader ever passed the cap.
	body := `{"a":"` + strings.Repeat("y", 1025-8) + `"}`
	require.Len(t, body, 1025)
	resp, _ := do(t, srv, http.MethodPost, "/p/_via/a/r/0", body)

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestWithMaxUpload_refusesAMultipartBodyOverTheCap(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithMaxUpload(4096))
	via.Mount(r, "/p", avatarPage{cap: &capture{}})
	srv := serve(t, r)

	resp := uploadPOST(&http.Client{CheckRedirect: noFollow}, t,
		srv.URL+"/p/_via/a/r/0", "big.bin", strings.Repeat("x", 4096))

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestWithMaxBody_panicsOnANonPositiveCap(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { via.NewRouter(via.WithMaxBody(0)) })
}

func TestWithMaxUpload_panicsOnANonPositiveCap(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { via.NewRouter(via.WithMaxUpload(-1)) })
}
