package content_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demos"
)

type browser struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	page   string
}

func (b *browser) load() {
	b.t.Helper()
	resp, err := b.client.Get(b.srv.URL + "/")
	require.NoError(b.t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(b.t, http.StatusOK, resp.StatusCode)
	b.page = string(body)
}

// post fires the first action the loaded page bound for child, the way a click
// on markup that has not been re-rendered would.
func (b *browser) post(child string) int {
	b.t.Helper()
	m := regexp.MustCompile(`@post\('([^']*_via/a/` + child + `/[A-Za-z0-9_-]+)'`).FindStringSubmatch(b.page)
	require.NotNil(b.t, m, "no action for child %s", child)
	req, err := http.NewRequest(http.MethodPost, b.srv.URL+m[1], strings.NewReader("{}"))
	require.NoError(b.t, err)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := b.client.Do(req)
	require.NoError(b.t, err)
	resp.Body.Close()
	return resp.StatusCode
}

func newBrowser(t *testing.T) *browser {
	t.Helper()
	r := via.Handler(demos.NewShiftPair())
	t.Cleanup(r.Close)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &browser{t: t, srv: srv, client: &http.Client{Jar: jar}}
}

func TestShiftBroken_answersGoneOnceTheBannerIsDismissed(t *testing.T) {
	t.Parallel()
	b := newBrowser(t)
	b.load()
	assert.Less(t, b.post("0-1"), 300, "the clock works before the dismiss")
	assert.Less(t, b.post("0-0"), 300, "dismiss")
	assert.Equal(t, http.StatusGone, b.post("0-1"), "the clock's key moved to 0-0")
}

func TestShiftFixed_keepsTheClockAfterTheDismiss(t *testing.T) {
	t.Parallel()
	b := newBrowser(t)
	b.load()
	assert.Less(t, b.post("1-0"), 300, "dismiss")
	assert.Less(t, b.post("1-1"), 300, "the clock kept its key")
}

func TestShiftBroken_restoreBringsTheBannerBack(t *testing.T) {
	t.Parallel()
	b := newBrowser(t)
	b.load()
	require.Less(t, b.post("0-0"), 300)
	b.load()
	assert.Equal(t, 1, strings.Count(b.page, "A banner above the clock."), "only the fixed banner")
	require.Less(t, b.post("0"), 300)
	b.load()
	assert.Equal(t, 2, strings.Count(b.page, "A banner above the clock."))
}

func TestHookLog_logsBothInitsAndTheConnect(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(demos.HookLog{}))
	status, body := app.Get("/")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "OnInit     GET /")
	conn := app.Connect()
	frame := conn.Await("OnConnect")
	assert.Contains(t, frame, "OnInit     POST /_via/sse")

	status, _ = app.Action(0).Over(conn).Fire()
	require.Equal(t, http.StatusNoContent, status)
	conn.Await("OnReload")
}

func TestRelay_carriesAPickToTheSibling(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(demos.Relay{}))
	conn := app.Connect()
	status, _ := app.ChildAction("0", 1).Over(conn).Fire()
	require.Equal(t, http.StatusNoContent, status)
	conn.Await("last pick: <strong>Rust</strong>")
}
