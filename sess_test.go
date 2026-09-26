package via_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
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

// member is a one-per-session value: the "logged-in user" the session
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
func (c *loginComp) SignOut(ctx *via.Ctx) { ctx.Session().Delete() }
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
// returns the currently-rendered action URL for child/n. loginComp's and
// counterComp's View render the same action set regardless of session state,
// so this is safe to call before any login/session step in the test.
func actionPath(t *testing.T, c *http.Client, base string, child string, n int) string {
	t.Helper()
	resp, err := c.Get(base + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return actionURL(t, string(b), child, n)
}

func jarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	// Its own transport: a nil Transport means http.DefaultTransport, shared with
	// every other parallel test, so one test's server teardown closed this
	// client's idle connections mid-request.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Jar: jar, Transport: tr}
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

func TestSession_storedValueIsReadableOnALaterRequest(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t) // carries the session cookie across requests

	fireAction(t, c, base, 0) // SignIn → store member{alice}

	_, body := fireAction(t, c, base, 1) // Greet → read it back
	assert.Contains(t, body, "hi alice",
		"a value stored in the session was not readable on a later request")
}

func TestSession_clearRemovesAStoredValue(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, base, 0) // SignIn
	fireAction(t, c, base, 2) // SignOut → Clear

	_, body := fireAction(t, c, base, 1) // Greet
	assert.NotContains(t, body, "hi alice", "Clear did not remove the stored session value")
}

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

func TestSession_rotateWithoutPriorDataIssuesAFreshCookie(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)

	fireAction(t, c, base, 3) // Refresh → Rotate, with no prior SignIn
	assert.NotEmpty(t, cookieValue(t, c, base, "via_session"),
		"Rotate on an empty session must still issue a fresh cookie")
}

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

func TestSession_activeSessionOutlivesTheTTLCountedFromSignIn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, via.Handler(loginComp{},
			via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		c := &http.Client{Jar: jar, Transport: srv.Client().Transport}

		fireAction(t, c, srv.URL, 0) // SignIn
		for range 5 {
			time.Sleep(10 * time.Hour)
			fireAction(t, c, srv.URL, 1)
		}
		_, body := fireAction(t, c, srv.URL, 1)
		assert.Contains(t, body, "hi alice", "a session in use every 10h must survive 50h past sign-in")
	})
}

func TestSession_streamConnectThatSlidesTheWindowReissuesTheCookie(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
		via.Mount(r, "/", loginComp{})
		via.Mount(r, "/live", sessionLive{})
		srv := httptest.NewTestServer(t, r)
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		c := &http.Client{Jar: jar, Transport: srv.Client().Transport}

		fireAction(t, c, srv.URL, 0) // SignIn
		time.Sleep(13 * time.Hour)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/live/_via/sse", strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		resp, err := c.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEmpty(t, resp.Header.Values("Set-Cookie"),
			"a reconnect is often the only request an open tab makes; the one that slides the window must re-send the cookie")
	})
}

func TestSession_cookieIsNotReissuedOnEveryRequest(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	c := jarClient(t)
	fireAction(t, c, base, 0) // SignIn

	resp := getPage(t, c, base)
	assert.Empty(t, resp.Header.Values("Set-Cookie"),
		"a session well inside its idle window must not re-send its cookie")
}

func TestSession_enabledByTTLAloneUsesAnAutoKey(t *testing.T) {
	t.Parallel()
	base := sessionServer(t, via.WithSessionTTL(time.Hour)) // no key supplied
	c := jarClient(t)

	fireAction(t, c, base, 0) // SignIn
	_, body := fireAction(t, c, base, 1)
	assert.Contains(t, body, "hi alice",
		"WithSessionTTL alone must enable sessions with an auto-generated key")
}

func TestSession_shortSessionKeyPanicsAtConstruction(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		via.Handler(loginComp{}, via.WithSessionKey([]byte("too-short")))
	}, "a session key under the minimum length must panic at construction")
}

var testSessionKey = via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))

// firstActionSessionCookie fires action 0 on a raw (non-jar) request so the
// caller can read the Set-Cookie attributes the jar hides.
func firstActionSessionCookie(t *testing.T, base string, headers ...map[string]string) *http.Cookie {
	t.Helper()
	for _, ck := range sessionActionResponse(t, http.DefaultClient, base, 0, headers...).Cookies() {
		if ck.Name == "via_session" {
			return ck
		}
	}
	return nil
}

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

func TestSession_cookieIsNotSecureOverPlainHTTPByDefault(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(loginComp{},
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	ck := firstActionSessionCookie(t, srv.URL)
	require.NotNil(t, ck)
	assert.False(t, ck.Secure, "a plain-HTTP cookie must not be Secure by default (dev ergonomics)")
}

func TestSession_cookieFollowsTheSchemeAProxyReports(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		headers map[string]string
		secure  bool
	}{
		{"X-Forwarded-Proto https", map[string]string{"X-Forwarded-Proto": "https"}, true},
		{"X-Forwarded-Proto case-folded", map[string]string{"X-Forwarded-Proto": "HTTPS"}, true},
		{"X-Forwarded-Proto chain, client hop first", map[string]string{"X-Forwarded-Proto": "https, http"}, true},
		{"Forwarded proto=https", map[string]string{"Forwarded": "for=192.0.2.60;proto=https;by=203.0.113.43"}, true},
		{"Forwarded quoted, first element", map[string]string{"Forwarded": `proto="https", proto=http`}, true},
		{"X-Forwarded-Proto http", map[string]string{"X-Forwarded-Proto": "http"}, false},
		{"Forwarded proto=http", map[string]string{"Forwarded": "proto=http"}, false},
		{"Forwarded without proto", map[string]string{"Forwarded": "for=192.0.2.60"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(via.Handler(loginComp{}, testSessionKey))
			t.Cleanup(srv.Close)

			ck := firstActionSessionCookie(t, srv.URL, c.headers)
			require.NotNil(t, ck, "no session cookie was issued")
			assert.Equal(t, c.secure, ck.Secure)
		})
	}
}

// liveSess is a live child that establishes its session in OnInit, so
// the cookie rides the SSE connect response itself rather than a later
// action's.
type liveSess struct{}

func (c *liveSess) OnInit(ctx *via.Ctx) error {
	ctx.Session().Put(member{Name: "bob"})
	return nil
}
func (c *liveSess) View() h.H { return h.Div(h.Str("live")) }

func TestSession_onConnectEstablishesTheCookie(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(liveSess{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)

	ctx := t.Context() // close the stream so the child tears down
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
	via.Mount(r, "/p", profilePage{})
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

	// A different Router over the same key and store: a restart, or a second pod.
	b := storeApp(t, via.WithSessionStore(store))
	assert.Contains(t, jarGet(t, c, b.URL+"/p"), "hi alice",
		"a shared SessionStore must carry the session's DATA to another process, not just its cookie")
}

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

func (k *sessKid) OnInit(ctx *via.Ctx) error {
	ctx.Session().Put(sessUser{Name: "ann"})
	return nil
}

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
		via.Child(p.A), via.Child(p.B),
		h.P(h.Str(p.hit)),
		h.Button(via.On("click", p.Save)),
	)
}

func TestSession_siblingChildsShareOneSessionPerRequest(t *testing.T) {
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

func TestSession_siblingChildsShareOneSessionAcrossHydratePasses(t *testing.T) {
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

// hangingStore is the backend that has stopped answering: every call blocks
// until its context is done, which without a store deadline is never — session
// calls deliberately outlive the request's own context.
// abort exists only so a regression fails loudly instead of hanging: without
// it a store that never returns also blocks httptest's Close forever.
type hangingStore struct {
	calls chan struct{}
	abort chan struct{}
}

func (s *hangingStore) block(ctx context.Context) error {
	select {
	case s.calls <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.abort:
		return context.Canceled
	}
}

func (s *hangingStore) Load(ctx context.Context, _ string) ([]byte, bool, error) {
	return nil, false, s.block(ctx)
}
func (s *hangingStore) Save(ctx context.Context, _ string, _ []byte, _ time.Duration) error {
	return s.block(ctx)
}
func (s *hangingStore) Delete(ctx context.Context, _ string) error { return s.block(ctx) }

type sessWritePage struct{}

func (sessWritePage) OnInit(ctx *via.Ctx) error {
	ctx.Session().Put(acct{Name: "alice"})
	return nil
}
func (sessWritePage) View() h.H { return h.P(h.Str("ok")) }

func TestSessionStoreTimeout_freesARequestAHungStoreWouldPin(t *testing.T) {
	t.Parallel()
	store := &hangingStore{calls: make(chan struct{}, 1), abort: make(chan struct{})}
	r := via.NewRouter(
		via.WithSessionStore(store),
		via.WithSessionStoreTimeout(50*time.Millisecond),
	)
	via.Mount(r, "/", sessWritePage{})
	app := vt.Serve(t, r)

	done := make(chan int, 1)
	go func() {
		code, _ := app.Get("/")
		done <- code
	}()
	<-store.calls

	select {
	case code := <-done:
		assert.Equal(t, http.StatusOK, code, "a store timeout must not fail the request")
	case <-time.After(3 * time.Second):
		// Released before the assertion: httptest's Close waits on the pinned
		// request, so a regression would otherwise hang the suite instead of
		// reporting this line.
		close(store.abort)
		<-done
		assert.Fail(t, "a hung session store pinned the request past its store timeout")
	}
}

// Two requests writing one session, a handle whose id was rotated away under
// it, a store that will not settle a conditional write: all of it is reachable
// over HTTP with a user-written SessionStore, so these drive the real server
// rather than forging a Session by hand. The stores below are the deterministic
// part — they fire a whole second request from inside one store call, so the
// interleave a race would only sometimes produce happens every run.

// auditPage exposes one action per write the tests below need; Show copies the
// session value into the render, which is the only way a ctx-free View can
// surface what the store actually holds.
type auditPage struct{ shown string }

func (p *auditPage) Put1(ctx *via.Ctx) { ctx.Session().Put(1) }
func (p *auditPage) Put2(ctx *via.Ctx) { ctx.Session().Put(2) }
func (p *auditPage) Put9(ctx *via.Ctx) { ctx.Session().Put(9) }
func (p *auditPage) Put3(ctx *via.Ctx) { ctx.Session().Put(3) }
func (p *auditPage) Del(ctx *via.Ctx)  { ctx.Session().Delete() }
func (p *auditPage) Rot(ctx *via.Ctx)  { ctx.Session().Rotate() }

func (p *auditPage) Put2Rotate(ctx *via.Ctx) {
	ctx.Session().Put(2)
	ctx.Session().Rotate()
}

// PutTwice writes twice through one handle: if the id was retired under it, the
// first write learns that from the store and the second short-circuits on it.
func (p *auditPage) PutTwice(ctx *via.Ctx) {
	ctx.Session().Put(2)
	ctx.Session().Put(9)
}

func (p *auditPage) Show(ctx *via.Ctx) {
	v, ok := ctx.Session().Get[int]()
	p.shown = "V=" + auditValStr(v, ok)
}

func auditValStr(n int, ok bool) string {
	if !ok {
		return "-"
	}
	return strconv.Itoa(n)
}

func (p *auditPage) View() h.H {
	return h.Div(
		h.P(h.Str(p.shown)),
		h.Button(via.On("click", p.Put1), h.Str("a1")),       // 0
		h.Button(via.On("click", p.Put2), h.Str("a2")),       // 1
		h.Button(via.On("click", p.Put9), h.Str("b9")),       // 2
		h.Button(via.On("click", p.Put3), h.Str("b3")),       // 3
		h.Button(via.On("click", p.Del), h.Str("dela")),      // 4
		h.Button(via.On("click", p.Rot), h.Str("rot")),       // 5
		h.Button(via.On("click", p.Put2Rotate), h.Str("ar")), // 6
		h.Button(via.On("click", p.PutTwice), h.Str("two")),  // 7
		h.Button(via.On("click", p.Show), h.Str("show")),     // 8
	)
}

const (
	audPut1 = iota
	audPut2
	audPut9
	audPut3
	audDel
	audRot
	audPut2Rotate
	audPutTwice
	audShow
)

const auditKey = "a-test-signing-key-32-bytes-long"

// auditServer serves auditPage and resolves its action URLs once, so a test can
// POST without a page GET in between — the store hooks below count store calls,
// and an interleaved GET would move the count they arm on.
func auditServer(t *testing.T, opts ...via.Option) (base string, acts []string) {
	t.Helper()
	srv := httptest.NewServer(via.Handler(auditPage{},
		append([]via.Option{via.WithSessionKey([]byte(auditKey))}, opts...)...))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	for n := range audShow + 1 {
		acts = append(acts, actionURL(t, string(b), "r", n))
	}
	return srv.URL, acts
}

// auditPost fires one action on c and returns the response, which carries both
// the re-rendered body and any Set-Cookie the session issued.
func auditPost(t *testing.T, c *http.Client, base, act string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+act, strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// auditPostAs fires an action presenting an explicit session cookie value —
// a second browser sharing the cookie, or a replay of an id the jar has since
// replaced.
func auditPostAs(t *testing.T, base, act, cookie string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+act, strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Cookie", "via_session="+cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func sessionCookieOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	for _, ck := range resp.Cookies() {
		if ck.Name == "via_session" {
			return ck.Value
		}
	}
	return ""
}

// gapStore fires hook once, right after the nth store read it serves, so a test
// can land a whole second request inside another request's read-modify-write
// window. Load and LoadVersion are armed separately: the merge window opens at
// a different call depending on whether the store does conditional writes.
type gapStore struct {
	inner    via.SessionStore
	loads    atomic.Int64
	loadAt   atomic.Int64
	verLoads atomic.Int64
	verAt    atomic.Int64
	hook     atomic.Pointer[func()]
}

func (s *gapStore) armLoad(n int64, hook func()) {
	s.hook.Store(&hook)
	s.loads.Store(0)
	s.loadAt.Store(n)
}

func (s *gapStore) armLoadVersion(n int64, hook func()) {
	s.hook.Store(&hook)
	s.verLoads.Store(0)
	s.verAt.Store(n)
}

func (s *gapStore) fire(at *atomic.Int64, count *atomic.Int64) {
	n := at.Load()
	if n <= 0 || count.Add(1) != n {
		return
	}
	at.Store(0) // disarmed before the hook runs: the hook reads this store too
	(*s.hook.Load())()
}

func (s *gapStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	data, ok, err := s.inner.Load(ctx, id)
	s.fire(&s.loadAt, &s.loads)
	return data, ok, err
}

func (s *gapStore) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	return s.inner.Save(ctx, id, data, ttl)
}

func (s *gapStore) Delete(ctx context.Context, id string) error { return s.inner.Delete(ctx, id) }

// casGapStore is gapStore with the conditional-write half, so via takes the CAS
// path; plainGapStore (a gapStore alone) is the store that has no such path.
type casGapStore struct{ *gapStore }

func (s *casGapStore) LoadVersion(ctx context.Context, id string) ([]byte, uint64, bool, error) {
	data, ver, ok, err := s.inner.(via.VersionedSessionStore).LoadVersion(ctx, id)
	s.fire(&s.verAt, &s.verLoads)
	return data, ver, ok, err
}

func (s *casGapStore) SaveIf(ctx context.Context, id string, data []byte, ttl time.Duration, ver uint64) (bool, error) {
	return s.inner.(via.VersionedSessionStore).SaveIf(ctx, id, data, ttl, ver)
}

func newPlainGap(inner via.SessionStore) *gapStore { return &gapStore{inner: inner} }

func newCASGap(inner via.VersionedSessionStore) *casGapStore {
	return &casGapStore{gapStore: &gapStore{inner: inner}}
}

// failStore is a store whose backend can be taken down mid-test, one method at
// a time. It keeps the CAS half of the store it wraps, so a test that fails
// Delete still exercises the conditional-write path.
type failStore struct {
	*via.MemorySessionStore
	mu                       sync.Mutex
	loadErr, saveErr, delErr error
	plainSaveErr             error // Save only, not SaveIf: fails a tombstone write while the CAS write lands
}

func (f *failStore) err(which *error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *which
}

func (f *failStore) set(which *error, e error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*which = e
}

func (f *failStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	if e := f.err(&f.loadErr); e != nil {
		return nil, false, e
	}
	return f.MemorySessionStore.Load(ctx, id)
}

func (f *failStore) LoadVersion(ctx context.Context, id string) ([]byte, uint64, bool, error) {
	if e := f.err(&f.loadErr); e != nil {
		return nil, 0, false, e
	}
	return f.MemorySessionStore.LoadVersion(ctx, id)
}

func (f *failStore) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	if e := f.err(&f.saveErr); e != nil {
		return e
	}
	if e := f.err(&f.plainSaveErr); e != nil {
		return e
	}
	return f.MemorySessionStore.Save(ctx, id, data, ttl)
}

func (f *failStore) SaveIf(ctx context.Context, id string, data []byte, ttl time.Duration, ver uint64) (bool, error) {
	if e := f.err(&f.saveErr); e != nil {
		return false, e
	}
	return f.MemorySessionStore.SaveIf(ctx, id, data, ttl, ver)
}

func (f *failStore) Delete(ctx context.Context, id string) error {
	if e := f.err(&f.delErr); e != nil {
		return e
	}
	return f.MemorySessionStore.Delete(ctx, id)
}

func newFailStore() *failStore { return &failStore{MemorySessionStore: via.NewMemorySessionStore()} }

func TestSession_concurrentRequestsLastWriterWins(t *testing.T) {
	t.Parallel()
	store := newPlainGap(via.NewMemorySessionStore())
	base, acts := auditServer(t, via.WithSessionStore(store))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	sid := cookieValue(t, c, base, "via_session")
	require.NotEmpty(t, sid)

	store.armLoad(1, func() { auditPostAs(t, base, acts[audPut9], sid) })
	auditPost(t, c, base, acts[audPut2])

	_, body := auditPost(t, c, base, acts[audShow])
	assert.Contains(t, body, "V=2", "the later save did not win over the interleaved write")
}

func TestSession_clearSurvivesMerge(t *testing.T) {
	t.Parallel()
	store := newPlainGap(via.NewMemorySessionStore())
	base, acts := auditServer(t, via.WithSessionStore(store))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	sid := cookieValue(t, c, base, "via_session")

	store.armLoad(1, func() { auditPostAs(t, base, acts[audPut3], sid) })
	auditPost(t, c, base, acts[audDel])

	_, body := auditPost(t, c, base, acts[audShow])
	assert.Contains(t, body, "V=-", "the Delete was undone by the other request's interleaved write")
}

func TestSession_rotateInvalidatesOldIDWhenDeleteFails(t *testing.T) {
	t.Parallel()
	fs := newFailStore()
	base, acts := auditServer(t, via.WithSessionStore(fs))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	old := cookieValue(t, c, base, "via_session")
	require.NotEmpty(t, old)

	fs.set(&fs.delErr, errors.New("redis down"))
	resp, _ := auditPost(t, c, base, acts[audRot])
	newID := sessionCookieOf(t, resp)
	require.NotEmpty(t, newID)
	require.NotEqual(t, old, newID, "Rotate did not re-id the session")
	fs.set(&fs.delErr, nil)

	_, stale := auditPostAs(t, base, acts[audShow], old)
	assert.Contains(t, stale, "V=-", "the pre-rotation id still resolves after Rotate")

	_, live := auditPostAs(t, base, acts[audShow], newID)
	assert.Contains(t, live, "V=1", "the rotated-to id does not resolve")
}

func TestSession_rotateFails500WhenOldIDCannotBeInvalidated(t *testing.T) {
	// Not t.Parallel(): it reads the package log writer.
	fs := newFailStore()
	base, acts := auditServer(t, via.WithSessionStore(fs))
	c := jarClient(t)
	auditPost(t, c, base, acts[audPut1])

	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prev)

	fs.set(&fs.delErr, errors.New("redis down"))
	fs.set(&fs.plainSaveErr, errors.New("redis down"))
	resp, _ := auditPost(t, c, base, acts[audRot])

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"Rotate reported success with the old id still valid")
	assert.Contains(t, logs.String(), "pre-rotation id is still valid",
		"the log must say the rotation could not invalidate the old id")
}

func TestSession_rotateKeepsOldIDWhenNewIDCannotBeWritten(t *testing.T) {
	t.Parallel()
	fs := newFailStore()
	base, acts := auditServer(t, via.WithSessionStore(fs))
	c := jarClient(t)
	auditPost(t, c, base, acts[audPut1])
	old := cookieValue(t, c, base, "via_session")
	require.NotEmpty(t, old)

	fs.set(&fs.saveErr, errors.New("redis down"))
	resp, _ := auditPost(t, c, base, acts[audRot])
	fs.set(&fs.saveErr, nil)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"Rotate reported a rotation whose new id was never written")
	assert.Empty(t, sessionCookieOf(t, resp), "Rotate issued a cookie for an id with no blob behind it")
	_, body := auditPostAs(t, base, acts[audShow], old)
	assert.Contains(t, body, "V=1", "a failed rotation deleted the only copy of the session")
}

func TestSession_anonymousRotateIssuesNoCookieWhenTheMintCannotBeWritten(t *testing.T) {
	t.Parallel()
	fs := newFailStore()
	base, acts := auditServer(t, via.WithSessionStore(fs))
	fs.set(&fs.saveErr, errors.New("redis down"))

	resp, _ := auditPost(t, jarClient(t), base, acts[audRot])
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"Rotate reported a session that was never written")
	assert.Empty(t, sessionCookieOf(t, resp), "Rotate issued a cookie for an id with no blob behind it")
}

func TestSession_storeOutageDoesNotMintOverExistingCookie(t *testing.T) {
	t.Parallel()
	fs := newFailStore()
	base, acts := auditServer(t, via.WithSessionStore(fs))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	id := cookieValue(t, c, base, "via_session")
	require.NotEmpty(t, id)

	fs.set(&fs.loadErr, errors.New("redis down"))
	resp, _ := auditPostAs(t, base, acts[audPut9], id) // a flash written during the outage
	assert.Empty(t, sessionCookieOf(t, resp), "a replacement session was minted during the outage")
	resp, _ = auditPostAs(t, base, acts[audRot], id)
	assert.Empty(t, sessionCookieOf(t, resp), "Rotate minted a session during the outage")
	fs.set(&fs.loadErr, nil)

	_, body := auditPostAs(t, base, acts[audShow], id)
	assert.Contains(t, body, "V=1", "the real session did not survive the outage")
}

func TestSession_saveRetriesWhenOvertakenMidMerge(t *testing.T) {
	t.Parallel()
	store := newCASGap(via.NewMemorySessionStore())
	base, acts := auditServer(t, via.WithSessionStore(store))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	sid := cookieValue(t, c, base, "via_session")

	store.armLoadVersion(1, func() { auditPostAs(t, base, acts[audPut9], sid) })
	auditPost(t, c, base, acts[audPut2])

	_, body := auditPost(t, c, base, acts[audShow])
	assert.Contains(t, body, "V=2", "the retried write never landed")
}

func TestSession_writeThroughARotatedAwayIDDoesNotReviveIt(t *testing.T) {
	t.Parallel()
	store := newCASGap(via.NewMemorySessionStore())
	base, acts := auditServer(t, via.WithSessionStore(store))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	old := cookieValue(t, c, base, "via_session")

	var newID string
	store.armLoadVersion(1, func() {
		resp, _ := auditPostAs(t, base, acts[audRot], old)
		newID = sessionCookieOf(t, resp)
	})
	auditPostAs(t, base, acts[audPut2], old) // resolved before the rotation, writes after it
	require.NotEmpty(t, newID)
	require.NotEqual(t, old, newID)

	_, stale := auditPostAs(t, base, acts[audShow], old)
	assert.Contains(t, stale, "V=-",
		"a write through the pinned handle re-created a session under the pre-rotation id")
	_, live := auditPostAs(t, base, acts[audShow], newID)
	assert.Contains(t, live, "V=1", "the rotated-to session was disturbed by the dropped write")
}

func TestSession_writeThroughATombstonedIDDoesNotReviveIt(t *testing.T) {
	t.Parallel()
	fs := newFailStore()
	store := newCASGap(fs)
	base, acts := auditServer(t, via.WithSessionStore(store))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	old := cookieValue(t, c, base, "via_session")

	var newID string
	store.armLoadVersion(1, func() {
		fs.set(&fs.delErr, errors.New("redis down"))
		resp, _ := auditPostAs(t, base, acts[audRot], old)
		newID = sessionCookieOf(t, resp)
		fs.set(&fs.delErr, nil)
	})
	auditPostAs(t, base, acts[audPut2], old)
	require.NotEmpty(t, newID)
	require.NotEqual(t, old, newID)

	_, stale := auditPostAs(t, base, acts[audShow], old)
	assert.Contains(t, stale, "V=-",
		"a write through the pinned handle overwrote the rotation tombstone, reviving the old id")
	_, live := auditPostAs(t, base, acts[audShow], newID)
	assert.Contains(t, live, "V=1", "the rotated-to session was disturbed by the dropped write")
}

// casStuckStore never lets a conditional write apply once armed: the CAS loop
// can retry to exhaustion and never settle.
type casStuckStore struct {
	*via.MemorySessionStore
	armed atomic.Bool
}

func (s *casStuckStore) SaveIf(ctx context.Context, id string, data []byte, ttl time.Duration, ver uint64) (bool, error) {
	if s.armed.Load() {
		return false, nil
	}
	return s.MemorySessionStore.SaveIf(ctx, id, data, ttl, ver)
}

func TestSession_saveDropsItsWriteWhenCASNeverSettles(t *testing.T) {
	t.Parallel()
	cs := &casStuckStore{MemorySessionStore: via.NewMemorySessionStore()}
	base, acts := auditServer(t, via.WithSessionStore(cs))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	cs.armed.Store(true)
	auditPost(t, c, base, acts[audPut2])
	cs.armed.Store(false)

	_, body := auditPost(t, c, base, acts[audShow])
	assert.Contains(t, body, "V=1", "an unsettled CAS loop wrote unconditionally instead of dropping")
}

func TestSession_rotateThroughARetiredHandleLeavesTheGoodCookieAlone(t *testing.T) {
	t.Parallel()
	store := newCASGap(via.NewMemorySessionStore())
	base, acts := auditServer(t, via.WithSessionStore(store))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	old := cookieValue(t, c, base, "via_session")

	var newID string
	store.armLoadVersion(1, func() {
		resp, _ := auditPostAs(t, base, acts[audRot], old)
		newID = sessionCookieOf(t, resp)
	})
	// The pinned request's Put is dropped (its id was retired under it); the
	// Rotate that follows must not answer with a cookie of its own.
	pinned, _ := auditPostAs(t, base, acts[audPut2Rotate], old)
	require.NotEmpty(t, newID)
	assert.Empty(t, sessionCookieOf(t, pinned),
		"Rotate through a retired handle issued Set-Cookie, clobbering the browser's good cookie")

	_, live := auditPostAs(t, base, acts[audShow], newID)
	assert.Contains(t, live, "V=1", "the live session was disturbed")
}

// nullValsStore is a third-party store whose backend normalises an empty value
// object to null — legal JSON for the same session. It implements only
// SessionStore, like a store written against the documented interface alone.
type nullValsStore struct {
	inner      via.SessionStore
	normalised atomic.Int64
}

func (s *nullValsStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	return s.inner.Load(ctx, id)
}

func (s *nullValsStore) Delete(ctx context.Context, id string) error { return s.inner.Delete(ctx, id) }

func (s *nullValsStore) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	var blob map[string]json.RawMessage
	if json.Unmarshal(data, &blob) == nil && string(blob["v"]) == "{}" {
		blob["v"] = json.RawMessage("null")
		if out, err := json.Marshal(blob); err == nil {
			s.normalised.Add(1)
			data = out
		}
	}
	return s.inner.Save(ctx, id, data, ttl)
}

func TestSession_nilValsSessionReadsAndWritesAlike(t *testing.T) {
	t.Parallel()
	store := &nullValsStore{inner: via.NewMemorySessionStore()}
	base, acts := auditServer(t, via.WithSessionStore(store))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	auditPost(t, c, base, acts[audDel]) // leaves the session stored with no values
	require.Positive(t, store.normalised.Load(),
		"precondition: the store never saw an empty value map to normalise")

	auditPost(t, c, base, acts[audPut9])

	_, body := auditPost(t, c, base, acts[audShow])
	assert.Contains(t, body, "V=9", "a session that reads fine was retired on its first write")
}

func TestSession_droppedWriteThroughARetiredHandleIsLogged(t *testing.T) {
	// Not t.Parallel(): it reads the package log writer.
	store := newCASGap(via.NewMemorySessionStore())
	base, acts := auditServer(t, via.WithSessionStore(store))
	c := jarClient(t)

	auditPost(t, c, base, acts[audPut1])
	old := cookieValue(t, c, base, "via_session")

	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prev)

	store.armLoadVersion(1, func() { auditPostAs(t, base, acts[audRot], old) })
	auditPostAs(t, base, acts[audPutTwice], old) // both writes land on a retired id

	assert.Contains(t, logs.String(), "session write dropped — this handle's session id was already retired",
		"the second write through a retired handle was dropped in silence")
}

func TestSession_refusesAPageGetWhileTheStoreIsDown(t *testing.T) {
	t.Parallel()
	store := &outageStore{SessionStore: via.NewMemorySessionStore()}
	base := sessionServer(t, via.WithSessionStore(store))
	c := jarClient(t)

	code, _ := fireAction(t, c, base, 0)
	require.Equal(t, http.StatusNoContent, code, "precondition: a healthy sign-in establishes the cookie")

	store.mu.Lock()
	store.down = true
	store.mu.Unlock()

	assert.Equal(t, http.StatusServiceUnavailable, getPage(t, c, base).StatusCode,
		"a page GET during a store outage must not render the signed-out page to a signed-in browser")
}

func TestSession_refusesAPlainActionWhileTheStoreIsDown(t *testing.T) {
	t.Parallel()
	store := &outageStore{SessionStore: via.NewMemorySessionStore()}
	base := sessionServer(t, via.WithSessionStore(store))
	c := jarClient(t)

	code, _ := fireAction(t, c, base, 0)
	require.Equal(t, http.StatusNoContent, code, "precondition: a healthy sign-in establishes the cookie")

	// The action URL is read off a rendered page, so it has to be taken while
	// the store still answers — the GET would otherwise 503 first.
	path := actionPath(t, c, base, "r", 1)

	store.mu.Lock()
	store.down = true
	store.mu.Unlock()

	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
		"a plain action during a store outage must not run against a session read as empty")
	assert.Contains(t, string(body), "session store unavailable")
}

// idComp surfaces ctx.Session().ID() through the View, the only way to read it.
type idComp struct{ shown, before, after string }

// "@" marks the id in the body: an action with no rendered change answers 204.
func (c *idComp) Show(ctx *via.Ctx) { c.shown = "@" + ctx.Session().ID() }
func (c *idComp) Put(ctx *via.Ctx) {
	ctx.Session().Put(member{Name: "alice"})
	c.shown = "@" + ctx.Session().ID()
}
func (c *idComp) Cycle(ctx *via.Ctx) {
	c.before = ctx.Session().ID()
	ctx.Session().Rotate()
	c.after = ctx.Session().ID()
}
func (c *idComp) View() h.H {
	return h.Div(
		h.P(h.Str("id=["), h.Str(c.shown), h.Str("]")),
		h.P(h.Str("before=["), h.Str(c.before), h.Str("] after=["), h.Str(c.after), h.Str("]")),
		h.Button(via.On("click", c.Show), h.Str("show")),   // 0
		h.Button(via.On("click", c.Put), h.Str("put")),     // 1
		h.Button(via.On("click", c.Cycle), h.Str("cycle")), // 2
	)
}

func idServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(via.Handler(idComp{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)
	return srv.URL
}

var rotatedIDs = regexp.MustCompile(`before=\[([^\]]*)\] after=\[([^\]]*)\]`)

func TestSession_idIsEmptyBeforeTheFirstWrite(t *testing.T) {
	t.Parallel()
	c := jarClient(t)

	_, body := fireAction(t, c, idServer(t), 0) // Show, on a request that never writes

	assert.Contains(t, body, "id=[@]", "ID must be empty while there is no session yet")
}

func TestSession_idIsSetAfterPut(t *testing.T) {
	t.Parallel()
	c := jarClient(t)

	_, body := fireAction(t, c, idServer(t), 1) // Put

	assert.NotContains(t, body, "id=[@]", "the first write mints an id ID must report")
	assert.Contains(t, body, "id=[@", "the View did not render the id at all")
}

func TestSession_idSurvivesRotate(t *testing.T) {
	t.Parallel()
	base := idServer(t)
	c := jarClient(t)

	fireAction(t, c, base, 1) // Put — mints the session
	_, body := fireAction(t, c, base, 2)

	m := rotatedIDs.FindStringSubmatch(body)
	require.Len(t, m, 3, "the View did not render both sides of the Rotate: %s", body)
	assert.NotEmpty(t, m[1], "ID must be set on a session that has been written to")
	assert.Equal(t, m[1], m[2], "Rotate re-ids the cookie; ID is the stable identity and must not move")
}

// ensureComp mints the session without storing anything and copies both the
// returned id and whether a value is readable into fields the View renders.
type ensureComp struct {
	sid   string
	value string
}

func (c *ensureComp) Mint(ctx *via.Ctx) {
	c.sid = ctx.Session().Ensure()
	if m, ok := ctx.Session().Get[member](); ok {
		c.value = m.Name
	} else {
		c.value = "none"
	}
}

func (c *ensureComp) View() h.H {
	return h.Div(
		h.P(h.Str("sid="), h.Str(c.sid)),
		h.P(h.Str("value="), h.Str(c.value)),
		h.Button(via.On("click", c.Mint), h.Str("mint")), // action 0
	)
}

var ensureSID = regexp.MustCompile(`sid=([A-Za-z0-9_-]+)`)

func ensureServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(via.Handler(ensureComp{},
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestSession_ensureMintsAnIdAndCookieWithoutAValue(t *testing.T) {
	t.Parallel()
	base := ensureServer(t)
	c := jarClient(t)

	resp := getPage(t, c, base)
	for _, ck := range resp.Cookies() {
		assert.NotEqual(t, "via_session", ck.Name, "a page that never called Ensure issued a session cookie")
	}

	_, body := fireAction(t, c, base, 0)
	assert.NotEmpty(t, cookieValue(t, c, base, "via_session"), "Ensure did not issue the session cookie")
	assert.Regexp(t, ensureSID, body, "Ensure returned an empty id")
	assert.Contains(t, body, "value=none", "Ensure stored a value; it must mint the id only")
}

func TestSession_ensureIsIdempotent(t *testing.T) {
	t.Parallel()
	base := ensureServer(t)
	c := jarClient(t)

	_, first := fireAction(t, c, base, 0)
	_, second := fireAction(t, c, base, 0)

	m1, m2 := ensureSID.FindStringSubmatch(first), ensureSID.FindStringSubmatch(second)
	require.Len(t, m1, 2)
	require.Len(t, m2, 2)
	assert.Equal(t, m1[1], m2[1], "a second Ensure minted a different session id")
}

// tickEnsurer calls Ensure from a Tick, where no response can carry a cookie.
type tickEnsurer struct{ got via.State[string] }

func (u *tickEnsurer) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, func(ctx *via.Ctx) {
		u.got.Set("ensure=[" + ctx.Session().Ensure() + "]")
	})
	return nil
}
func (u *tickEnsurer) View() h.H { return h.Div(u.got.Display()) }

func TestSession_ensureFromATickReturnsEmptyAndMintsNothing(t *testing.T) {
	// Sequential: it captures the global log output.
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(tickEnsurer{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
		lines, cancel := openStream(t, srv)
		defer cancel()
		_ = awaitTabID(t, lines)
		awaitLine(t, lines, "ensure=[]")
		synctest.Wait()
	})

	assert.NotContains(t, buf.String(), "no cookie can be set",
		"Ensure has nothing to store, so it must not mint a session the browser never learns of")
}

// namePutter writes the session from a Listen handler, through the handle its
// stream connected with.
type namePutter struct {
	Names *topic.Topic[string]
	done  via.State[string]
}

func (p *namePutter) OnInit(ctx *via.Ctx) error {
	ctx.Listen(p.Names, p.put)
	return nil
}

func (p *namePutter) put(ctx *via.Ctx, name string) {
	ctx.Session().Put(member{Name: name})
	p.done.Set("put " + name)
}

func (p *namePutter) View() h.H { return h.Div(p.done.Display()) }

func TestSession_listenHandlerPutAfterARotateElsewhereDoesNotReviveTheOldID(t *testing.T) {
	t.Parallel()
	names := topic.New[string]()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/", loginComp{})
	via.Mount(r, "/live", namePutter{Names: names})
	app := vt.Serve(t, r)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	app.Client().Jar = jar
	c := app.Client()

	fireAction(t, c, app.URL(), 0) // SignIn
	u, err := url.Parse(app.URL())
	require.NoError(t, err)
	old := jar.Cookies(u)
	require.NotEmpty(t, old)
	conn := app.ConnectAt("/live", "{}")

	fireAction(t, c, app.URL(), 3) // Rotate
	names.Publish("mallory")
	require.NoError(t, conn.AwaitClose(), "a Rotate elsewhere ends the stream holding the old id")

	stale := jarClient(t)
	stale.Transport = c.Transport
	stale.Jar.SetCookies(u, old)
	_, body := fireAction(t, stale, app.URL(), 1) // Greet
	assert.NotContains(t, body, "hi ", "the rotated-away id must not resolve again")
	_, body = fireAction(t, c, app.URL(), 1)
	assert.Contains(t, body, "hi alice", "the rotated session keeps its own value")
}

func TestSession_responsesThatCarryASessionAreNotStored(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(loginComp{}, testSessionKey))
	t.Cleanup(srv.Close)
	c := jarClient(t)

	anon := getPage(t, c, srv.URL)
	anon.Body.Close()
	assert.Equal(t, "no-cache", anon.Header.Get("Cache-Control"), "a plain page served without a session is shareable but revalidated")

	signIn := sessionActionResponse(t, c, srv.URL, 0)
	assert.Equal(t, "private, no-store", signIn.Header.Get("Cache-Control"), "the response that sets the cookie")

	page := getPage(t, c, srv.URL)
	page.Body.Close()
	assert.Equal(t, "private, no-store", page.Header.Get("Cache-Control"), "a page rendered for a session")

	greet := sessionActionResponse(t, c, srv.URL, 1)
	assert.Equal(t, "private, no-store", greet.Header.Get("Cache-Control"), "an action run for a session")
}

func TestSession_streamThatSetsTheCookieIsNotStored(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(via.Handler(liveSessStream{}, testSessionKey))
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "private, no-store", resp.Header.Get("Cache-Control"))
}

func sessionActionResponse(t *testing.T, c *http.Client, base string, n int, headers ...map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+actionPath(t, c, base, "r", n), strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Datastar-Request", "true")
	for _, hs := range headers {
		for k, v := range hs {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	return resp
}
