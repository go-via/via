package via_test

import (
	"context"
	"errors"
	"io"
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

// With WithTrustedOrigin set, origin enforcement is on: the SSE connect opens a
// long-lived server resource (an stream goroutine + timers), so like the action
// POST it must fail closed to any request that can't prove a same-origin (or
// allowlisted) source.
func TestSSE_enforcementRejectsCrossSiteOrigin(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}, via.WithTrustedOrigin("https://childder.example")))
	assert.Equal(t, http.StatusForbidden,
		sseStatus(t, srv, map[string]string{"Sec-Fetch-Site": "cross-site"}))
}

// Under enforcement, a connect that carries no origin signal at all (no
// Sec-Fetch-Site, no Origin) proves nothing about its source and must fail
// closed, exactly as the action endpoint does.
func TestSSE_enforcementFailsClosedWithoutAnyOriginSignal(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}, via.WithTrustedOrigin("https://childder.example")))
	assert.Equal(t, http.StatusForbidden, sseStatus(t, srv, nil))
}

// Under enforcement, a real same-origin browser SSE fetch (Sec-Fetch-Site:
// same-origin) must still connect.
func TestSSE_enforcementAllowsSameOrigin(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}, via.WithTrustedOrigin("https://childder.example")))
	assert.Equal(t, http.StatusOK,
		sseStatus(t, srv, map[string]string{"Sec-Fetch-Site": "same-origin"}))
}

// Without WithTrustedOrigin the endpoint is open by default (dev-friendly; the
// per-tab id is the CSRF token): a cross-site connect must succeed.
func TestSSE_defaultAllowsCrossSite(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}))
	assert.Equal(t, http.StatusOK,
		sseStatus(t, srv, map[string]string{"Sec-Fetch-Site": "cross-site"}))
}

// A known cross-origin childder allowlisted with WithTrustedOrigin must still be
// able to open the stream, so the floor doesn't break a deliberate child.
func TestSSE_allowsTrustedCrossOrigin(t *testing.T) {
	t.Parallel()
	const childder = "https://childder.example"
	srv := serve(t, via.Handler(quietChild{}, via.WithTrustedOrigin(childder)))
	assert.Equal(t, http.StatusOK,
		sseStatus(t, srv, map[string]string{"Origin": childder, "Sec-Fetch-Site": "cross-site"}))
}

// Each stream holds an stream goroutine + timers; left uncapped, a client
// can open them without bound and exhaust the server. A connect past the cap
// must be refused (503) rather than admitted, so the resource ceiling holds.
func TestSSE_overTheConnectionCapIsRefused(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(quietChild{}, via.WithMaxSSEConn(1)))

	_, release := openStream(t, srv) // takes the only slot; asserts it connected (200)
	defer release()

	assert.Equal(t, http.StatusServiceUnavailable, sseStatus(t, srv, sameOrigin()),
		"a connect past the cap must be refused with 503")
}

// The cap counts CONCURRENT streams, not lifetime connects: when a stream closes
// its slot must free so a later client can connect. A cap that never decremented
// would wedge the app at its limit forever.
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

// The cap is per-Handler: two independently registered apps in one process must
// not share a counter, or a busy app would throttle an unrelated one.
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

// POST /_via/a/{n} is a state-changing endpoint; under origin enforcement
// (WithTrustedOrigin set) a cross-site request must be rejected and must not
// mutate server state, or any page on the web can drive the counter (CSRF).
func TestAction_enforcementRejectsCrossSiteOriginAndDoesNotMutate(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin("https://childder.example")))

	status, _ := app.Action(1).SecFetch("cross-site").Fire()
	assert.Equal(t, http.StatusForbidden, status)

	_, body := app.Get("/")
	assert.Contains(t, body, "<h1>0</h1>", "cross-site POST must not have mutated the store")
}

// same-site (a sibling subdomain) is NOT same-origin; treating it as trusted is
// the classic CSRF confusion, so under enforcement a same-site action request
// must be rejected.
func TestAction_enforcementRejectsSameSiteOrigin(t *testing.T) {
	t.Parallel()
	status, _ := vt.Serve(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin("https://childder.example"))).Action(1).SecFetch("same-site").Fire()
	assert.Equal(t, http.StatusForbidden, status)
}

// Under enforcement, a request that carries no origin signal at all (no
// Sec-Fetch-Site, no Origin) proves nothing about where it came from; the
// floor must fail closed rather than silently trust it.
func TestAction_enforcementFailsClosedWithoutAnyOriginSignal(t *testing.T) {
	t.Parallel()
	status, _ := vt.Serve(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin("https://childder.example"))).Action(1).NoOrigin().Fire()
	assert.Equal(t, http.StatusForbidden, status)
}

// A legitimate same-origin browser fetch (proven via a matching Origin header
// when Sec-Fetch-Site is absent) must be allowed and must mutate state.
func TestAction_allowsSameOriginViaMatchingOriginHeader(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(counter{count: &store{}}))
	status, body := app.Action(1).Origin(app.URL()).Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

// An unbounded request body is a memory-exhaustion DoS; the handler must cap it
// and reject an oversize body with 413 rather than buffering it whole.
func TestAction_rejectsOversizeBody(t *testing.T) {
	t.Parallel()
	big := `{"f0":"` + strings.Repeat("a", 2<<20) + `"}`
	status, _ := vt.Serve(t, via.Handler(counter{count: &store{}})).Action(1).Body(big).Fire()
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
}

// A malformed (non-empty, invalid-JSON) body must fail loudly with 400, not
// silently bind an empty signal set and misroute the action.
func TestAction_rejectsMalformedBody(t *testing.T) {
	t.Parallel()
	status, _ := vt.Serve(t, via.Handler(counter{count: &store{}})).Action(1).Body("{not valid json").Fire()
	assert.Equal(t, http.StatusBadRequest, status)
}

// An empty body is the common plain-action case and must be treated as "no
// signals", not as a malformed-body 400.
func TestAction_treatsEmptyBodyAsNoSignals(t *testing.T) {
	t.Parallel()
	status, body := vt.Serve(t, via.Handler(counter{count: &store{}})).Action(1).Body("").Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

// A panicking action must be contained: the request returns 500 and the server
// keeps serving subsequent requests (no crash, no wedged connection).
func TestAction_panicIsRecoveredAs500AndServerStaysUp(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(panicComp{}))

	status, _ := app.Action(0).Fire()
	assert.Equal(t, http.StatusInternalServerError, status)

	status2, _ := app.Get("/")
	assert.Equal(t, http.StatusOK, status2, "server must keep serving after a panicking action")
}

// Without WithTrustedOrigin the action endpoint is open by default, so
// non-browser/dev clients that cannot send origin headers just work.
func TestAction_defaultAllowsCrossSite(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(counter{count: &store{}}))
	status, body := app.Action(1).SecFetch("cross-site").Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

// The default-open floor must also admit a request with NO origin signal at all
// (no Origin, no Sec-Fetch-Site) — the plain curl / non-browser client case
// that motivates the dev-friendly default.
func TestAction_defaultAllowsRequestWithoutAnyOriginSignal(t *testing.T) {
	t.Parallel()
	status, body := vt.Serve(t, via.Handler(counter{count: &store{}})).Action(1).NoOrigin().Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

// WithTrustedOrigin allowlists a specific cross-origin childder; that origin
// must be allowed even when the browser labels the request cross-site.
func TestWithTrustedOrigin_allowsNamedCrossOrigin(t *testing.T) {
	t.Parallel()
	const trusted = "https://trusted.example"
	app := vt.Serve(t, via.Handler(counter{count: &store{}}, via.WithTrustedOrigin(trusted)))
	status, body := app.Action(1).Origin(trusted).SecFetch("cross-site").Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<h1>1</h1>")
}

// When Sec-Fetch-Site is absent the floor falls back to matching the Origin
// host against the request Host. That match is case-insensitive and treats an
// explicit default port (:80 for http, :443 for https) as equivalent to none —
// browsers vary in whether they include it, and a mismatch there would reject
// legitimate same-origin requests. These normalization branches need a
// controlled request Host, which vt supplies.
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

// A different host, or the same host on a different (non-default) port, is a
// distinct origin and must be rejected — the floor is the CSRF boundary.
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

// On a request that arrived over TLS, an http Origin is a scheme downgrade and
// must be rejected even though the host matches — scheme is part of a web
// origin. A matching https Origin on the same host is allowed. (This only bites
// the Sec-Fetch-Site-absent fallback; modern browsers never send an http Origin
// to their own https document.)
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

// A connect resolves the session BEFORE OnInit so the stream is bound to the
// identity the browser held when it opened. If the store cannot answer, that
// resolve must fail the connect: admitting the stream would leave it forever
// unbound — no later live action binds it, since one sees a session that
// already existed — and the tab id would be a bearer credential good from any
// request for the connection's whole life.
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

// branchedView's action SET depends on server state: unlocked renders
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

// The tab id rides as a signal, never as a header. Honouring a header too
// would keep a channel the browser can be made to attach cross-origin under
// some configurations, alongside the synchronizer token the signal already is.
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

// And the belt-and-braces half: Datastar-Request still gates the JSON path, so
// a form-shaped cross-origin POST cannot be dressed up as a signal action.
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
// signal takes a SECOND discovery pass — the pass whose bind Ctx never ran the
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
