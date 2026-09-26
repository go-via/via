package via_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
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

func TestLiveAction_unknownTabIsGone(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(clicker{}))
	t.Cleanup(srv.Close)

	_, page := do(t, srv, http.MethodGet, "/", "")
	url := actionURL(t, page, "r", 0)

	resp, _ := post(t, srv, url, withTab("nonexistent", "{}"), map[string]string{
		"Sec-Fetch-Site": "same-origin",
	})
	assert.Equal(t, http.StatusGone, resp.StatusCode)
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
		// A caller can end the stream from the server side without cancelling
		// ctx, so a scan error here isn't necessarily a bug — log it so a
		// spurious "frame never arrived" failure carries the real reason.
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			t.Logf("sse scan ended: %v", err)
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

// liveActionRequest builds a raw dispatch POST against child/n using the
// page's currently-rendered action URL, with tab as its viatab signal —
// bypassing any cookie jar, so the caller controls exactly what (if any)
// session cookie rides along.
func liveActionRequest(t *testing.T, srv *httptest.Server, page, tab, child string, n int) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, page, child, n), strings.NewReader(withTab(tab, "{}")))
	require.NoError(t, err)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return req
}

func TestDispatch_liveActionUnderASessionRejectsAMismatchedSession(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/login", loginComp{})  // plain — establishes the session cookie
	via.Mount(r, "/live", sessionLive{}) // Live root, shares the router-wide session manager
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
	// the check rejects a mismatch, not the connection itself.
	ownReq := liveActionRequest(t, srv, string(page), tab, "r", 0)
	ownResp, err := owner.Do(ownReq)
	require.NoError(t, err)
	defer ownResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, ownResp.StatusCode,
		"the connecting session's own dispatch must still succeed")
}

func TestDispatch_liveActionOnAnAnonymousConnectionIsUnaffected(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(sessionLive{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
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

// signIn establishes a session on c through the plain mount at path, whose
// first action writes one.
func signIn(t *testing.T, srv *httptest.Server, c *http.Client, path string) {
	t.Helper()
	loginResp, err := c.Get(srv.URL + path)
	require.NoError(t, err)
	loginPage, err := io.ReadAll(loginResp.Body)
	require.NoError(t, err)
	loginResp.Body.Close()
	req, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, string(loginPage), "r", 0), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := c.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.NotEmpty(t, cookieValue(t, c, srv.URL, "via_session"), "precondition: signing in must establish a session")
}

type namedLogin struct{ name string }

func (l *namedLogin) SignIn(ctx *via.Ctx) { ctx.Session().Put(member{Name: l.name}) }
func (l *namedLogin) View() h.H           { return h.Button(via.On("click", l.SignIn), h.Str("in")) }

// whoLive publishes, over the stream, whose session its action ran under.
type whoLive struct{ who via.State[string] }

func (w *whoLive) Whoami(ctx *via.Ctx) {
	w.who.Set("nobody")
	if m, ok := ctx.Session().Get[member](); ok {
		w.who.Set(m.Name)
	}
}
func (w *whoLive) View() h.H {
	return h.Div(h.P(h.Str("who: "), w.who.Display()), h.Button(via.On("click", w.Whoami)))
}

func TestDispatch_aLeakedTabIDBoundByAnAttackerGrantsNoVictimSession(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/as-victim", namedLogin{name: "vicky"})
	via.Mount(r, "/as-attacker", namedLogin{name: "mallory"})
	via.Mount(r, "/live", whoLive{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	lines, cancel := openStreamAt(t, srv, "/live/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)
	getResp, err := http.DefaultClient.Get(srv.URL + "/live")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()
	victim, attacker := jarClient(t), jarClient(t)
	signIn(t, srv, victim, "/as-victim")
	signIn(t, srv, attacker, "/as-attacker")

	atkResp, err := attacker.Do(liveActionRequest(t, srv, string(page), tab, "r", 0))
	require.NoError(t, err)
	atkBody, _ := io.ReadAll(atkResp.Body)
	atkResp.Body.Close()
	require.Equal(t, http.StatusNoContent, atkResp.StatusCode, "the leaked id still drives the anonymous tab")
	assert.NotContains(t, string(atkBody), "vicky")

	vicResp, err := victim.Do(liveActionRequest(t, srv, string(page), tab, "r", 0))
	require.NoError(t, err)
	vicResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, vicResp.StatusCode, "the first session to reach the tab owns it")

	deadline := time.After(2 * time.Second)
	for seen := false; !seen; {
		select {
		case <-deadline:
			require.Fail(t, "the attacker's action never reached the stream")
		case line := <-lines:
			assert.NotContains(t, line, "vicky", "an action ran under the victim's session")
			seen = strings.Contains(line, "mallory")
		}
	}
}

func TestDispatch_liveActionCarryingASessionBindsAnAnonymousConnection(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/login", loginComp{})
	via.Mount(r, "/live", sessionLive{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// The tab connects anonymously; the user then logs in from another tab.
	lines, cancel := openStreamAt(t, srv, "/live/_via/sse")
	defer cancel()
	tab := awaitTabID(t, lines)
	getResp, err := http.DefaultClient.Get(srv.URL + "/live")
	require.NoError(t, err)
	page, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	getResp.Body.Close()
	owner := jarClient(t)
	signIn(t, srv, owner, "/login")

	ownResp, err := owner.Do(liveActionRequest(t, srv, string(page), tab, "r", 1)) // Peek, owner's cookie
	require.NoError(t, err)
	ownResp.Body.Close()
	require.Equal(t, http.StatusNoContent, ownResp.StatusCode)

	bareResp, err := http.DefaultClient.Do(liveActionRequest(t, srv, string(page), tab, "r", 0)) // Bump, no cookie
	require.NoError(t, err)
	bareResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, bareResp.StatusCode,
		"once a session reached the tab, its id must stop being a bearer credential")

	againResp, err := owner.Do(liveActionRequest(t, srv, string(page), tab, "r", 0))
	require.NoError(t, err)
	againResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, againResp.StatusCode, "the binding session still dispatches")
}

func TestDispatch_liveReadOnlySessionTouchByTheOwnerDoesNotBind(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(sessionLive{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
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

func (p *liveLoginer) Bump(ctx *via.Ctx) { p.n.Set(p.n.Get() + 1) } // action 0

func (p *liveLoginer) Login(ctx *via.Ctx) { ctx.Session().Put(member{Name: "bob"}) } // action 1

func (p *liveLoginer) Rotate(ctx *via.Ctx) { ctx.Session().Rotate() } // action 2

func (p *liveLoginer) View() h.H {
	return h.Div(p.n.Display(),
		h.Button(via.On("click", p.Bump)),
		h.Button(via.On("click", p.Login)),
		h.Button(via.On("click", p.Rotate)))
}

func TestDispatch_liveActionLoginBindsTheConnectionAgainstALaterCookielessDispatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(liveLoginer{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
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

func TestDispatch_onConnectMintedSessionBindsTheConnection(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(onConnectLoginer{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
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

func TestDispatch_liveLoginOnOneTabDoesNotBindASiblingTab(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(liveLoginer{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
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

func TestDispatch_rotateAfterALiveLoginKeepsTheBindingOnTheNewID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(liveLoginer{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
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

// raceLoginer is liveLoginer's Login, but pausable: it blocks on the child
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

func TestDispatch_cookielessDispatchRacingAConcurrentLoginIsRejectedNotAppliedStale(t *testing.T) {
	t.Parallel()
	root := raceLoginer{started: make(chan struct{}), proceed: make(chan struct{})}
	srv := httptest.NewServer(via.Handler(root, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
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
	// while the connection is still unbound — the exact window I3 closes.
	// No deterministic handshake for this checkpoint is exposed through via's
	// public surface, and adding one would mean touching non-test files, which
	// is out of scope here; synctest doesn't help either since the race is
	// against the httptest server's real, non-bubble goroutines.
	time.Sleep(50 * time.Millisecond)
	close(root.proceed) // let Login finish and bind

	loginResp := <-loginDone
	loginResp.Body.Close()
	bumpResp := <-bumpDone
	defer bumpResp.Body.Close()

	assert.Equal(t, http.StatusForbidden, bumpResp.StatusCode,
		"a cookieless dispatch racing a concurrent login must be rejected against the connection it actually runs on, not the one that existed when it was queued")
}

// sessionLiveChild puts the same live unit one level down, under a plain root.
// The session binding is a property of the connection, so a child-scoped
// dispatch must be held to it identically — but the dispatch address gains an
// child key, and the session check and the child lookup are separate sites.
type sessionLiveChild struct{ Child sessionLive }

func (p *sessionLiveChild) View() h.H { return h.Div(h.Str("shell"), via.Child(p.Child)) }

func TestDispatch_liveChildActionUnderASessionRejectsAMismatchedSession(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/login", loginComp{})
	via.Mount(r, "/live", sessionLiveChild{})
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

	req := liveActionRequest(t, srv, string(page), tab, "0", 0) // Bump, no cookie at all
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a child-scoped dispatch against a session-bound connection with no session must be rejected")

	ownReq := liveActionRequest(t, srv, string(page), tab, "0", 0)
	ownResp, err := owner.Do(ownReq)
	require.NoError(t, err)
	defer ownResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, ownResp.StatusCode,
		"the connecting session's own child-scoped dispatch must still succeed")
}

type liveLoginerChild struct{ Child liveLoginer }

func (p *liveLoginerChild) View() h.H { return h.Div(h.Str("shell"), via.Child(p.Child)) }

func TestDispatch_rotateAfterALiveChildLoginKeepsTheBindingOnTheNewID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(liveLoginerChild{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
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

	loginReq := liveActionRequest(t, srv, string(page), tab, "0", 1)
	loginResp, err := owner.Do(loginReq)
	require.NoError(t, err)
	loginResp.Body.Close()
	before := cookieValue(t, owner, srv.URL, "via_session")
	require.NotEmpty(t, before)

	rotReq := liveActionRequest(t, srv, string(page), tab, "0", 2)
	rotResp, err := owner.Do(rotReq)
	require.NoError(t, err)
	rotResp.Body.Close()
	after := cookieValue(t, owner, srv.URL, "via_session")
	require.NotEqual(t, before, after, "Rotate must mint a fresh id")

	okReq := liveActionRequest(t, srv, string(page), tab, "0", 0)
	okResp, err := owner.Do(okReq)
	require.NoError(t, err)
	defer okResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, okResp.StatusCode,
		"the rotated session must still own its child-scoped connection")
}

// sessionTicker reads the session in a Tick, the way a per-user push does,
// and renders what it read.
type sessionTicker struct{ who via.State[string] }

func (p *sessionTicker) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Second, p.tick)
	return nil
}
func (p *sessionTicker) tick(ctx *via.Ctx) {
	m, _ := ctx.Session().Get[member]()
	p.who.Set(m.Name)
}
func (p *sessionTicker) Login(ctx *via.Ctx)  { ctx.Session().Put(member{Name: "alice"}) }
func (p *sessionTicker) Logout(ctx *via.Ctx) { ctx.Session().Delete() }
func (p *sessionTicker) Rotate(ctx *via.Ctx) { ctx.Session().Rotate() }
func (p *sessionTicker) Rename(ctx *via.Ctx) { ctx.Session().Put(member{Name: "bob"}) }
func (p *sessionTicker) View() h.H {
	return h.Div(h.P(h.Str("who:"+p.who.Get())),
		h.Button(via.On("click", p.Login)),
		h.Button(via.On("click", p.Logout)),
		h.Button(via.On("click", p.Rotate)),
		h.Button(via.On("click", p.Rename)))
}

// servedWithJar is vt.Serve whose client keeps cookies, so every Connect and
// Fire carries the session the last response set, as a browser's tabs do.
func servedWithJar(t *testing.T, h http.Handler) *vt.App {
	t.Helper()
	app := vt.Serve(t, h)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	app.Client().Jar = jar
	return app
}

func sessionCookieID(t *testing.T, app *vt.App) string {
	t.Helper()
	u, err := url.Parse(app.URL())
	require.NoError(t, err)
	for _, ck := range app.Client().Jar.Cookies(u) {
		if ck.Name == "via_session" {
			id, _, _ := strings.Cut(ck.Value, ".")
			return id
		}
	}
	require.Fail(t, "no session cookie")
	return ""
}

func TestLive_aSessionWriteOnOneTabReachesEveryTabsTickHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := servedWithJar(t, via.Handler(sessionTicker{}, testSessionKey))
		a := app.Connect()
		code, _ := app.Action(0).Over(a).Fire()
		require.Equal(t, http.StatusNoContent, code)
		b := app.Connect()
		b.Await("who:alice")

		code, _ = app.Action(1).Over(a).Fire()
		require.Equal(t, http.StatusNoContent, code)

		b.Await("who:</p>")
		a.Await("who:</p>")
	})
}

func TestLive_rotateClosesTheSessionsOtherTabsAndKeepsItsOwn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := servedWithJar(t, via.Handler(sessionTicker{}, testSessionKey))
		a := app.Connect()
		code, _ := app.Action(0).Over(a).Fire()
		require.Equal(t, http.StatusNoContent, code)
		b := app.Connect()
		b.Await("who:alice")

		code, _ = app.Action(2).Over(a).Fire()
		require.Equal(t, http.StatusNoContent, code)

		assert.NoError(t, b.AwaitClose(), "a tab rendered under the old id must end cleanly so it reloads")
		time.Sleep(26 * time.Second) // past a keepalive beat, which re-reads the session under the id the tab holds
		code, _ = app.Action(3).Over(a).Fire()
		assert.Equal(t, http.StatusNoContent, code, "the tab that rotated keeps its stream, under the new id")
		a.Await("who:bob")
	})
}

func TestLive_aSessionRevokedByAnotherProcessClosesTheStreamOnTheNextBeat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := via.NewMemorySessionStore()
		app := servedWithJar(t, via.Handler(sessionTicker{}, testSessionKey, via.WithSessionStore(store)))
		a := app.Connect()
		code, _ := app.Action(0).Over(a).Fire()
		require.Equal(t, http.StatusNoContent, code)
		a.Await("who:alice")

		// Straight at the store: this process's own session manager never hears of it.
		require.NoError(t, store.Delete(context.Background(), sessionCookieID(t, app)))
		time.Sleep(25 * time.Second)

		assert.NoError(t, a.AwaitClose(), "a stream whose session is gone from the store must end on its next beat")
	})
}

func TestLive_aSessionWriteInAnotherProcessReachesTickHandlersOnTheNextBeat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := via.NewMemorySessionStore()
		app := servedWithJar(t, via.Handler(sessionTicker{}, testSessionKey, via.WithSessionStore(store)))
		other := servedWithJar(t, via.Handler(sessionTicker{}, testSessionKey, via.WithSessionStore(store)))
		a := app.Connect()
		code, _ := app.Action(0).Over(a).Fire()
		require.Equal(t, http.StatusNoContent, code)
		a.Await("who:alice")

		from, err := url.Parse(app.URL())
		require.NoError(t, err)
		to, err := url.Parse(other.URL())
		require.NoError(t, err)
		other.Client().Jar.SetCookies(to, app.Client().Jar.Cookies(from))
		o := other.Connect()
		code, _ = other.Action(3).Over(o).Fire()
		require.Equal(t, http.StatusNoContent, code)

		time.Sleep(25 * time.Second)
		a.Await("who:bob")
	})
}

func TestLive_aHandlerBlockedPastThePinnedDeadlineIsLoggedWithNoActionPosted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out lockedBuf
		p := newBlockedTick()
		r := via.NewRouter(logTo(&out), via.WithPinnedDeadline(2*time.Second))
		via.Mount(r, "/", p)
		app := vt.Serve(t, r)
		app.Connect()
		<-p.entered

		time.Sleep(3 * time.Second)

		assert.Contains(t, out.String(), "tab pinned", "a pinned tab must be reported even when no action arrives")
		assert.Contains(t, out.String(), "blockedTick")
		close(p.release)
	})
}

// bigFrames pushes a changing ~256KiB element patch every 10ms, enough to
// fill a socket whose reader has stopped.
type bigFrames struct{ n via.State[int] }

func (p *bigFrames) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, p.tick)
	return nil
}
func (p *bigFrames) tick(*via.Ctx) { p.n.Set(p.n.Get() + 1) }
func (p *bigFrames) Bump(*via.Ctx) {}
func (p *bigFrames) View() h.H {
	return h.Div(h.P(h.Str(strings.Repeat("x", 256<<10)), p.n.Display()), h.Button(via.On("click", p.Bump)))
}

func TestLive_aStreamStuckWritingToAClientThatStoppedReadingSaysSo(t *testing.T) {
	t.Parallel()
	var out lockedBuf
	r := via.NewRouter(logTo(&out), via.WithPinnedDeadline(300*time.Millisecond))
	via.Mount(r, "/", bigFrames{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	_, page := do(t, srv, http.MethodGet, "/", "")

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	require.NoError(t, conn.(*net.TCPConn).SetReadBuffer(4096))
	_, err = io.WriteString(conn, "POST /_via/sse HTTP/1.1\r\nHost: "+srv.Listener.Addr().String()+
		"\r\nSec-Fetch-Site: same-origin\r\nContent-Length: 2\r\n\r\n{}")
	require.NoError(t, err)
	br := bufio.NewReader(conn)
	tab := ""
	for tab == "" {
		line, err := br.ReadString('\n')
		require.NoError(t, err)
		if m := tabRe.FindStringSubmatch(line); m != nil {
			tab = m[1]
		}
	}
	// From here nothing reads: the stream fills the socket and blocks in a write.

	require.Eventually(t, func() bool { return strings.Contains(out.String(), "tab pinned") }, 5*time.Second, 20*time.Millisecond)
	resp, _ := post(t, srv, actionURL(t, page, "r", 0), withTab(tab, "{}"), map[string]string{
		"Sec-Fetch-Site": "same-origin", "Datastar-Request": "true",
	})

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Contains(t, out.String(), "not reading", "the warning must say the stream is stuck writing to its client")
	assert.NotContains(t, out.String(), "inside a Tick", "a stalled write is not a blocked handler")
}

// slowDispose is live (its State) and takes longer than the pinned deadline to
// release what it holds, the way a teardown doing I/O does.
type slowDispose struct {
	n    via.State[int]
	gone chan struct{}
}

func (p *slowDispose) OnInit(ctx *via.Ctx) error {
	ctx.OnDispose(func() {
		time.Sleep(10 * time.Second)
		close(p.gone)
	})
	return nil
}

func (p *slowDispose) View() h.H { return h.Div(p.n.Display()) }

func TestLive_aSlowOnDisposeIsNotReportedAsPinned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out lockedBuf
		p := slowDispose{gone: make(chan struct{})}
		r := via.NewRouter(logTo(&out), via.WithPinnedDeadline(2*time.Second))
		via.Mount(r, "/", p)
		app := vt.Serve(t, r)
		app.Connect().Close()
		<-p.gone

		assert.NotContains(t, out.String(), "tab pinned", "a closed tab has no actions left to answer 503")
	})
}

func TestLive_aPinLoggedByTheStreamDoesNotSilenceTheActionThatHitsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out lockedBuf
		p := newBlockedTick()
		r := via.NewRouter(logTo(&out), via.WithPinnedDeadline(2*time.Second))
		via.Mount(r, "/", p)
		app := vt.Serve(t, r)
		conn := app.Connect()
		<-p.entered
		time.Sleep(3 * time.Second)
		require.Equal(t, 1, strings.Count(out.String(), "tab pinned"), "precondition: the stream's own timer logged")

		status, _ := app.Action(0).Over(conn).Fire()

		assert.Equal(t, http.StatusServiceUnavailable, status)
		assert.Equal(t, 2, strings.Count(out.String(), "tab pinned"))
		close(p.release)
	})
}

var pageTabRe = regexp.MustCompile(`<body[^>]*data-signals='\{"viatab":"([^"]*)"\}'`)

func pageTab(t *testing.T, page string) string {
	t.Helper()
	m := pageTabRe.FindStringSubmatch(page)
	require.NotNil(t, m, "the page declares no viatab signal on <body>")
	return m[1]
}

func pageBody(t *testing.T, srv *httptest.Server, c *http.Client, path string) string {
	t.Helper()
	resp, err := c.Get(srv.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

func TestTab_livePageCarriesTheIDItsStreamAdopts(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(clicker{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	id := pageTab(t, page)
	require.NotEmpty(t, id, "a live page must hand out its tab id with the document, before any stream")

	lines, cancel := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(id, "{}"))
	defer cancel()
	assert.Equal(t, id, awaitTabID(t, lines), "the stream must adopt the id its page rendered")
}

func TestTab_eachRenderMintsItsOwnID(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(clicker{}))
	_, a := do(t, srv, http.MethodGet, "/", "")
	_, b := do(t, srv, http.MethodGet, "/", "")
	assert.NotEqual(t, pageTab(t, a), pageTab(t, b))
}

func TestTab_plainPageCarriesNoID(t *testing.T) {
	t.Parallel()
	_, page := do(t, newCounter(t), http.MethodGet, "/", "")
	assert.Empty(t, pageTab(t, page), "a page with no stream has no tab to name")
}

func TestTab_streamRefusesAnIDItDidNotIssue(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(clicker{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	forged := strings.Repeat("A", len(pageTab(t, page)))

	lines, cancel := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(forged, "{}"))
	defer cancel()
	got := awaitTabID(t, lines)
	assert.NotEqual(t, forged, got, "a client-chosen id would let a cross-site page name the tab it then drives")
	assert.NotEmpty(t, got)
}

func TestTab_streamRefusesAnIDIssuedToAnotherSession(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(testSessionKey)
	via.Mount(r, "/login", loginComp{})
	via.Mount(r, "/live", sessionLive{})
	srv := serve(t, r)
	owner := jarClient(t)
	signIn(t, srv, owner, "/login")
	id := pageTab(t, pageBody(t, srv, owner, "/live"))

	lines, cancel := openStreamWithBody(t, srv, jarClient(t), "/live/_via/sse", withTab(id, "{}"))
	defer cancel()
	assert.NotEqual(t, id, awaitTabID(t, lines),
		"a leaked id must not let another browser claim the owner's tab before the owner connects")

	own, cancelOwn := openStreamWithBody(t, srv, owner, "/live/_via/sse", withTab(id, "{}"))
	defer cancelOwn()
	assert.Equal(t, id, awaitTabID(t, own), "the owner's own stream still adopts it")
}

func TestTab_streamRefusesAnIDAnotherStreamHolds(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(clicker{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	id := pageTab(t, page)

	first, cancel := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(id, "{}"))
	defer cancel()
	require.Equal(t, id, awaitTabID(t, first))

	// A duplicated browser tab restores the same document and so the same id.
	second, cancel2 := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(id, "{}"))
	defer cancel2()
	assert.NotEqual(t, id, awaitTabID(t, second), "two streams must never share one tab id")
}

func TestTab_reconnectAfterTheStreamClosedAdoptsTheSameID(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(clicker{}))
	_, page := do(t, srv, http.MethodGet, "/", "")
	id := pageTab(t, page)

	lines, cancel := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(id, "{}"))
	require.Equal(t, id, awaitTabID(t, lines))
	cancel()

	// The server notices the hang-up asynchronously; until it has, the id is
	// still held and a reconnect is handed a fresh one.
	var got string
	for deadline := time.Now().Add(2 * time.Second); got != id && time.Now().Before(deadline); {
		again, stop := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(id, "{}"))
		got = awaitTabID(t, again)
		stop()
	}
	assert.Equal(t, id, got, "a network-drop retry resends the page's id and must get its tab back")
}

func TestDispatch_actionBeforeItsStreamConnectsRunsOnceTheStreamOpens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(clicker{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		id := pageTab(t, page)
		got := fireAsync(srv, liveActionRequest(t, srv, page, id, "r", 0))
		synctest.Wait()
		select {
		case a := <-got:
			require.Failf(t, "answered before its stream connected", "status %d", a.status)
		default:
		}

		lines, cancel := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(id, "{}"))
		defer cancel()
		require.Equal(t, id, awaitTabID(t, lines))
		assert.Equal(t, http.StatusNoContent, (<-got).status, "the click made before the stream connected must run, not 410")
		awaitLine(t, lines, "count: 1")
	})
}

func TestDispatch_actionOnATabWhoseStreamNeverConnectsAnswers410AfterTwoSecondsAtMost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		deadline time.Duration
		want     time.Duration
	}{
		{"deadline past the cap", 10 * time.Second, 2 * time.Second},
		{"deadline under the cap", time.Second, time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := liveServer(t, via.Handler(clicker{}, via.WithPinnedDeadline(tt.deadline)))
				_, page := do(t, srv, http.MethodGet, "/", "")
				start := time.Now()
				assert.Equal(t, http.StatusGone, fireNow(t, srv, liveActionRequest(t, srv, page, pageTab(t, page), "r", 0)))
				assert.Equal(t, tt.want, time.Since(start), "the wait for a stream is min(2s, the pinned deadline)")
			})
		})
	}
}

func TestDispatch_actionWhoseStreamConnectsInsideTwoSecondsRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(clicker{}, via.WithPinnedDeadline(10*time.Second)))
		_, page := do(t, srv, http.MethodGet, "/", "")
		id := pageTab(t, page)
		got := fireAsync(srv, liveActionRequest(t, srv, page, id, "r", 0))

		time.Sleep(1500 * time.Millisecond)
		lines, cancel := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(id, "{}"))
		defer cancel()
		require.Equal(t, id, awaitTabID(t, lines))
		assert.Equal(t, http.StatusNoContent, (<-got).status)
		awaitLine(t, lines, "count: 1")
	})
}

func TestDispatch_actionCarryingAnotherProcesssIDDoesNotWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		podA := liveServer(t, via.Handler(clicker{}, testSessionKey))
		podB := liveServer(t, via.Handler(clicker{}, testSessionKey))
		_, page := do(t, podA, http.MethodGet, "/", "")
		start := time.Now()
		assert.Equal(t, http.StatusGone, fireNow(t, podB, liveActionRequest(t, podB, page, pageTab(t, page), "r", 0)))
		assert.Zero(t, time.Since(start), "a stream for another pod's tab never connects here, so waiting only delays the reload")
	})
}

func TestDispatch_actionCarryingAnIDNoRenderIssuedDoesNotWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(clicker{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		forged := strings.Repeat("A", len(pageTab(t, page)))
		start := time.Now()
		assert.Equal(t, http.StatusGone, fireNow(t, srv, liveActionRequest(t, srv, page, forged, "r", 0)))
		assert.Zero(t, time.Since(start), "only an id this server issued is worth waiting for")
	})
}

type answer struct {
	status int
	at     time.Time
}

// fireAsync sends req in the background and reports its status and when it
// came back, for a test that has to act while the request is parked.
func fireAsync(srv *httptest.Server, req *http.Request) <-chan answer {
	ch := make(chan answer, 1)
	go func() {
		resp, err := srv.Client().Do(req)
		if err != nil {
			ch <- answer{at: time.Now()}
			return
		}
		resp.Body.Close()
		ch <- answer{resp.StatusCode, time.Now()}
	}()
	return ch
}

func fireNow(t *testing.T, srv *httptest.Server, req *http.Request) int {
	t.Helper()
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

func TestDispatch_actionWithAMalformedAddressDoesNotWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(clicker{}))
		_, page := do(t, srv, http.MethodGet, "/", "")
		id := pageTab(t, page)
		good := liveActionRequest(t, srv, page, id, "r", 0)
		act := good.URL.Path[strings.LastIndex(good.URL.Path, "/")+1:]
		for _, path := range []string{"/_via/a/r/nope", "/_via/a/r-1/" + act, "/_via/a/0-/" + act} {
			req := liveActionRequest(t, srv, page, id, "r", 0)
			req.URL.Path = path
			start := time.Now()
			assert.Equal(t, http.StatusGone, fireNow(t, srv, req), path)
			assert.Zero(t, time.Since(start), "%s names no action via could have rendered, so no stream will run it", path)
		}
	})
}

func TestDispatch_parkedActionsPastTheRouterCapAnswer503AtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out lockedBuf
		srv := liveServer(t, via.Handler(clicker{}, via.WithMaxSSEConn(2),
			logTo(&out)))
		_, page := do(t, srv, http.MethodGet, "/", "")
		id := pageTab(t, page)
		parked := []<-chan answer{
			fireAsync(srv, liveActionRequest(t, srv, page, id, "r", 0)),
			fireAsync(srv, liveActionRequest(t, srv, page, id, "r", 0)),
		}
		synctest.Wait()

		start := time.Now()
		for range 2 {
			assert.Equal(t, http.StatusServiceUnavailable, fireNow(t, srv, liveActionRequest(t, srv, page, id, "r", 0)))
		}
		assert.Zero(t, time.Since(start), "a request past the cap is refused, not parked")
		assert.Equal(t, 1, strings.Count(out.String(), "actions parked"), "the refusal is logged once per interval")

		lines, cancel := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(id, "{}"))
		defer cancel()
		require.Equal(t, id, awaitTabID(t, lines))
		for _, p := range parked {
			assert.Equal(t, http.StatusNoContent, (<-p).status, "the actions under the cap still run")
		}
	})
}

func TestDispatch_parkedActionAnswers503WhenTheRouterCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := via.NewRouter()
		via.Mount(r, "/", clicker{})
		srv := liveServer(t, r)
		_, page := do(t, srv, http.MethodGet, "/", "")
		got := fireAsync(srv, liveActionRequest(t, srv, page, pageTab(t, page), "r", 0))
		synctest.Wait()

		start := time.Now()
		r.Close()
		a := <-got

		assert.Equal(t, http.StatusServiceUnavailable, a.status)
		assert.Equal(t, start, a.at, "no stream opens on a closed router, so the wait ends with it")
	})
}

// slowJoin's OnConnect publishes to its own topic, so its stream goroutine is
// inside recv right after the stream registers and cannot pick up an action.
type slowJoin struct {
	n    via.State[int]
	room *topic.Topic[int]
}

func (p *slowJoin) OnInit(ctx *via.Ctx) error {
	ctx.Listen(p.room, p.recv)
	ctx.OnConnect(p.join)
	return nil
}

func (p *slowJoin) join()              { p.room.Publish(1) }
func (p *slowJoin) recv(*via.Ctx, int) { time.Sleep(5 * time.Second) }
func (p *slowJoin) Bump(ctx *via.Ctx)  { p.n.Set(p.n.Get() + 1) }
func (p *slowJoin) View() h.H          { return h.Div(p.n.Display(), h.Button(via.On("click", p.Bump))) }

func TestDispatch_aParkedActionsWholeWaitIsBoundedByThePinnedDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(slowJoin{room: topic.New[int]()}, via.WithPinnedDeadline(3*time.Second),
			logTo(io.Discard)))
		_, page := do(t, srv, http.MethodGet, "/", "")
		id := pageTab(t, page)
		start := time.Now()
		got := fireAsync(srv, liveActionRequest(t, srv, page, id, "r", 0))

		time.Sleep(1500 * time.Millisecond)
		lines, cancel := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(id, "{}"))
		defer cancel()
		require.Equal(t, id, awaitTabID(t, lines))
		a := <-got

		assert.Equal(t, http.StatusServiceUnavailable, a.status)
		assert.Equal(t, 3*time.Second, a.at.Sub(start), "parking and pickup share one deadline")
	})
}

type orderLog struct {
	mu  sync.Mutex
	got []string
}

func (l *orderLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, s)
}

func (l *orderLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.got...)
}

type ordered struct {
	n   via.State[int]
	log *orderLog
}

func (o *ordered) A(*via.Ctx) { o.log.add("A") }
func (o *ordered) B(*via.Ctx) { o.log.add("B") }
func (o *ordered) C(*via.Ctx) { o.log.add("C") }

func (o *ordered) View() h.H {
	return h.Div(o.n.Display(),
		h.Button(via.On("click", o.A)), h.Button(via.On("click", o.B)), h.Button(via.On("click", o.C)))
}

func TestDispatch_parkedActionsRunInArrivalOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for range 50 {
			log := &orderLog{}
			srv := liveServer(t, via.Handler(ordered{log: log}))
			_, page := do(t, srv, http.MethodGet, "/", "")
			id := pageTab(t, page)
			a := fireAsync(srv, liveActionRequest(t, srv, page, id, "r", 0))
			synctest.Wait()
			b := fireAsync(srv, liveActionRequest(t, srv, page, id, "r", 1))
			synctest.Wait()

			connected := make(chan context.CancelFunc, 1)
			go func() {
				lines, cancel := openStreamWithBody(t, srv, srv.Client(), "/_via/sse", withTab(id, "{}"))
				awaitTabID(t, lines)
				connected <- cancel
			}()
			c := fireAsync(srv, liveActionRequest(t, srv, page, id, "r", 2))
			for _, ch := range []<-chan answer{a, b, c} {
				require.Equal(t, http.StatusNoContent, (<-ch).status)
			}
			(<-connected)()
			require.Equal(t, []string{"A", "B", "C"}, log.list())
		}
	})
}

func TestDispatch_actionOnATabTheServerEndedAnswers410AtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := via.NewMemorySessionStore()
		app := servedWithJar(t, via.Handler(sessionTicker{}, testSessionKey, via.WithSessionStore(store)))
		a := app.Connect()
		code, _ := app.Action(0).Over(a).Fire()
		require.Equal(t, http.StatusNoContent, code)
		// Connected after the login, so its id is bound to the cookie a click still carries.
		b := app.Connect()
		b.Await("who:alice")
		require.NoError(t, store.Delete(context.Background(), sessionCookieID(t, app)))
		time.Sleep(25 * time.Second)
		require.NoError(t, b.AwaitClose())

		start := time.Now()
		code, _ = app.Action(3).Over(b).Fire()

		assert.Equal(t, http.StatusGone, code)
		assert.Zero(t, time.Since(start), "a stream the server ended is not coming back under this id")
	})
}

func TestDispatch_actionOnATabWhoseClientHungUpWaitsForItsReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(clicker{}))
		conn := app.Connect()
		id := conn.TabID()
		conn.Close()
		synctest.Wait()

		got := make(chan int, 1)
		go func() {
			code, _ := app.Action(0).Tab(id).Fire()
			got <- code
		}()
		synctest.Wait()
		select {
		case code := <-got:
			require.Failf(t, "answered before the reconnect", "status %d", code)
		default:
		}

		again := app.ConnectWith(`{"viatab":"` + id + `"}`)
		require.Equal(t, id, again.TabID())
		assert.Equal(t, http.StatusNoContent, <-got, "a network drop's retry reconnects under the same id")
		again.Await("count: 1")
	})
}

func TestDispatch_actionsWaitingOnATabAnswer410AtOnceWhenTheServerEndsItsStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newBlockedTick()
		r := via.NewRouter(testSessionKey, via.WithPinnedDeadline(10*time.Second),
			logTo(io.Discard))
		via.Mount(r, "/login", loginComp{})
		via.Mount(r, "/live", p)
		srv := liveServer(t, r)
		c := srv.Client()
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		c.Jar = jar
		signIn(t, srv, c, "/login")
		page := pageBody(t, srv, c, "/live")
		id := pageTab(t, page)
		lines, cancel := openStreamWithBody(t, srv, c, "/live/_via/sse", withTab(id, "{}"))
		defer cancel()
		require.Equal(t, id, awaitTabID(t, lines))
		// The stream goroutine is inside the Tick, so no action on the tab is picked up.
		<-p.entered
		waiting := []<-chan answer{
			fireAsync(srv, liveActionRequest(t, srv, page, id, "r", 0)),
			fireAsync(srv, liveActionRequest(t, srv, page, id, "r", 0)),
		}
		synctest.Wait()
		for _, w := range waiting {
			select {
			case a := <-w:
				require.Failf(t, "answered while the stream was busy", "status %d", a.status)
			default:
			}
		}

		start := time.Now()
		login := pageBody(t, srv, c, "/login")
		rotate, err := http.NewRequest(http.MethodPost, srv.URL+actionURL(t, login, "r", 3), strings.NewReader("{}"))
		require.NoError(t, err)
		rotate.Header.Set("Sec-Fetch-Site", "same-origin")
		rotate.Header.Set("Datastar-Request", "true")
		require.Equal(t, http.StatusNoContent, fireNow(t, srv, rotate))

		for _, w := range waiting {
			a := <-w
			assert.Equal(t, http.StatusGone, a.status)
			assert.Equal(t, start, a.at, "a Rotate ended the stream, so nothing is worth waiting for")
		}
		close(p.release)
	})
}
