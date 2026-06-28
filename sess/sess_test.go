package sess_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via/v2"
	"github.com/go-via/via/v2/h"
	"github.com/go-via/via/v2/sess"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// member is a one-per-session typed value: the "logged-in user" the session
// remembers across requests.
type member struct{ Name string }

// loginComp stores a member on SignIn and, on Greet, reads it back into a field
// the View renders — the only way to observe session persistence through the
// ctx-free View is to copy the session value into the composition during an
// action and let the re-render surface it.
type loginComp struct{ greeting string }

func (c *loginComp) SignIn(ctx *via.Ctx) { sess.Put(ctx, member{Name: "alice"}) }
func (c *loginComp) Greet(ctx *via.Ctx) {
	if m, ok := sess.Get[member](ctx); ok {
		c.greeting = "hi " + m.Name
	}
}
func (c *loginComp) SignOut(ctx *via.Ctx) { sess.Clear[member](ctx) }
func (c *loginComp) Refresh(ctx *via.Ctx) { sess.Rotate(ctx) }
func (c *loginComp) View() h.H {
	return h.Div(
		h.P(h.Str(c.greeting)),
		h.Button(via.OnClick(c.SignIn), h.Str("in")),      // action 0
		h.Button(via.OnClick(c.Greet), h.Str("greet")),    // action 1
		h.Button(via.OnClick(c.SignOut), h.Str("out")),    // action 2
		h.Button(via.OnClick(c.Refresh), h.Str("rotate")), // action 3
	)
}

// tally is a per-session counter; counterComp bumps it and shows it, so two
// browsers can be proven to keep independent counts.
type tally struct{ N int }

type counterComp struct{ shown int }

func (c *counterComp) Bump(ctx *via.Ctx) {
	t, _ := sess.Get[tally](ctx)
	t.N++
	sess.Put(ctx, t)
}
func (c *counterComp) Show(ctx *via.Ctx) {
	t, _ := sess.Get[tally](ctx)
	c.shown = t.N
}
func (c *counterComp) View() h.H {
	return h.Div(
		h.P(h.Str("n="), h.Str(c.shown)),
		h.Button(via.OnClick(c.Bump), h.Str("+")),    // action 0
		h.Button(via.OnClick(c.Show), h.Str("show")), // action 1
	)
}

func cookieValue(t *testing.T, c *http.Client, base, name string) string {
	t.Helper()
	u, err := url.Parse(base)
	require.NoError(t, err)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// greetWithRawCookie fires the Greet action (1) presenting an explicit cookie
// value, so a test can replay a stale session id the jar has already replaced.
func greetWithRawCookie(t *testing.T, base, name, value string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/_via/a/1", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Cookie", name+"="+value)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func jarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar}
}

func fireAction(t *testing.T, c *http.Client, base string, n int) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/_via/a/"+strconv.Itoa(n), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func getPage(t *testing.T, c *http.Client, base string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	return resp
}

func sessionServer(t *testing.T, opts ...via.Option) string {
	t.Helper()
	srv := httptest.NewServer(via.Register(loginComp{}, opts...))
	t.Cleanup(srv.Close)
	return srv.URL
}

// A session exists to outlive a single request: a value stored on one request
// must be readable on the next from the same browser, or "stay logged in" is
// impossible. The signed cookie carries only an id; the value lives server-side
// and is resolved back by that id.
func TestSession_storedValueIsReadableOnALaterRequest(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t) // carries the session cookie across requests

	fireAction(t, c, base, 0) // SignIn → store member{alice}

	_, body := fireAction(t, c, base, 1) // Greet → read it back
	assert.Contains(t, body, "hi alice",
		"a value stored in the session was not readable on a later request")
}

// Clear removes a stored value, so a logout drops the session-held identity:
// after SignIn then SignOut, a Greet must no longer see the member.
func TestSession_clearRemovesAStoredValue(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, base, 0) // SignIn
	fireAction(t, c, base, 2) // SignOut → Clear

	_, body := fireAction(t, c, base, 1) // Greet
	assert.NotContains(t, body, "hi alice", "Clear did not remove the stored session value")
}

// Sessions must be ISOLATED per browser: two clients storing under the same
// type key must keep independent values, or the store is a process-global bag
// that leaks one user's data to another. Two clients bump their own counters a
// different number of times and must each read back only their own.
func TestSession_isolatesValuesPerSession(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(counterComp{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)
	c1, c2 := jarClient(t), jarClient(t)

	fireAction(t, c1, srv.URL, 0) // c1 → 1
	fireAction(t, c1, srv.URL, 0) // c1 → 2
	fireAction(t, c2, srv.URL, 0) // c2 → 1

	_, b1 := fireAction(t, c1, srv.URL, 1)
	_, b2 := fireAction(t, c2, srv.URL, 1)
	assert.Contains(t, b1, "n=2", "client 1's count was wrong — sessions are not isolated")
	assert.Contains(t, b2, "n=1", "client 2 saw another session's count")
}

// Rotate is fixation defense: after an auth change it must mint a NEW session id
// (so any id an attacker fixed before login is useless) while preserving the
// stored data, so the user stays logged in under the new id.
func TestSession_rotateChangesTheIdButKeepsData(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, base, 0) // SignIn
	before := cookieValue(t, c, base, "via_session")
	require.NotEmpty(t, before, "SignIn did not issue a session cookie")

	fireAction(t, c, base, 3) // Refresh → Rotate
	after := cookieValue(t, c, base, "via_session")
	assert.NotEqual(t, before, after, "Rotate must issue a fresh session id")

	_, body := fireAction(t, c, base, 1) // Greet
	assert.Contains(t, body, "hi alice", "Rotate must preserve the session's data")
}

// Rotating before anything is stored still mints a fresh server session and
// issues its cookie — fixation defense must not depend on prior data.
func TestSession_rotateWithoutPriorDataIssuesAFreshCookie(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, base, 3) // Refresh → Rotate, with no prior SignIn
	assert.NotEmpty(t, cookieValue(t, c, base, "via_session"),
		"Rotate on an empty session must still issue a fresh cookie")
}

// The pre-rotate id must stop resolving — a captured/fixed session id is dead
// after rotation, which is the whole point of fixation defense.
func TestSession_rotateInvalidatesTheOldId(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, base, 0) // SignIn
	old := cookieValue(t, c, base, "via_session")
	fireAction(t, c, base, 3) // Refresh → Rotate

	body := greetWithRawCookie(t, base, "via_session", old)
	assert.NotContains(t, body, "hi alice", "the pre-rotate session id still resolved")
}

// Enabling sessions issues the browser an HttpOnly cookie so the session id
// can't be read by page scripts.
func TestSession_issuesAnHttpOnlyCookieWhenEnabled(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))

	// Fire the first session access on a raw request so we can read the
	// Set-Cookie attributes the cookiejar would otherwise hide.
	req, err := http.NewRequest(http.MethodPost, base+"/_via/a/0", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	var sessionCookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "via_session" {
			sessionCookie = ck
		}
	}
	require.NotNil(t, sessionCookie, "no via_session cookie was issued on first session access")
	assert.True(t, sessionCookie.HttpOnly, "session cookie must be HttpOnly")
}

// A session left idle past its TTL must stop resolving — a long-abandoned
// session must not silently resurrect on a late request.
func TestSession_expiresAfterIdleTTL(t *testing.T) {
	t.Parallel()
	base := sessionServer(t,
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")),
		via.WithSessionTTL(30*time.Millisecond))
	c := jarClient(t)

	fireAction(t, c, base, 0)            // SignIn
	time.Sleep(80 * time.Millisecond)    // sit idle past the TTL
	_, body := fireAction(t, c, base, 1) // Greet
	assert.NotContains(t, body, "hi alice", "an idle session past its TTL must not resolve")
}

// WithSessionTTL alone (no WithSessionKey) must still enable sessions, signing
// the cookie with an auto-generated per-process key — zero-config dev sessions.
func TestSession_enabledByTTLAloneUsesAnAutoKey(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionTTL(time.Hour)) // no key supplied
	c := jarClient(t)

	fireAction(t, c, base, 0) // SignIn
	_, body := fireAction(t, c, base, 1)
	assert.Contains(t, body, "hi alice",
		"WithSessionTTL alone must enable sessions with an auto-generated key")
}

// firstActionSessionCookie fires action 0 on a raw (non-jar) request so the
// caller can read the Set-Cookie attributes the jar hides.
func firstActionSessionCookie(t *testing.T, base string) *http.Cookie {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/_via/a/0", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == "via_session" {
			return ck
		}
	}
	return nil
}

// A TLS-terminating proxy forwards plain HTTP to the app, so req.TLS is nil and
// the auto Secure flag would be off even though the user is on https.
// WithSecureCookies forces Secure regardless, for that deployment.
func TestSession_secureCookieOptInForcesSecureOverPlainHTTP(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(loginComp{},
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")),
		via.WithSecureCookies()))
	t.Cleanup(srv.Close)

	ck := firstActionSessionCookie(t, srv.URL)
	require.NotNil(t, ck, "no session cookie was issued")
	assert.True(t, ck.Secure, "WithSecureCookies must set Secure even when the request is plain HTTP")
}

// Over real TLS the cookie must be Secure automatically (no opt-in needed) —
// that auto-detection is exactly why Secure isn't forced by default.
func TestSession_cookieIsSecureOverTLS(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(via.Register(loginComp{},
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_via/a/0", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req) // trusts the test cert
	require.NoError(t, err)
	resp.Body.Close()

	var ck *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "via_session" {
			ck = c
		}
	}
	require.NotNil(t, ck, "no session cookie was issued")
	assert.True(t, ck.Secure, "over TLS the session cookie must be Secure automatically")
}

// Without the opt-in, a plain-HTTP request must NOT get a Secure cookie, or dev
// on http://localhost can never receive it — the ergonomic default.
func TestSession_cookieIsNotSecureOverPlainHTTPByDefault(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(loginComp{},
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	ck := firstActionSessionCookie(t, srv.URL)
	require.NotNil(t, ck)
	assert.False(t, ck.Secure, "a plain-HTTP cookie must not be Secure by default (dev ergonomics)")
}

// liveSess is a live island that establishes its session in OnConnect — the
// place a live app must do it, since the SSE connect response is still open
// there (a live action runs after its 204 and can't set a cookie).
type liveSess struct{}

func (c *liveSess) OnConnect(ctx *via.Ctx) error { sess.Put(ctx, member{Name: "bob"}); return nil }
func (c *liveSess) View() h.H                    { return h.Div(h.Str("live")) }

// A live app's OnConnect must be able to establish the session: the cookie is
// issued on the SSE connect response, so it's in place before any later live
// action needs it.
func TestSession_onConnectEstablishesTheCookie(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(liveSess{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // close the stream so the island tears down
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var names []string
	for _, ck := range resp.Cookies() {
		names = append(names, ck.Name)
	}
	assert.Contains(t, names, "via_session",
		"OnConnect's sess.Put did not establish the session cookie on the SSE connect")
}

// tamperID swaps the first character of the cookie's id part, leaving the
// signature intact so the HMAC no longer matches — a forged/guessed session id.
func tamperID(v string) string {
	i := strings.LastIndexByte(v, '.')
	if i < 0 {
		return v
	}
	id, sig := v[:i], v[i+1:]
	first := "A"
	if strings.HasPrefix(id, "A") {
		first = "B"
	}
	return first + id[1:] + "." + sig
}

// A cookie whose id doesn't match its signature must be rejected outright — the
// signature is what stops an attacker from forging or guessing a session id.
func TestSession_rejectsATamperedCookie(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, base, 0) // SignIn → valid signed cookie
	valid := cookieValue(t, c, base, "via_session")
	require.NotEmpty(t, valid)

	body := greetWithRawCookie(t, base, "via_session", tamperID(valid))
	assert.NotContains(t, body, "hi alice", "a cookie with a broken signature must not resolve a session")
}

// WithSessionCookieName lets an app pick its cookie name — the mitigation for
// two via apps on one host clobbering a shared default cookie.
func TestSession_usesACustomCookieName(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Register(loginComp{},
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")),
		via.WithSessionCookieName("myapp_sid")))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_via/a/0", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	var names []string
	for _, ck := range resp.Cookies() {
		names = append(names, ck.Name)
	}
	assert.Contains(t, names, "myapp_sid", "the custom cookie name was not used")
	assert.NotContains(t, names, "via_session", "the default cookie name must not also be set")
}

// An app that never opts into sessions must stay cookieless — the v2 default —
// and session reads must return the zero value rather than silently sharing
// state across unrelated visitors.
func TestSession_plainAppStaysCookielessAndReadsEmpty(t *testing.T) {
	t.Parallel()
	base := sessionServer(t) // no session option
	c := jarClient(t)

	resp := getPage(t, c, base)
	for _, ck := range resp.Cookies() {
		assert.NotEqual(t, "via_session", ck.Name, "a plain app must not issue a session cookie")
	}

	fireAction(t, c, base, 0) // SignIn → no-op without sessions
	_, body := fireAction(t, c, base, 1)
	assert.NotContains(t, body, "hi alice",
		"without sessions a stored value must not resolve")
}
