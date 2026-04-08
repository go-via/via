package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
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

// Cookie helpers for test — manual jar since http.Client default has no jar.

func collectCookies(t *testing.T, _ string, cookies []*http.Cookie) []*http.Cookie {
	t.Helper()
	return cookies
}

func mergeCookies(existing []*http.Cookie, fresh []*http.Cookie) []*http.Cookie {
	merged := make(map[string]*http.Cookie)
	for _, c := range existing {
		merged[c.Name] = c
	}
	for _, c := range fresh {
		merged[c.Name] = c
	}
	out := make([]*http.Cookie, 0, len(merged))
	for _, c := range merged {
		out = append(out, c)
	}
	return out
}

func addCookies(req *http.Request, cookies []*http.Cookie) {
	for _, c := range cookies {
		req.AddCookie(c)
	}
}
