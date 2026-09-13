package via_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
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

// member is a one-per-session typed value: the "logged-in user" the session
// remembers across requests.
type member struct{ Name string }

// loginComp stores a member on SignIn and, on Greet, reads it back into a field
// the View renders — the only way to observe session persistence through the
// ctx-free View is to copy the session value into the composition during an
// action and let the re-render surface it.
type loginComp struct{ greeting string }

func (c *loginComp) SignIn(ctx *via.Ctx) { ctx.Session().Put(member{Name: "alice"}) }
func (c *loginComp) Greet(ctx *via.Ctx) {
	if m, ok := ctx.Session().Get[member](); ok {
		c.greeting = "hi " + m.Name
	}
}
func (c *loginComp) SignOut(ctx *via.Ctx) { ctx.Session().Delete[member]() }
func (c *loginComp) Refresh(ctx *via.Ctx) { ctx.Session().Rotate() }
func (c *loginComp) View() h.H {
	return h.Div(
		h.P(h.Str(c.greeting)),
		h.Button(via.On("click", c.SignIn), h.Str("in")),      // action 0
		h.Button(via.On("click", c.Greet), h.Str("greet")),    // action 1
		h.Button(via.On("click", c.SignOut), h.Str("out")),    // action 2
		h.Button(via.On("click", c.Refresh), h.Str("rotate")), // action 3
	)
}

// tally is a per-session counter; counterComp bumps it and shows it, so two
// browsers can be proven to keep independent counts.
type tally struct{ N int }

type counterComp struct{ shown int }

func (c *counterComp) Bump(ctx *via.Ctx) {
	t, _ := ctx.Session().Get[tally]()
	t.N++
	ctx.Session().Put(t)
}
func (c *counterComp) Show(ctx *via.Ctx) {
	t, _ := ctx.Session().Get[tally]()
	c.shown = t.N
}
func (c *counterComp) View() h.H {
	return h.Div(
		h.P(h.Str("n="), h.Str(c.shown)),
		h.Button(via.On("click", c.Bump), h.Str("+")),    // action 0
		h.Button(via.On("click", c.Show), h.Str("show")), // action 1
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
	req, err := http.NewRequest(http.MethodPost, base+actionPath(t, http.DefaultClient, base, "r", 1), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Cookie", name+"="+value)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// actionPath fetches base's root page on a bare (cookie-less) client and
// returns the currently-rendered action URL for embed/n. loginComp's and
// counterComp's View render the same action set regardless of session state,
// so this is safe to call before any login/session step in the test.
func actionPath(t *testing.T, c *http.Client, base string, embed string, n int) string {
	t.Helper()
	resp, err := c.Get(base + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return actionURL(t, string(b), embed, n)
}

func jarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar}
}

func fireAction(t *testing.T, c *http.Client, base string, n int) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+actionPath(t, c, base, "r", n), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
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
	srv := httptest.NewServer(via.Handler(loginComp{}, opts...))
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
	srv := httptest.NewServer(via.Handler(counterComp{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
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

// Writing into a session never rotates its id on its own. Rotate-on-write is
// tempting but wrong: "first write" can only be tracked per REQUEST, so every
// writing request rotates again, and a request still carrying the previous id
// (a double-click, a retried form) forks a fresh, empty session instead of
// resolving to the one the user was just using. Fixation defense is
// [Session.Rotate], called explicitly at an auth-state change.
func TestSession_writingIntoASessionDoesNotRotateItsID(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, base, 0) // SignIn — first write, mints the session
	first := cookieValue(t, c, base, "via_session")
	require.NotEmpty(t, first)

	fireAction(t, c, base, 1) // Greet — a second write-capable request, no Rotate call
	second := cookieValue(t, c, base, "via_session")
	assert.Equal(t, first, second, "a write with no explicit Rotate call must not change the session id")
}

// Neighbour of the no-auto-rotate fix: concurrent writers racing the SAME
// cookie must not each mint their own session — that was the reID leak the
// automatic rotation caused (N concurrent Puts -> N live ids aliasing one
// *sessionData, none ever swept). With rotation gone from the write path,
// concurrent Bumps on one cookie must all land on the one id the
// SignIn-less jar started with. (What they do to the counter once there is
// a lost-update race the Session API never promised to prevent — Get/Put
// isn't compare-and-swap — so this test only pins the id, not the sum.)
func TestSession_concurrentWritesOnOneCookieUseOneSessionID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(counterComp{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)
	c := jarClient(t)

	fireAction(t, c, srv.URL, 0) // Bump — mints the session and its cookie
	id := cookieValue(t, c, srv.URL, "via_session")
	require.NotEmpty(t, id)

	const n = 16
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fireAction(t, c, srv.URL, 0) // Bump, same cookie
		}()
	}
	wg.Wait()

	assert.Equal(t, id, cookieValue(t, c, srv.URL, "via_session"),
		"concurrent writes on one cookie must not rotate/fork the session id")
}

// Enabling sessions issues the browser an HttpOnly cookie so the session id
// can't be read by page scripts.
func TestSession_issuesAnHttpOnlyCookieWhenEnabled(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))

	// Fire the first session access on a raw request so we can read the
	// Set-Cookie attributes the cookiejar would otherwise hide.
	req, err := http.NewRequest(http.MethodPost, base+actionPath(t, http.DefaultClient, base, "r", 0), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
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
	assert.Equal(t, http.SameSiteLaxMode, sessionCookie.SameSite, "session cookie must be SameSite=Lax")
	assert.Greater(t, sessionCookie.MaxAge, 0, "session cookie must carry a MaxAge, not expire with the browser session")
}

// A session left idle past its TTL must stop resolving — a long-abandoned
// session must not silently resurrect on a late request. This runs at the
// real default TTL (24h), not a shortened override: synctest's fake clock
// makes the wait free in wall time, and proves the documented default rather
// than a stand-in for it. The server must live on httptest's in-memory
// network — a real listener's Accept-loop goroutine would block the bubble
// from ever going idle.
func TestSession_expiresAfterIdleTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, via.Handler(loginComp{},
			via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		c := &http.Client{Jar: jar, Transport: srv.Client().Transport}

		fireAction(t, c, srv.URL, 0)            // SignIn
		time.Sleep(24*time.Hour + time.Second)  // sit idle past the default TTL
		_, body := fireAction(t, c, srv.URL, 1) // Greet
		assert.NotContains(t, body, "hi alice", "an idle session past its TTL must not resolve")
	})
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

// A short WithSessionKey is a guessable HMAC-SHA256 key — accepted silently,
// it would forgeably sign every session cookie the app issues. It must fail
// loudly at construction, not at request time.
func TestSession_shortSessionKeyPanicsAtConstruction(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		via.Handler(loginComp{}, via.WithSessionKey([]byte("too-short")))
	}, "a session key under the minimum length must panic at construction")
}

// firstActionSessionCookie fires action 0 on a raw (non-jar) request so the
// caller can read the Set-Cookie attributes the jar hides.
func firstActionSessionCookie(t *testing.T, base string) *http.Cookie {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+actionPath(t, http.DefaultClient, base, "r", 0), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
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
	srv := httptest.NewServer(via.Handler(loginComp{},
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
	srv := httptest.NewTLSServer(via.Handler(loginComp{},
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+actionPath(t, srv.Client(), srv.URL, "r", 0), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
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
	srv := httptest.NewServer(via.Handler(loginComp{},
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	ck := firstActionSessionCookie(t, srv.URL)
	require.NotNil(t, ck)
	assert.False(t, ck.Secure, "a plain-HTTP cookie must not be Secure by default (dev ergonomics)")
}

// liveSess is a live embed that establishes its session in OnInit, so
// the cookie rides the SSE connect response itself rather than a later
// action's.
type liveSess struct{}

func (c *liveSess) OnInit(ctx *via.Ctx) error { ctx.Session().Put(member{Name: "bob"}); return nil }
func (c *liveSess) View() h.H                 { return h.Div(h.Str("live")) }

// A live app's OnInit must be able to establish the session: the cookie is
// issued on the SSE connect response, so it's in place before any later live
// action needs it.
func TestSession_onConnectEstablishesTheCookie(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(liveSess{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	ctx := t.Context() // close the stream so the embed tears down
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
		"OnInit's ctx.Session().Put did not establish the session cookie on the SSE connect")
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

// hmacSign reproduces sessionManager.sign under an arbitrary key, so a test
// can forge a signature the server's real key never produced.
func hmacSign(key []byte, id string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// A cookie whose id is genuinely valid (still resolvable in the store) but
// whose signature has been corrupted must still be rejected — tamperID above
// only ever breaks the id half, so sabotaging verify to always succeed passes
// every existing test (the forged id fails the store lookup regardless of
// what verify says). This corrupts only the signature half of a real,
// currently-valid cookie, isolating the MAC check itself.
func TestSession_rejectsACookieWithACorruptedSignature(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, base, 0) // SignIn → valid signed cookie
	valid := cookieValue(t, c, base, "via_session")
	require.NotEmpty(t, valid)

	i := strings.LastIndexByte(valid, '.')
	require.GreaterOrEqual(t, i, 0)
	id, sig := valid[:i], valid[i+1:]
	first := "A"
	if strings.HasPrefix(sig, "A") {
		first = "B"
	}
	corrupted := id + "." + first + sig[1:]

	body := greetWithRawCookie(t, base, "via_session", corrupted)
	assert.NotContains(t, body, "hi alice",
		"a real session id with a corrupted signature must not resolve")
}

// A cookie whose id is genuine but whose signature was produced by a
// DIFFERENT key (the two-apps-on-localhost-with-different-secrets case) must
// be rejected exactly like a corrupted signature — this is the same MAC
// check, exercised via a differently-keyed forgery rather than bit damage.
func TestSession_rejectsACookieSignedUnderADifferentKey(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, base, 0) // SignIn → valid signed cookie
	valid := cookieValue(t, c, base, "via_session")
	require.NotEmpty(t, valid)

	i := strings.LastIndexByte(valid, '.')
	require.GreaterOrEqual(t, i, 0)
	id := valid[:i]
	forged := id + "." + hmacSign([]byte("a-different-signing-key-32-bytes"), id)

	body := greetWithRawCookie(t, base, "via_session", forged)
	assert.NotContains(t, body, "hi alice",
		"a real session id signed under a different key must not resolve")
}

// WithSessionCookieName lets an app pick its cookie name — the mitigation for
// two via apps on one host clobbering a shared default cookie.
func TestSession_usesACustomCookieName(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(loginComp{},
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")),
		via.WithSessionCookieName("myapp_sid")))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+actionPath(t, http.DefaultClient, srv.URL, "r", 0), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
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

// Sessions are always available: a plain app (no session option) stays
// cookieless until the first ctx.Session().Put, and from that write on the value
// resolves — no opt-in ceremony, the cookie is the lazy consequence of the
// first write. Fails if sessions go back behind an option gate, or if a page
// view starts minting cookies eagerly.
func TestSession_alwaysOnLazyCookie(t *testing.T) {
	t.Parallel()
	base := sessionServer(t) // no session option
	c := jarClient(t)

	resp := getPage(t, c, base)
	for _, ck := range resp.Cookies() {
		assert.NotEqual(t, "via_session", ck.Name, "a read-only page must not issue a session cookie")
	}

	fireAction(t, c, base, 0) // SignIn → Put mints the session lazily
	_, body := fireAction(t, c, base, 1)
	assert.Contains(t, body, "hi alice",
		"sessions are always on: a stored value must resolve without any With* option")
}

// sharedStore is a SessionStore written the way a user's Redis or SQL one is:
// from outside the package, against the exported interface only, with no
// knowledge of via's session internals. Blobs in, blobs out.
type sharedStore struct {
	mu   sync.Mutex
	m    map[string][]byte
	ttls []time.Duration
}

func newSharedStore() *sharedStore { return &sharedStore{m: map[string][]byte{}} }

func (s *sharedStore) Load(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[id]
	return b, ok, nil
}

func (s *sharedStore) Save(_ context.Context, id string, data []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = data
	s.ttls = append(s.ttls, ttl)
	return nil
}

func (s *sharedStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

func (s *sharedStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.m))
}

func (s *sharedStore) lastTTL() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ttls) == 0 {
		return 0
	}
	return s.ttls[len(s.ttls)-1]
}

// storeApp builds a fresh Router over the given store — a redeploy, or a second
// pod, as far as sessions are concerned.
func storeApp(t *testing.T, opts ...via.Option) *httptest.Server {
	t.Helper()
	r := via.NewRouter(append([]via.Option{via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))}, opts...)...)
	r.Mount("/p", profilePage{})
	return serve(t, r)
}

func TestSessionStore_sharedStoreCarriesSessionsAcrossProcesses(t *testing.T) {
	t.Parallel()
	store := newSharedStore()
	c := jarClient(t)

	a := storeApp(t, via.WithSessionStore(store))
	page := jarGet(t, c, a.URL+"/p")
	jarPost(t, c, a.URL+actionURL(t, page, "r", 0))
	require.Contains(t, jarGet(t, c, a.URL+"/p"), "hi alice", "the session must work on the pod that minted it")

	// A DIFFERENT Router over the SAME key and store: a restart, or a second pod.
	b := storeApp(t, via.WithSessionStore(store))
	assert.Contains(t, jarGet(t, c, b.URL+"/p"), "hi alice",
		"a shared SessionStore must carry the session's DATA to another process, not just its cookie")
}

// The repro the README used to deny: a stable key alone keeps the COOKIE valid
// and loses the DATA, because the default store is this process's memory.
func TestSessionStore_defaultStoreLosesSessionsOnRestart(t *testing.T) {
	t.Parallel()
	c := jarClient(t)

	a := storeApp(t)
	page := jarGet(t, c, a.URL+"/p")
	jarPost(t, c, a.URL+actionURL(t, page, "r", 0))
	require.Contains(t, jarGet(t, c, a.URL+"/p"), "hi alice")

	b := storeApp(t)
	assert.NotContains(t, jarGet(t, c, b.URL+"/p"), "hi alice",
		"the in-process default cannot outlive its process — WithSessionStore is the fix")
}

func TestSessionStore_rotateMovesTheBlobToTheNewIDAndDropsTheOld(t *testing.T) {
	t.Parallel()
	store := newSharedStore()
	srv := sessionServer(t, via.WithSessionStore(store), via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, srv, 0) // SignIn
	before := store.keys()
	require.Len(t, before, 1, "one write, one stored session")

	fireAction(t, c, srv, 3) // Refresh → Rotate
	after := store.keys()
	assert.Len(t, after, 1, "Rotate must not leave the pre-rotation id behind")
	assert.NotEqual(t, before[0], after[0], "Rotate must re-id the session in the store")

	_, body := fireAction(t, c, srv, 1) // Greet
	assert.Contains(t, body, "hi alice", "the data must ride to the new id")
}

func TestSessionStore_handsTheConfiguredTTLToEverySave(t *testing.T) {
	t.Parallel()
	store := newSharedStore()
	srv := sessionServer(t, via.WithSessionStore(store), via.WithSessionTTL(90*time.Minute),
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	fireAction(t, jarClient(t), srv, 0) // SignIn

	assert.Equal(t, 90*time.Minute, store.lastTTL(),
		"a store must be handed the expiry deadline rather than reimplement TTL")
}

// A store that ignores the ttl it was handed must still not resurrect an idle
// session: via stamps its own deadline into the blob.
func TestSessionStore_expiredBlobIsRefusedEvenWhenTheStoreIgnoresTTL(t *testing.T) {
	t.Parallel()
	store := newSharedStore()
	c := jarClient(t)

	srv := sessionServer(t, via.WithSessionStore(store), via.WithSessionTTL(time.Hour),
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	fireAction(t, c, srv, 0) // SignIn
	_, body := fireAction(t, c, srv, 1)
	require.Contains(t, body, "hi alice")

	// Rewrite the blob's own deadline into the past, leaving the store's entry
	// live — exactly what a backend with no expiry support leaves behind.
	for _, k := range store.keys() {
		raw, _, _ := store.Load(context.Background(), k)
		var blob map[string]any
		require.NoError(t, json.Unmarshal(raw, &blob))
		blob["exp"] = time.Now().Add(-time.Minute).UnixNano()
		out, err := json.Marshal(blob)
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), k, out, time.Hour))
	}
	_, after := fireAction(t, c, srv, 1)
	assert.NotContains(t, after, "hi alice",
		"via's own expiry stamp must fail an idle session closed")
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
	app := vt.Serve(t, via.Handler(sessInAction{}))
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

// sessKid writes the session from its own OnInit; two of them under a root that
// never calls Session() is the sibling case.
type sessKid struct{ Tag string }

func (k *sessKid) OnInit(ctx *via.Ctx) error { ctx.Session().Put(sessUser{Name: "ann"}); return nil }

func (k *sessKid) View() h.H { return h.P(h.Str(k.Tag)) }

type sessSiblings struct {
	Q    via.Signal[string]
	A, B sessKid
	hit  string
}

func (p *sessSiblings) Save(ctx *via.Ctx) {
	u, ok := ctx.Session().Get[sessUser]()
	p.hit = fmt.Sprintf("who:%v:%s", ok, u.Name)
}

func (p *sessSiblings) View() h.H {
	return h.Div(
		h.Input(p.Q.Bind()),
		via.Embed(p.A), via.Embed(p.B),
		h.P(h.Str(p.hit)),
		h.Button(via.On("click", p.Save)),
	)
}

func TestSession_siblingEmbedsShareOneSessionPerRequest(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(sessSiblings{}))

	req, err := http.NewRequest(http.MethodGet, app.URL()+"/", nil)
	require.NoError(t, err)
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Len(t, resp.Header.Values("Set-Cookie"), 1,
		"the root resolves the session once per request; siblings must inherit it, not mint their own")
}

func TestSession_siblingEmbedsShareOneSessionAcrossHydratePasses(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(sessSiblings{}))
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

	assert.Len(t, resp.Header.Values("Set-Cookie"), 1,
		"a second discovery pass must not re-mint per sibling")
	assert.Contains(t, string(b), "who:true:ann",
		"and the handler must read the very session the children wrote")
}
