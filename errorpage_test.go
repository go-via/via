package via_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
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
