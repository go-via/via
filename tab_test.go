package via_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A live action POST with no/unknown tab id (no stream to route to)
// must 410 so a stale client re-bootstraps, never silently mutate a throwaway.
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

// A tab connects anonymously, then a live action logs it in (Session().Put).
// From that point on the connection must stop accepting a cookieless dispatch:
// otherwise a leaked tab id is a bearer token for the logged-in stream.
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

// The README-recommended "establish the session in OnInit" pattern must
// bind the connection too — not just a session that already existed at
// connect time.
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

// Neighbour: two tabs open anonymously against the same app — logging one of
// them in through a live action must bind ONLY that connection. A sibling
// tab that never logs in keeps dispatching cookielessly, exactly as before.
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

// Neighbour: a session that rotates AFTER a live login bound the connection
// must keep the binding across the new id — Rotate moves the same
// *sessionData pointer (see sessionStore.reID), which is what dispatch
// compares against.
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
	// while the connection is STILL unbound — the exact window I3 closes.
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

// sessionLiveEmbed puts the same live unit one level down, under a plain root.
// The session binding is a property of the CONNECTION, so an embed-scoped
// dispatch must be held to it identically — but the dispatch address gains an
// embed key, and the session check and the embed lookup are separate sites.
type sessionLiveEmbed struct{ Child sessionLive }

func (p *sessionLiveEmbed) View() h.H { return h.Div(h.Str("shell"), via.Embed(p.Child)) }

func TestDispatch_liveEmbedActionUnderASessionRejectsAMismatchedSession(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	r.Mount("/login", loginComp{})
	r.Mount("/live", sessionLiveEmbed{})
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
		"an embed-scoped dispatch against a session-bound connection with no session must be rejected")

	ownReq := liveActionRequest(t, srv, string(page), tab, "0", 0)
	ownResp, err := owner.Do(ownReq)
	require.NoError(t, err)
	defer ownResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, ownResp.StatusCode,
		"the connecting session's own embed-scoped dispatch must still succeed")
}

type liveLoginerEmbed struct{ Child liveLoginer }

func (p *liveLoginerEmbed) View() h.H { return h.Div(h.Str("shell"), via.Embed(p.Child)) }

func TestDispatch_rotateAfterALiveEmbedLoginKeepsTheBindingOnTheNewID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(liveLoginerEmbed{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
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
		"the rotated session must still own its embed-scoped connection")
}
