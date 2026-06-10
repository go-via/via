package via_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- pages ---

type recoverPage struct {
	N via.StateTabNum[int]
	Q via.Signal[int]
}

func (p *recoverPage) OnInit(ctx *via.Ctx) error {
	return p.N.Update(ctx, func(int) (int, error) { return 7, nil })
}

func (p *recoverPage) Bump(ctx *via.Ctx) error {
	return p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil })
}

func (p *recoverPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.ID("n"), p.N.Text(ctx))
}

type recoverParamPage struct {
	Name string `path:"name"`
}

func (p *recoverParamPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Textf("hello %s", p.Name))
}

// --- helpers ---

// jarClient returns an http.Client with a cookie jar, so the session the
// SSE handshake mints is carried by follow-up action POSTs (the recovered
// ctx binds the handshake's session; a cookie-less action would 403).
func jarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar, Transport: &http.Transport{}}
}

// openRawSSE opens GET /_sse with the given via_tab and optional Referer,
// returning the response status, a frames channel (nil unless 200), and a
// cancel func.
func openRawSSE(t *testing.T, httpc *http.Client, serverURL, tabID, referer string) (int, <-chan string, func()) {
	t.Helper()
	ctx, cancelF := context.WithCancel(context.Background())
	u := serverURL + "/_sse?datastar=" + url.QueryEscape(`{"via_tab":"`+tabID+`"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	require.NoError(t, err)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := httpc.Do(req)
	require.NoError(t, err)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancelF()
		return resp.StatusCode, nil, func() {}
	}
	out := make(chan string, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				out <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return resp.StatusCode, out, func() { cancelF(); resp.Body.Close() }
}

var staleSuffix = strings.Repeat("ab", 32) // 64 hex chars, valid shape, never issued

var tabSignalRE = regexp.MustCompile(`"via_tab":"([^"]+)"`)

// A session-mismatch 403 (the localhost cookie-clobber freeze and the
// stale-cookie case) must be observable: a bare metric + log line, not a
// silent dead-end the developer can't diagnose. Here a page GET binds a
// session to the ctx, then a cookie-less action on that tab trips the gate.
func TestSession_mismatch403IsMeteredForObservability(t *testing.T) {
	t.Parallel()
	m := &captureMetrics{}
	app := via.New(via.WithMetrics(m))
	server := vt.Serve(t, app)
	via.Mount[recoverPage](app, "/")

	body := getBody(t, server, "/")
	// The page HTML-escapes the data-signals JSON, so match the tab id by its
	// stable shape (/_ + 64 hex) rather than the unescaped "via_tab":"…" form.
	tabID := regexp.MustCompile(`/_[0-9a-f]{64}`).FindString(body)
	require.NotEmpty(t, tabID, "rendered page must carry a via_tab id")

	resp, err := server.Client().Post(server.URL+"/_action/Bump", "application/json",
		strings.NewReader(`{"via_tab":"`+tabID+`"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a cookie-less action on a session-bound tab must 403 (session mismatch)")
	assert.Contains(t, m.counters, "via.session.mismatch:",
		"a session-mismatch 403 must increment via.session.mismatch for diagnosability")
}

// Two via apps on the same host (different ports) share the via_session cookie
// and clobber each other. WithSessionCookieName lets co-located apps pick
// distinct names so each keeps its own session.
func TestWithSessionCookieName_usesTheConfiguredCookieName(t *testing.T) {
	t.Parallel()
	app := via.New(via.WithSessionCookieName("myapp_sess"), via.WithInsecureCookies())
	server := vt.Serve(t, app)
	via.Mount[recoverPage](app, "/")

	httpc := jarClient(t)
	resp, err := httpc.Get(server.URL + "/")
	require.NoError(t, err)
	pageBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var got string
	for _, c := range resp.Cookies() {
		if c.Name == "myapp_sess" {
			got = c.Name
		}
		assert.NotEqual(t, "via_session", c.Name,
			"the default cookie name must not leak when a custom name is set")
	}
	assert.Equal(t, "myapp_sess", got, "session cookie must use the configured name")

	// The renamed cookie must still round-trip: a follow-up action on the same
	// jar carries it, so the session matches and the action is not a 403.
	tabID := regexp.MustCompile(`/_[0-9a-f]{64}`).FindString(string(pageBytes))
	require.NotEmpty(t, tabID)
	ar, err := httpc.Post(server.URL+"/_action/Bump", "application/json",
		strings.NewReader(`{"via_tab":"`+tabID+`"}`))
	require.NoError(t, err)
	defer ar.Body.Close()
	assert.Equal(t, http.StatusOK, ar.StatusCode,
		"the renamed session cookie must round-trip so the action is authorized")
}

func TestWithSessionCookieName_panicsOnEmptyName(t *testing.T) {
	t.Parallel()
	assert.PanicsWithValue(t,
		"via: WithSessionCookieName requires a non-empty name",
		func() { via.WithSessionCookieName("") },
		"an empty cookie name is a programming error and must panic at registration")
}

// nginx and other reverse proxies buffer proxied responses by default, holding
// an SSE stream's frames until the buffer fills — heartbeat and patches never
// reach the browser. The X-Accel-Buffering: no header opts the stream out.
// (datastar's NewSSE sets Cache-Control but not this.)
func TestSSE_setsXAccelBufferingHeaderSoProxiesDontStall(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[recoverPage](app, "/")

	httpc := jarClient(t)
	resp, err := httpc.Get(server.URL + "/")
	require.NoError(t, err)
	pageBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tabID := regexp.MustCompile(`/_[0-9a-f]{64}`).FindString(string(pageBytes))
	require.NotEmpty(t, tabID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := server.URL + "/_sse?datastar=" + url.QueryEscape(`{"via_tab":"`+tabID+`"}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	sse, err := httpc.Do(req)
	require.NoError(t, err)
	defer sse.Body.Close()
	require.Equal(t, http.StatusOK, sse.StatusCode)
	assert.Equal(t, "no", sse.Header.Get("X-Accel-Buffering"),
		"SSE response must disable proxy buffering so frames aren't held by nginx/proxies")
}

type routeProbePage struct{}

func (p *routeProbePage) Act(ctx *via.Ctx) error { return nil }
func (p *routeProbePage) View(ctx *via.CtxR) h.H { return h.Div() }

// A group middleware guarding actions/SSE must see the logical PAGE route, not
// the global "/_action/{id}" / "/_sse" path — otherwise path-based guards and
// per-route policy can't tell which page they're protecting. via.RouteFrom(r)
// exposes the resolved route on all three entry points.
func TestRouteFrom_groupMiddlewareSeesPageRouteNotActionPath(t *testing.T) {
	t.Parallel()
	app := via.New()
	server := vt.Serve(t, app)

	var mu sync.Mutex
	var captured string
	grp := app.Group("/grp")
	grp.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		mu.Lock()
		captured = via.RouteFrom(r)
		mu.Unlock()
		next.ServeHTTP(w, r)
	})
	via.Mount[routeProbePage](grp, "/pg")

	httpc := jarClient(t)
	resp, err := httpc.Get(server.URL + "/grp/pg")
	require.NoError(t, err)
	pageBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tabID := regexp.MustCompile(`via_tab&#34;:&#34;([^&]+)&#34;`).FindStringSubmatch(string(pageBytes))
	require.Len(t, tabID, 2, "page must carry a via_tab")

	ar, err := httpc.Post(server.URL+"/_action/Act", "application/json",
		strings.NewReader(`{"via_tab":"`+tabID[1]+`"}`))
	require.NoError(t, err)
	ar.Body.Close()

	mu.Lock()
	got := captured
	mu.Unlock()
	assert.NotContains(t, got, "_action",
		"RouteFrom on an action request must NOT be the /_action path")
	assert.Equal(t, "/grp/pg", got,
		"RouteFrom must return the resolved page route even on the action POST")
}

// --- known-tab reconnect ---

func TestSSE_reconnectResyncsViewElements(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[recoverPage](app, "/r")

	tc := vt.NewClient(t, server, "/r")
	frames, cancel := tc.SSEReady()
	require.Equal(t, http.StatusOK, tc.Action("Bump").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, ">8<")
	cancel() // drop the stream; queue is fully drained

	// Reconnect with no pending patches: the resync must re-ship the
	// current view so a client that drifted during the gap is corrected.
	// Raw SSE() (not SSEReady) — the resync frame precedes the ready
	// marker, which SSEReady would consume along with everything before it.
	frames2, cancel2 := tc.SSE()
	defer cancel2()
	body := vt.AwaitFrame(t, frames2, 2*time.Second, "datastar-patch-elements", ">8<")

	// View-only resync: signals must NOT be re-seeded on a known-tab
	// reconnect (would clobber live client-side signal state). The only
	// signal traffic allowed here is the empty keepalive payload.
	assert.NotContains(t, body, `"q"`, "reconnect resync must not re-seed signals")
	assert.NotContains(t, body, "via_tab", "reconnect resync must not re-seed via_tab")
}

func TestSSE_firstConnectDoesNotResync(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[recoverPage](app, "/r")

	tc := vt.NewClient(t, server, "/r")
	frames, cancel := tc.SSEReady()
	defer cancel()

	// The page document already carries the full view; the first connect
	// must not redundantly re-ship it.
	select {
	case f, ok := <-frames:
		if ok {
			assert.NotContains(t, f, "datastar-patch-elements",
				"first connect must not re-render the view")
		}
	case <-time.After(150 * time.Millisecond):
	}
}

// --- unknown-tab re-bootstrap ---

func TestSSE_unknownTabRebootstrapsFreshCtx(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[recoverPage](app, "/r")

	httpc := jarClient(t)
	stale := "/r_" + staleSuffix
	status, frames, cancel := openRawSSE(t, httpc, server.URL, stale, server.URL+"/r")
	defer cancel()
	require.Equal(t, http.StatusOK, status,
		"unknown via_tab on a mounted route must re-bootstrap, not 404")

	// Bootstrap must seed a fresh via_tab signal and ship the full view
	// (OnInit ran: N==7), replacing the stale container.
	body := vt.AwaitFrame(t, frames, 2*time.Second,
		"via_tab", "datastar-patch-elements", ">7<")
	m := tabSignalRE.FindStringSubmatch(body)
	require.Len(t, m, 2, "bootstrap must patch a via_tab signal")
	newTab := m[1]
	assert.NotEqual(t, stale, newTab, "re-bootstrap must mint a fresh tab id")
	assert.True(t, strings.HasPrefix(newTab, "/r_"))
	assert.Contains(t, body, stale,
		"element patch must target the stale container id")

	// The recovered tab is live: actions against the new id work and
	// their patches arrive on this same stream.
	fire := func() int {
		resp, err := httpc.Post(server.URL+"/_action/Bump", "application/json",
			strings.NewReader(`{"via_tab":"`+newTab+`"}`))
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(t, http.StatusOK, fire())
	vt.AwaitFrame(t, frames, 2*time.Second, ">8<")
}

func TestSSE_unknownTabRecoversPathParamsFromReferer(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[recoverParamPage](app, "/u/{name}")

	stale := "/u/{name}_" + staleSuffix
	status, frames, cancel := openRawSSE(t, jarClient(t), server.URL, stale, server.URL+"/u/alice")
	defer cancel()
	require.Equal(t, http.StatusOK, status)
	vt.AwaitFrame(t, frames, 2*time.Second, "hello alice")
}

func TestSSE_unknownTabParamRouteWithoutRefererFallsBackToReload(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[recoverParamPage](app, "/u/{name}")

	stale := "/u/{name}_" + staleSuffix
	status, frames, cancel := openRawSSE(t, jarClient(t), server.URL, stale, "")
	defer cancel()
	require.Equal(t, http.StatusOK, status,
		"unrecoverable params must degrade to an explicit reload, not 404")
	vt.AwaitFrame(t, frames, 2*time.Second, "window.location.reload()")
}

func TestSSE_unknownTabUnmountedRoutePrefix404s(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[recoverPage](app, "/r")

	status, _, cancel := openRawSSE(t, jarClient(t), server.URL, "/nope_"+staleSuffix, server.URL+"/nope")
	defer cancel()
	assert.Equal(t, http.StatusNotFound, status,
		"a tab id whose route prefix was never mounted is forged — keep the 404")
}

func TestSSE_unknownTabMalformedID404s(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[recoverPage](app, "/r")

	for _, id := range []string{"", "garbage", "/r_nothex", "/r_" + staleSuffix[:10]} {
		status, _, cancel := openRawSSE(t, jarClient(t), server.URL, id, server.URL+"/r")
		cancel()
		assert.Equal(t, http.StatusNotFound, status, "id=%q", id)
	}
}
