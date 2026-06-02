package via_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
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

// --- known-tab reconnect ---

func TestSSE_reconnectResyncsViewElements(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[recoverPage](app, "/r")
	defer server.Close()

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

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[recoverPage](app, "/r")
	defer server.Close()

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

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[recoverPage](app, "/r")
	defer server.Close()

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

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[recoverParamPage](app, "/u/{name}")
	defer server.Close()

	stale := "/u/{name}_" + staleSuffix
	status, frames, cancel := openRawSSE(t, jarClient(t), server.URL, stale, server.URL+"/u/alice")
	defer cancel()
	require.Equal(t, http.StatusOK, status)
	vt.AwaitFrame(t, frames, 2*time.Second, "hello alice")
}

func TestSSE_unknownTabParamRouteWithoutRefererFallsBackToReload(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[recoverParamPage](app, "/u/{name}")
	defer server.Close()

	stale := "/u/{name}_" + staleSuffix
	status, frames, cancel := openRawSSE(t, jarClient(t), server.URL, stale, "")
	defer cancel()
	require.Equal(t, http.StatusOK, status,
		"unrecoverable params must degrade to an explicit reload, not 404")
	vt.AwaitFrame(t, frames, 2*time.Second, "window.location.reload()")
}

func TestSSE_unknownTabUnmountedRoutePrefix404s(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[recoverPage](app, "/r")
	defer server.Close()

	status, _, cancel := openRawSSE(t, jarClient(t), server.URL, "/nope_"+staleSuffix, server.URL+"/nope")
	defer cancel()
	assert.Equal(t, http.StatusNotFound, status,
		"a tab id whose route prefix was never mounted is forged — keep the 404")
}

func TestSSE_unknownTabMalformedID404s(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[recoverPage](app, "/r")
	defer server.Close()

	for _, id := range []string{"", "garbage", "/r_nothex", "/r_" + staleSuffix[:10]} {
		status, _, cancel := openRawSSE(t, jarClient(t), server.URL, id, server.URL+"/r")
		cancel()
		assert.Equal(t, http.StatusNotFound, status, "id=%q", id)
	}
}
