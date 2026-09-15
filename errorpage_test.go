package via_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type errOK struct{}

func (errOK) View() h.H { return h.Div(h.Str("ok")) }

type errMissing struct{}

func (errMissing) OnInit(*via.Ctx) error { return via.ErrNotFound }
func (errMissing) View() h.H             { return h.Div(h.Str("never")) }

type errBoom struct{}

func (errBoom) View() h.H { panic("boom in View") }

type errAsset struct{}

func (errAsset) PageMeta() via.Meta {
	return via.Meta{Assets: via.Assets{Styles: []via.Style{{Href: "https://page.example/p.css"}}}}
}
func (errAsset) View() h.H { return h.Div(h.Str("asset")) }

func errPage(ctx *via.Ctx, e via.PageError) h.H {
	return h.Div(
		h.H1(h.Str("sorry")),
		h.P(h.Str(string(e.Reason))),
		h.P(h.Str(e.Detail)),
	)
}

func errGet(t *testing.T, r *via.Router, path string) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	return resp, b.String()
}

func TestErrorPage_rendersOnMuxMiss(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithErrorPage(errPage))
	r.Mount("/", errOK{})
	t.Cleanup(r.Close)

	resp, body := errGet(t, r, "/nowhere")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Contains(t, body, "<h1>sorry</h1>")
	assert.Contains(t, body, string(via.ReasonNotFound))
	assert.Contains(t, body, "<!doctype html>")
	assert.Contains(t, body, "<title>404 Not Found</title>")
}

func TestErrorPage_rendersOnErrNotFoundFromOnInit(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithErrorPage(errPage))
	r.Mount("/gone", errMissing{})
	t.Cleanup(r.Close)

	resp, body := errGet(t, r, "/gone")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, body, "<h1>sorry</h1>")
	assert.Contains(t, body, "not found")
}

func TestErrorPage_rendersOnRenderPanic(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithErrorPage(errPage))
	r.Mount("/boom", errBoom{})
	t.Cleanup(r.Close)

	resp, body := errGet(t, r, "/boom")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Contains(t, body, "<h1>sorry</h1>")
	assert.Contains(t, body, string(via.ReasonInternal))
	assert.Contains(t, body, "render failed")
}

func TestErrorPage_carriesUnderlyingError(t *testing.T) {
	t.Parallel()
	var got via.PageError
	r := via.NewRouter(via.WithErrorPage(func(_ *via.Ctx, e via.PageError) h.H {
		got = e
		return h.Div(h.Str("x"))
	}))
	r.Mount("/gone", errMissing{})
	t.Cleanup(r.Close)

	errGet(t, r, "/gone")
	require.Error(t, got.Err)
	assert.ErrorIs(t, got.Err, via.ErrNotFound)
	assert.Equal(t, http.StatusNotFound, got.Status)
	assert.Equal(t, via.ReasonNotFound, got.Reason)
}

func TestErrorPage_seesTheRequest(t *testing.T) {
	t.Parallel()
	path := ""
	r := via.NewRouter(via.WithErrorPage(func(ctx *via.Ctx, _ via.PageError) h.H {
		path = ctx.Request().URL.Path
		return h.Div(h.Str("x"))
	}))
	r.Mount("/", errOK{})
	t.Cleanup(r.Close)

	errGet(t, r, "/nowhere")
	assert.Equal(t, "/nowhere", path)
}

func TestErrorPage_skipsDatastarActionResponse(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithErrorPage(errPage))
	r.Mount("/", errOK{})
	t.Cleanup(r.Close)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/_via/a/r/nope", strings.NewReader("{}"))
	req.Header.Set("Datastar-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)

	assert.Equal(t, http.StatusGone, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")
	assert.NotContains(t, b.String(), "sorry")
	assert.Contains(t, b.String(), "does not bind it")
}

func TestErrorPage_rendersOnNativeFormFailure(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithErrorPage(errPage))
	r.Mount("/", errOK{})
	t.Cleanup(r.Close)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// A native submit carries no Datastar-Request header; a non-multipart body
	// is the shortest way to fail one, and the browser renders the answer.
	resp, err := http.Post(srv.URL+"/_via/a/r/nope", "application/x-www-form-urlencoded", strings.NewReader("a=1"))
	require.NoError(t, err)
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, b.String(), "<h1>sorry</h1>")
	assert.Contains(t, b.String(), string(via.ReasonBadRequest))
}

func TestErrorPage_fallsBackWhenHandlerPanics(t *testing.T) {
	r := via.NewRouter(via.WithErrorPage(func(*via.Ctx, via.PageError) h.H { panic("handler boom") }))
	r.Mount("/", errOK{})
	t.Cleanup(r.Close)

	var resp *http.Response
	var body string
	logs := captureLog(t, func() {
		resp, body = errGet(t, r, "/nowhere")
		errGet(t, r, "/nowhere-else")
	})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")
	assert.Contains(t, body, "404 page not found")
	assert.Equal(t, 1, strings.Count(logs, "handler panicked"))
}

func TestErrorPage_fallsBackWhenHandlerReturnsNil(t *testing.T) {
	r := via.NewRouter(via.WithErrorPage(func(*via.Ctx, via.PageError) h.H { return nil }))
	r.Mount("/gone", errMissing{})
	t.Cleanup(r.Close)

	var resp *http.Response
	var body string
	logs := captureLog(t, func() {
		resp, body = errGet(t, r, "/gone")
		errGet(t, r, "/gone")
	})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")
	assert.Equal(t, "not found\n", body)
	assert.Equal(t, 1, strings.Count(logs, "returned nil"))
}

func TestErrorPage_cannotWidenAMountCSP(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(
		via.WithErrorPage(errPage),
		via.WithHead(via.Head{Assets: via.Assets{Styles: []via.Style{{Href: "https://global.example/g.css"}}}}),
	)
	r.Mount("/asset", errAsset{})
	t.Cleanup(r.Close)

	ok, _ := errGet(t, r, "/asset")
	require.Equal(t, http.StatusOK, ok.StatusCode)
	require.Contains(t, ok.Header.Get("Content-Security-Policy"), "https://page.example")

	bad, _ := errGet(t, r, "/nowhere")
	csp := bad.Header.Get("Content-Security-Policy")
	assert.NotContains(t, csp, "https://page.example")
	assert.Contains(t, csp, "https://global.example")
}

func TestErrorPage_absentKeepsPlainText(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	r.Mount("/gone", errMissing{})
	t.Cleanup(r.Close)

	resp, body := errGet(t, r, "/gone")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")
	assert.Equal(t, "not found\n", body)
}

func TestWithErrorPage_panicsOnNil(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { via.NewRouter(via.WithErrorPage(nil)) })
}

func TestWithErrorPage_panicsOnConflict(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { via.NewRouter(via.WithErrorPage(errPage), via.WithErrorPage(errPage)) })
}

type errParams struct{}

func (errParams) OnInit(ctx *via.Ctx) error { ctx.Param[int]("id"); return nil }
func (errParams) View() h.H                 { return h.Div(h.Str("params")) }

type errLive struct {
	n via.State[int]
}

func (l *errLive) Bump(*via.Ctx) { l.n.Set(l.n.Get() + 1) }

func (l *errLive) View() h.H {
	return h.Div(h.Button(via.On("click", l.Bump), h.Str("+")), l.n.Display())
}

// nativePost submits a multipart body with no Datastar-Request header — a real
// <form> navigation, which is the transport an error page covers.
func nativePost(t *testing.T, c *http.Client, url string, fields map[string]string) (*http.Response, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	require.NoError(t, mw.Close())
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	return resp, b.String()
}

func TestErrorPage_paramReturnsZeroInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	got := "unset"
	n := -1
	r := via.NewRouter(via.WithErrorPage(func(ctx *via.Ctx, _ via.PageError) h.H {
		got = ctx.Param[string]("id")
		n = ctx.Param[int]("id")
		return h.Div(h.Str("x"))
	}))
	r.Mount("/thread/{id}", errParams{})
	t.Cleanup(r.Close)

	resp, body := errGet(t, r, "/nowhere")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, body, "<div>x</div>",
		"Param must not be able to crash the one render that answers a failure")
	assert.Equal(t, "", got)
	assert.Equal(t, 0, n)
}

func TestErrorPage_paramReturnsZeroOnAnUndecodableSegment(t *testing.T) {
	t.Parallel()
	n := -1
	r := via.NewRouter(via.WithErrorPage(func(ctx *via.Ctx, _ via.PageError) h.H {
		n = ctx.Param[int]("id")
		return h.Div(h.Str("x"))
	}))
	r.Mount("/thread/{id}", errParams{})
	t.Cleanup(r.Close)

	resp, body := errGet(t, r, "/thread/abc")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, body, "<div>x</div>")
	assert.Equal(t, 0, n)
}

func TestErrorPage_sessionWriteNamesTheErrorPageAsTheCause(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	r := via.NewRouter(via.WithErrorPage(func(ctx *via.Ctx, _ via.PageError) h.H {
		ctx.Session().Put("nope")
		return h.Div(h.Str("x"))
	}))
	r.Mount("/", errOK{})
	t.Cleanup(r.Close)

	errGet(t, r, "/nowhere")
	assert.Contains(t, logs.String(), "WithErrorPage handler")
	assert.NotContains(t, logs.String(), "Tick or Listen handler",
		"an error page is not a Tick handler — the old message sent readers hunting the wrong cause")
}

func TestErrorPage_reportsMethodNotAllowed(t *testing.T) {
	t.Parallel()
	var got via.PageError
	r := via.NewRouter(via.WithErrorPage(func(_ *via.Ctx, e via.PageError) h.H {
		got = e
		return h.Div(h.Str("x"))
	}))
	r.Mount("/", errOK{})
	t.Cleanup(r.Close)

	resp, _ := errGet(t, r, "/_via/a/r/0")
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Equal(t, via.ReasonMethodNotAllowed, got.Reason,
		"a GET of an action URL is a wrong method, not a malformed request")
}

func TestErrorPage_carriesErrStaleTabOnAnActionWithNoStream(t *testing.T) {
	t.Parallel()
	var got via.PageError
	r := via.NewRouter(via.WithErrorPage(func(_ *via.Ctx, e via.PageError) h.H {
		got = e
		return h.Div(h.Str("x"))
	}))
	r.Mount("/", errLive{})
	t.Cleanup(r.Close)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	page := jarGet(t, srv.Client(), srv.URL+"/")
	resp, _ := nativePost(t, srv.Client(), srv.URL+actionURL(t, page, "r", 0), nil)

	require.Equal(t, http.StatusGone, resp.StatusCode)
	assert.Equal(t, via.ReasonGone, got.Reason)
	assert.ErrorIs(t, got.Err, via.ErrStaleTab,
		"a reload fixes a stale tab and fixes nothing else — that is the distinction worth a sentinel")
}

func TestErrorPage_rendersOnAnOversizeNativeSubmit(t *testing.T) {
	t.Parallel()
	var got via.PageError
	r := via.NewRouter(via.WithErrorPage(func(_ *via.Ctx, e via.PageError) h.H {
		got = e
		return h.Div(h.Str("too big"))
	}))
	r.Mount("/", errOK{})
	t.Cleanup(r.Close)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("f", "big.bin")
	require.NoError(t, err)
	fw.Write(make([]byte, 9<<20))
	require.NoError(t, mw.Close())
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_via/a/r/0", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out bytes.Buffer
	out.ReadFrom(resp.Body)

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.Equal(t, via.ReasonTooLarge, got.Reason)
	assert.Contains(t, out.String(), "too big")
}

// flakyStore is a real store that can be switched off mid-test, the way a
// Redis a pod has just lost its connection to behaves.
type flakyStore struct {
	*sharedStore
	down atomic.Bool
}

func (f *flakyStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	if f.down.Load() {
		return nil, false, errors.New("store: connection refused")
	}
	return f.sharedStore.Load(ctx, id)
}

type errLiveSess struct {
	n via.State[int]
}

func (l *errLiveSess) OnInit(ctx *via.Ctx) error { ctx.Session().Put("u1"); return nil }
func (l *errLiveSess) Bump(*via.Ctx)             { l.n.Set(l.n.Get() + 1) }

func (l *errLiveSess) View() h.H {
	return h.Div(h.Button(via.On("click", l.Bump), h.Str("+")), l.n.Display())
}

func TestErrorPage_carriesErrStoreDownWhenTheStoreCannotAnswer(t *testing.T) {
	t.Parallel()
	store := &flakyStore{sharedStore: newSharedStore()}
	var got via.PageError
	r := via.NewRouter(
		via.WithErrorPage(func(_ *via.Ctx, e via.PageError) h.H {
			got = e
			return h.Div(h.Str("x"))
		}),
		via.WithSessionStore(store),
		via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")),
	)
	r.Mount("/", errLiveSess{})
	t.Cleanup(r.Close)

	app := vt.Serve(t, r)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	app.Client().Jar = jar

	// The session must exist BEFORE the connect: a stream binds the identity the
	// browser held when it opened, and an unbound stream skips the store read
	// this test is about.
	app.Get("/")
	conn := app.Connect()
	store.down.Store(true)

	resp, body := nativePost(t, app.Client(), app.URL()+conn.ActionURL("r", 0),
		map[string]string{"_viatab": conn.TabID()})

	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, via.ReasonUnavailable, got.Reason)
	assert.ErrorIs(t, got.Err, via.ErrStoreDown,
		"a dependency outage wants a status page; the other 503s want a retry prompt")
	assert.Contains(t, body, "<div>x</div>")
}

// gatePage's OnInit sends every visitor away. The error-page wrapper sits in
// front of it on a plain GET (only Datastar POSTs and the SSE route are
// exempt), so this is what proves the wrapper passes a non-error response
// through instead of holding it: a login gate that got swallowed would strand
// every unauthenticated visitor on a blank 200.
type gatePage struct{}

func (p *gatePage) OnInit(ctx *via.Ctx) error { ctx.Redirect("/login"); return nil }
func (p *gatePage) View() h.H                 { return h.Div(h.Str("secret")) }

func TestErrorPage_passesARedirectThroughUntouched(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithErrorPage(errPage))
	r.Mount("/secret", gatePage{})
	t.Cleanup(r.Close)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.Get(srv.URL + "/secret")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode, "the wrapper caught a redirect it must pass through")
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}
