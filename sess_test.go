package via_test

import (
	"html"
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

type testUser struct {
	Name string
}

func TestGetSess_returnsZeroValueWhenNeverSet(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			user := via.GetSess[testUser](ctx)
			return h.Div(h.Text(user.Name))
		})
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/")
	// Name is empty string — the div should have no text content
	assert.NotContains(t, body, "alice")
}

func TestSess_setsSessionCookieOnFirstRequest(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	var sessCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "via_session" {
			sessCookie = c
		}
	}
	require.NotNil(t, sessCookie, "page GET should set via_session cookie")
	assert.True(t, sessCookie.HttpOnly, "session cookie should be HttpOnly")
	assert.Equal(t, "/", sessCookie.Path, "session cookie should have Path=/")
}

func TestGetSess_readsValueSetBySetSess(t *testing.T) {
	t.Parallel()

	gotCh := make(chan testUser, 1)
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.HandleFunc("POST /set-user", func(w http.ResponseWriter, r *http.Request) {
		via.SetSess(w, r, testUser{Name: "alice"})
	})

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			user := via.GetSess[testUser](ctx)
			if user.Name != "" {
				gotCh <- user
			}
			return h.Div()
		})
	})
	t.Cleanup(server.Close)

	// First visit to get session cookie
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	jar := collectCookies(t, server.URL, resp.Cookies())

	// Set session data via HTTP handler
	req, _ := http.NewRequest("POST", server.URL+"/set-user", nil)
	addCookies(req, jar)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	jar = mergeCookies(jar, resp2.Cookies())

	// Revisit page — view should read the session data
	req2, _ := http.NewRequest("GET", server.URL+"/", nil)
	addCookies(req2, jar)
	resp3, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	resp3.Body.Close()

	select {
	case user := <-gotCh:
		assert.Equal(t, "alice", user.Name)
	case <-time.After(sseTimeout):
		require.Fail(t, "view did not read session data set by SetSess")
	}
}

func TestClearSess_destroysSessionData(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.HandleFunc("POST /set-user", func(w http.ResponseWriter, r *http.Request) {
		via.SetSess(w, r, testUser{Name: "bob"})
	})

	v.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		via.ClearSess(w, r)
	})

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			user := via.GetSess[testUser](ctx)
			return h.Div(h.Text(user.Name))
		})
	})
	t.Cleanup(server.Close)

	// Get session cookie
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	jar := collectCookies(t, server.URL, resp.Cookies())

	// Set user
	req, _ := http.NewRequest("POST", server.URL+"/set-user", nil)
	addCookies(req, jar)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	jar = mergeCookies(jar, resp2.Cookies())

	// Clear session
	req2, _ := http.NewRequest("POST", server.URL+"/logout", nil)
	addCookies(req2, jar)
	resp3, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	resp3.Body.Close()
	jar = mergeCookies(jar, resp3.Cookies())

	// Revisit — session data should be gone
	req3, _ := http.NewRequest("GET", server.URL+"/", nil)
	addCookies(req3, jar)
	resp4, err := http.DefaultClient.Do(req3)
	require.NoError(t, err)
	body, err := io.ReadAll(resp4.Body)
	require.NoError(t, err)
	resp4.Body.Close()

	assert.NotContains(t, string(body), "bob", "session data should be cleared after ClearSess")
}

func TestGetSess_returnsZeroOnWrongArgType(t *testing.T) {
	t.Parallel()
	user := via.GetSess[testUser](42)
	assert.Equal(t, testUser{}, user)
}

func TestSetSess_noopsOnNilWriter(t *testing.T) {
	t.Parallel()
	// Should not panic
	via.SetSess[testUser](nil, nil, testUser{Name: "nope"})
}

func TestClearSess_noopsOnNilWriter(t *testing.T) {
	t.Parallel()
	// Should not panic
	via.ClearSess(nil, nil)
}

func TestGetSess_isolatesBetweenSessions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.HandleFunc("POST /set-user", func(w http.ResponseWriter, r *http.Request) {
		via.SetSess(w, r, testUser{Name: r.URL.Query().Get("name")})
	})

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			user := via.GetSess[testUser](ctx)
			return h.Div(h.Text(user.Name))
		})
	})
	t.Cleanup(server.Close)

	// Session A: set user to "alice"
	respA, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	respA.Body.Close()
	jarA := collectCookies(t, server.URL, respA.Cookies())

	reqA, _ := http.NewRequest("POST", server.URL+"/set-user?name=alice", nil)
	addCookies(reqA, jarA)
	respA2, err := http.DefaultClient.Do(reqA)
	require.NoError(t, err)
	respA2.Body.Close()

	// Session B (fresh, no cookies): visit page — should NOT see alice
	respB, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	bodyB, err := io.ReadAll(respB.Body)
	require.NoError(t, err)
	respB.Body.Close()

	assert.NotContains(t, string(bodyB), "alice", "session B should not see session A's data")
}

func TestGetSess_worksWithHTTPRequest(t *testing.T) {
	t.Parallel()

	gotCh := make(chan testUser, 1)
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.HandleFunc("POST /set-user", func(w http.ResponseWriter, r *http.Request) {
		via.SetSess(w, r, testUser{Name: "carol"})
	})

	v.HandleFunc("GET /check-user", func(w http.ResponseWriter, r *http.Request) {
		gotCh <- via.GetSess[testUser](r)
	})

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	// Get session
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	jar := collectCookies(t, server.URL, resp.Cookies())

	// Set user
	req, _ := http.NewRequest("POST", server.URL+"/set-user", nil)
	addCookies(req, jar)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	jar = mergeCookies(jar, resp2.Cookies())

	// Read via *http.Request in another handler
	req2, _ := http.NewRequest("GET", server.URL+"/check-user", nil)
	addCookies(req2, jar)
	resp3, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	resp3.Body.Close()

	select {
	case user := <-gotCh:
		assert.Equal(t, "carol", user.Name)
	case <-time.After(sseTimeout):
		require.Fail(t, "GetSess did not read session data from *http.Request")
	}
}

func TestSession_sweepsExpiredSessions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server), via.WithSessionTTL(100*time.Millisecond))

	v.HandleFunc("POST /set-user", func(w http.ResponseWriter, r *http.Request) {
		via.SetSess(w, r, testUser{Name: "ephemeral"})
	})

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			user := via.GetSess[testUser](ctx)
			return h.Div(h.Text(user.Name))
		})
	})
	t.Cleanup(server.Close)

	// Get session cookie
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	jar := collectCookies(t, server.URL, resp.Cookies())

	// Set session data
	req, _ := http.NewRequest("POST", server.URL+"/set-user", nil)
	addCookies(req, jar)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	jar = mergeCookies(jar, resp2.Cookies())

	// Wait for sweep (interval = TTL/2 = 50ms; need TTL + interval = 150ms)
	time.Sleep(200 * time.Millisecond)

	// Revisit — session should have been swept, data gone
	req2, _ := http.NewRequest("GET", server.URL+"/", nil)
	addCookies(req2, jar)
	resp3, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	body, err := io.ReadAll(resp3.Body)
	require.NoError(t, err)
	resp3.Body.Close()

	assert.NotContains(t, string(body), "ephemeral", "session data should be swept after TTL expires")
}

func TestSession_refreshesTTLOnAccess(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server), via.WithSessionTTL(150*time.Millisecond))

	v.HandleFunc("POST /set-user", func(w http.ResponseWriter, r *http.Request) {
		via.SetSess(w, r, testUser{Name: r.URL.Query().Get("name")})
	})

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			user := via.GetSess[testUser](ctx)
			return h.Div(h.Text(user.Name))
		})
	})
	t.Cleanup(server.Close)

	// Session A: will be kept alive with regular access
	respA, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	respA.Body.Close()
	jarA := collectCookies(t, server.URL, respA.Cookies())

	reqA, _ := http.NewRequest("POST", server.URL+"/set-user?name=alive", nil)
	addCookies(reqA, jarA)
	respA2, err := http.DefaultClient.Do(reqA)
	require.NoError(t, err)
	respA2.Body.Close()
	jarA = mergeCookies(jarA, respA2.Cookies())

	// Session B: will be abandoned after setup
	respB, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	respB.Body.Close()
	jarB := collectCookies(t, server.URL, respB.Cookies())

	reqB, _ := http.NewRequest("POST", server.URL+"/set-user?name=abandoned", nil)
	addCookies(reqB, jarB)
	respB2, err := http.DefaultClient.Do(reqB)
	require.NoError(t, err)
	respB2.Body.Close()
	jarB = mergeCookies(jarB, respB2.Cookies())

	// Keep session A alive every 50ms for 300ms (past the 150ms TTL)
	for range 6 {
		time.Sleep(50 * time.Millisecond)
		req, _ := http.NewRequest("GET", server.URL+"/", nil)
		addCookies(req, jarA)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		jarA = mergeCookies(jarA, resp.Cookies())
	}

	// Session A should still have its data
	reqA3, _ := http.NewRequest("GET", server.URL+"/", nil)
	addCookies(reqA3, jarA)
	respA3, err := http.DefaultClient.Do(reqA3)
	require.NoError(t, err)
	bodyA, err := io.ReadAll(respA3.Body)
	require.NoError(t, err)
	respA3.Body.Close()
	assert.Contains(t, string(bodyA), "alive", "regularly accessed session should survive past TTL")

	// Session B should have been swept (abandoned for 300ms, TTL is 150ms)
	reqB3, _ := http.NewRequest("GET", server.URL+"/", nil)
	addCookies(reqB3, jarB)
	respB3, err := http.DefaultClient.Do(reqB3)
	require.NoError(t, err)
	bodyB, err := io.ReadAll(respB3.Body)
	require.NoError(t, err)
	respB3.Body.Close()
	assert.NotContains(t, string(bodyB), "abandoned", "abandoned session should be swept after TTL expires")
}

func TestSession_zeroTTLDisablesSweep(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server), via.WithSessionTTL(0))

	v.HandleFunc("POST /set-user", func(w http.ResponseWriter, r *http.Request) {
		via.SetSess(w, r, testUser{Name: "forever"})
	})

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			user := via.GetSess[testUser](ctx)
			return h.Div(h.Text(user.Name))
		})
	})
	t.Cleanup(server.Close)

	// Get session and set data
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	jar := collectCookies(t, server.URL, resp.Cookies())

	req, _ := http.NewRequest("POST", server.URL+"/set-user", nil)
	addCookies(req, jar)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	jar = mergeCookies(jar, resp2.Cookies())

	// Wait a while
	time.Sleep(200 * time.Millisecond)

	// Data should still be there — no sweep with TTL=0
	req2, _ := http.NewRequest("GET", server.URL+"/", nil)
	addCookies(req2, jar)
	resp3, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	body, err := io.ReadAll(resp3.Body)
	require.NoError(t, err)
	resp3.Body.Close()

	assert.Contains(t, string(body), "forever", "session should never expire with TTL=0")
}

func TestSess_cookieIsNotSecureByDefault(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	var sessCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "via_session" {
			sessCookie = c
		}
	}
	require.NotNil(t, sessCookie)
	assert.False(t, sessCookie.Secure, "session cookie should not be Secure without WithSecureCookies")
}

func TestSess_cookieIsSecureWithOption(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server), via.WithSecureCookies())
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	var sessCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "via_session" {
			sessCookie = c
		}
	}
	require.NotNil(t, sessCookie)
	assert.True(t, sessCookie.Secure, "session cookie should be Secure with WithSecureCookies")
}

func TestClearSess_cookieIsSecureWithOption(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server), via.WithSecureCookies())

	v.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		via.ClearSess(w, r)
	})

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	jar := collectCookies(t, server.URL, resp.Cookies())

	req, _ := http.NewRequest("POST", server.URL+"/logout", nil)
	addCookies(req, jar)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()

	var cleared *http.Cookie
	for _, c := range resp2.Cookies() {
		if c.Name == "via_session" {
			cleared = c
		}
	}
	require.NotNil(t, cleared)
	assert.True(t, cleared.Secure, "clearing cookie should also be Secure to match original attributes")
}

func TestContext_sweepsIdleContexts(t *testing.T) {
	t.Parallel()

	disposeCh := make(chan struct{}, 1)
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server), via.WithContextTTL(100*time.Millisecond))
	v.Page("/", func(cmp *via.Cmp) {
		cmp.Dispose(func() { disposeCh <- struct{}{} })
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	// Render page to create a ctx, but never open SSE and never fire any
	// action — ctx sits idle in the registry.
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	// Wait for sweep: TTL + interval (TTL/2) + slack
	select {
	case <-disposeCh:
	case <-time.After(sseTimeout):
		require.Fail(t, "idle ctx should have been swept and disposed")
	}
}

func TestContext_activeSSEExtendsTTL(t *testing.T) {
	t.Parallel()

	disposeCh := make(chan struct{}, 1)
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server), via.WithContextTTL(300*time.Millisecond))
	v.Page("/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error { return nil })
		cmp.Dispose(func() { disposeCh <- struct{}{} })
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Button(h.Text("go"), act.OnClick())
		})
	})
	t.Cleanup(server.Close)

	tc := newTestClientFromServer(t, server, "/")
	tc.connect()

	// Fire actions every 100ms for 450ms to keep ctx alive past TTL.
	for range 5 {
		time.Sleep(90 * time.Millisecond)
		tc.fire(tc.actionID())
	}

	select {
	case <-disposeCh:
		require.Fail(t, "active ctx should not have been swept")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestContext_zeroTTLDisablesSweep(t *testing.T) {
	t.Parallel()

	disposeCh := make(chan struct{}, 1)
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server), via.WithContextTTL(0))
	v.Page("/", func(cmp *via.Cmp) {
		cmp.Dispose(func() { disposeCh <- struct{}{} })
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	select {
	case <-disposeCh:
		require.Fail(t, "ctx must not be swept when TTL is 0")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSSE_sendsHeartbeatAtInterval(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server), via.WithSSEHeartbeat(50*time.Millisecond))
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	tc := newTestClientFromServer(t, server, "/").connect()

	// Expect at least one heartbeat within ~200ms (3+ ticks at 50ms)
	deadline := time.Now().Add(500 * time.Millisecond)
	var gotHeartbeat bool
	for time.Now().Before(deadline) {
		ok, ev := tc.tryReadEvent(150 * time.Millisecond)
		if !ok {
			break
		}
		if ev.eventType == "datastar-patch-signals" && strings.Contains(ev.data, "{}") {
			gotHeartbeat = true
			break
		}
	}
	assert.True(t, gotHeartbeat, "expected at least one heartbeat signal-patch event")
}

func TestSSE_zeroHeartbeatDisablesIt(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server), via.WithSSEHeartbeat(0))
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	tc := newTestClientFromServer(t, server, "/").connect()

	ok, _ := tc.tryReadEvent(300 * time.Millisecond)
	assert.False(t, ok, "no events expected when heartbeat is disabled")
}

func TestSSE_heartbeatTouchesContextLastAccess(t *testing.T) {
	t.Parallel()

	disposeCh := make(chan struct{}, 1)
	var server *httptest.Server
	v := via.New(
		via.WithTestServer(&server),
		via.WithSSEHeartbeat(50*time.Millisecond),
		via.WithContextTTL(200*time.Millisecond),
	)
	v.Page("/", func(cmp *via.Cmp) {
		cmp.Dispose(func() { disposeCh <- struct{}{} })
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	tc := newTestClientFromServer(t, server, "/").connect()
	_ = tc

	// Heartbeats should keep lastAccess fresh past the TTL window.
	select {
	case <-disposeCh:
		require.Fail(t, "ctx disposed despite heartbeats refreshing lastAccess")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestPatchQueue_concatenatesMultipleScriptsIntoOneEvent(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			ctx.ExecScript("console.log('a')")
			ctx.ExecScript("console.log('b')")
			ctx.ExecScript("console.log('c')")
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Button(h.Text("go"), act.OnClick())
		})
	})

	tc := newTestClientFromServer(t, server, "/").connect()
	tc.fire(tc.actionID())

	ev := tc.readEvent()
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "console.log('a')")
	assert.Contains(t, ev.data, "console.log('b')")
	assert.Contains(t, ev.data, "console.log('c')")
	idxA := strings.Index(ev.data, "console.log('a')")
	idxB := strings.Index(ev.data, "console.log('b')")
	idxC := strings.Index(ev.data, "console.log('c')")
	assert.Less(t, idxA, idxB, "script order must be preserved")
	assert.Less(t, idxB, idxC, "script order must be preserved")
	assert.Contains(t, ev.data, "try{", "each script must be wrapped in try/catch")

	// No further event should arrive for this action's scripts
	ok, _ := tc.tryReadEvent(200 * time.Millisecond)
	assert.False(t, ok, "scripts should be concatenated into exactly one event")
}

func TestPatchQueue_redirectPreemptsOtherPatches(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "original")
		act := cmp.Action(func(ctx *via.Ctx) error {
			sig.SetValue(ctx, "changed")
			ctx.Redirect("/dashboard")
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("val=%s", sig.Get(ctx)), act.OnClick())
		})
	})

	tc := newTestClientFromServer(t, server, "/").connect()
	tc.fire(tc.actionID())

	ev := tc.readEvent()
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "location", "redirect must use script-based location change")

	ok, other := tc.tryReadEvent(200 * time.Millisecond)
	assert.False(t, ok, "redirect must preempt subsequent element/signal patches, got %q", other.eventType)
}

func TestSess_sessionIDHas256BitsOfEntropy(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	var sessCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "via_session" {
			sessCookie = c
		}
	}
	require.NotNil(t, sessCookie)
	assert.Len(t, sessCookie.Value, 64, "session ID should be 64 hex chars (256 bits)")
}

func TestAction_rejectsMismatchedSession(t *testing.T) {
	t.Parallel()

	fired := make(chan struct{}, 1)
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Page("/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			fired <- struct{}{}
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Button(h.Text("go"), act.OnClick())
		})
	})
	t.Cleanup(server.Close)

	// Session A: render page, get cookies + tab ID + action ID
	respA, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	rawA, _ := io.ReadAll(respA.Body)
	respA.Body.Close()
	bodyA := html.UnescapeString(string(rawA))
	cookiesA := respA.Cookies()
	tabA := extractCtxID(t, string(bodyA))
	actionA := extractActionID(t, string(bodyA))

	// Session B: fresh session, tries to fire session A's action using A's tab ID
	respB, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	respB.Body.Close()
	cookiesB := respB.Cookies()
	require.NotEqual(t, cookiesA[0].Value, cookiesB[0].Value, "sessions must differ")

	triggerActionWithCookies(t, server.URL, tabA, actionA, cookiesB)

	select {
	case <-fired:
		require.Fail(t, "action fired despite session mismatch")
	case <-time.After(200 * time.Millisecond):
	}

	// Session A should still be able to fire its own action
	triggerActionWithCookies(t, server.URL, tabA, actionA, cookiesA)
	select {
	case <-fired:
	case <-time.After(sseTimeout):
		require.Fail(t, "owning session could not fire action")
	}
}

func TestSSE_rejectsMismatchedSession(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	t.Cleanup(server.Close)

	respA, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	rawA, _ := io.ReadAll(respA.Body)
	respA.Body.Close()
	bodyA := html.UnescapeString(string(rawA))
	tabA := extractCtxID(t, string(bodyA))

	// Fresh session B tries to open SSE for tab A
	respB, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	respB.Body.Close()
	cookiesB := respB.Cookies()

	sigsJSON := `{"via_tab":"` + tabA + `"}`
	req, _ := http.NewRequest("GET", server.URL+"/_sse?datastar="+sigsJSON, nil)
	for _, c := range cookiesB {
		req.AddCookie(c)
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.NotEqual(t, "text/event-stream", resp.Header.Get("Content-Type"),
		"SSE stream should not start on session mismatch")
}

func TestSSEClose_rejectsMismatchedSession(t *testing.T) {
	t.Parallel()

	fired := make(chan struct{}, 1)
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Page("/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			fired <- struct{}{}
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Button(h.Text("go"), act.OnClick())
		})
	})
	t.Cleanup(server.Close)

	respA, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	rawA, _ := io.ReadAll(respA.Body)
	respA.Body.Close()
	bodyA := html.UnescapeString(string(rawA))
	cookiesA := respA.Cookies()
	tabA := extractCtxID(t, string(bodyA))
	actionA := extractActionID(t, string(bodyA))

	respB, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	respB.Body.Close()
	cookiesB := respB.Cookies()

	// Session B attempts to dispose session A's ctx
	reqClose, _ := http.NewRequest("POST", server.URL+"/_sse/close", strings.NewReader(tabA))
	for _, c := range cookiesB {
		reqClose.AddCookie(c)
	}
	respClose, err := http.DefaultClient.Do(reqClose)
	require.NoError(t, err)
	respClose.Body.Close()

	// Session A's ctx should still be alive: action still fires
	triggerActionWithCookies(t, server.URL, tabA, actionA, cookiesA)
	select {
	case <-fired:
	case <-time.After(sseTimeout):
		require.Fail(t, "ctx was disposed by a different session")
	}
}

func TestSess_sessionIDsAreUnique(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})

	seen := make(map[string]struct{}, 50)
	for range 50 {
		resp, err := http.Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
		for _, c := range resp.Cookies() {
			if c.Name == "via_session" {
				_, dup := seen[c.Value]
				assert.False(t, dup, "duplicate session ID: %s", c.Value)
				seen[c.Value] = struct{}{}
			}
		}
	}
}

func TestSetSess_preservesMultipleTypesInSameSession(t *testing.T) {
	t.Parallel()

	type theme string
	type locale string

	type result struct {
		th theme
		lo locale
	}
	resultCh := make(chan result, 1)

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.HandleFunc("POST /set", func(w http.ResponseWriter, r *http.Request) {
		via.SetSess(w, r, theme("dark"))
		via.SetSess(w, r, locale("en"))
	})

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			th := via.GetSess[theme](ctx)
			lo := via.GetSess[locale](ctx)
			if th != "" || lo != "" {
				resultCh <- result{th, lo}
			}
			return h.Div()
		})
	})
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	jar := collectCookies(t, server.URL, resp.Cookies())

	req, _ := http.NewRequest("POST", server.URL+"/set", nil)
	addCookies(req, jar)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	jar = mergeCookies(jar, resp2.Cookies())

	req2, _ := http.NewRequest("GET", server.URL+"/", nil)
	addCookies(req2, jar)
	resp3, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	resp3.Body.Close()

	select {
	case r := <-resultCh:
		assert.Equal(t, theme("dark"), r.th)
		assert.Equal(t, locale("en"), r.lo)
	case <-time.After(sseTimeout):
		require.Fail(t, "view did not read multiple session types")
	}
}

