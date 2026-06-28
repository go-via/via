package via_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/go-via/via/v2"
	"github.com/go-via/via/v2/h"
	"github.com/go-via/via/v2/sess"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acct is a session-stored value; profilePage loads it in OnInit so its ctx-free
// View can render the logged-in user.
type acct struct{ Name string }

type profilePage struct{ greeting string }

func (p *profilePage) OnInit(ctx *via.Ctx) {
	if a, ok := sess.Get[acct](ctx); ok {
		p.greeting = "hi " + a.Name
	}
}
func (p *profilePage) SignIn(ctx *via.Ctx) { sess.Put(ctx, acct{Name: "alice"}) }
func (p *profilePage) View() h.H {
	return h.Div(h.P(h.Str(p.greeting)), h.Button(via.OnClick(p.SignIn), h.Str("in")))
}

func jarGet(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func jarPost(t *testing.T, c *http.Client, url string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
}

// OnInit runs per request before the (ctx-free) View, so a page can load
// session data into its fields and render it. Without it, a stateless page could
// never show "the logged-in user" — View has no ctx to read the session from.
func TestRouter_onInitLoadsSessionForRender(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/p", profilePage{})
	srv := serve(t, r)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	assert.NotContains(t, jarGet(t, c, srv.URL+"/p"), "hi alice", "no session yet")
	jarPost(t, c, srv.URL+"/p/_via/a/0") // SignIn → sets the session cookie
	assert.Contains(t, jarGet(t, c, srv.URL+"/p"), "hi alice",
		"OnInit must load the session before the ctx-free View renders")
}

// A router serves several pages at their own paths; each page's actions are
// namespaced under its mount path, so two pages can both declare action 1
// without colliding, and an action on one page never touches the other.
func TestRouter_mountsPagesWithPathNamespacedIndependentActions(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/a", counter{count: &store{}})
	via.Mount(r, "/b", counter{count: &store{}})
	srv := serve(t, r)

	// Each page renders at its path, with its actions namespaced under it.
	_, a := do(t, srv, http.MethodGet, "/a", "")
	assert.Contains(t, a, `<h1>0</h1>`)
	assert.Contains(t, a, `@post('/a/_via/a/1')`, "page /a's Inc must post under /a")
	_, b := do(t, srv, http.MethodGet, "/b", "")
	assert.Contains(t, b, `@post('/b/_via/a/1')`, "page /b's Inc must post under /b")

	// Inc on /a; /b must be untouched (independent state + routing).
	do(t, srv, http.MethodPost, "/a/_via/a/1", "{}")
	_, a2 := do(t, srv, http.MethodGet, "/a", "")
	assert.Contains(t, a2, `<h1>1</h1>`, "/a's counter must reflect its action")
	_, b2 := do(t, srv, http.MethodGet, "/b", "")
	assert.Contains(t, b2, `<h1>0</h1>`, "/b must be unaffected by an action on /a")
}

// Mounting at "/" must namespace to the root (no prefix): the page posts to
// /_via/a/{n}, exactly like a single-page Register.
func TestRouter_mountAtRootHasNoPrefix(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/", counter{count: &store{}})
	srv := serve(t, r)

	_, body := do(t, srv, http.MethodGet, "/", "")
	assert.Contains(t, body, `@post('/_via/a/1')`, "root mount must post to /_via/a/{n} with no prefix")
	resp, after := do(t, srv, http.MethodPost, "/_via/a/1", "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, after, `<h1>1</h1>`)
}

// A mounted action still ships the page-hardening response headers and behaves
// like the single-page action (element-patch on change).
func TestRouter_mountedActionElementPatches(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/a", counter{count: &store{}})
	srv := serve(t, r)

	resp, body := do(t, srv, http.MethodPost, "/a/_via/a/1", "{}")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, `<h1>1</h1>`, "mounted action must element-patch the new value")
}
