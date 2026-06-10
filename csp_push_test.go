package via_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cspPushPage struct{}

func (p *cspPushPage) PopToast(ctx *via.Ctx) error {
	ctx.Notify("hi")
	return nil
}

func (p *cspPushPage) GoRedirect(ctx *via.Ctx) error {
	ctx.Redirect("/elsewhere")
	return nil
}

func (p *cspPushPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Text("push"))
}

// pathKeyedNonce gives the page GET ("/") the nonce "pagenonce" and every
// other request (the /_sse handshake, the /_action POST) the nonce
// "reqnonce". A script pushed over SSE is injected into the page document,
// so it must carry the document's nonce ("pagenonce") — not the nonce of
// the SSE or action request that happens to trigger the push.
func pathKeyedNonce() via.Middleware {
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		n := "reqnonce"
		if r.URL.Path == "/" {
			n = "pagenonce"
		}
		w.Header().Set("Content-Security-Policy",
			"script-src 'self' 'nonce-"+n+"'")
		next.ServeHTTP(w, via.RequestWithCSPNonce(r, n))
	}
}

func TestPushedToast_carriesPageNonceNotRequestNonce(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.Use(pathKeyedNonce())
	via.Mount[cspPushPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()
	require.Equal(t, http.StatusOK, tc.Action("PopToast").Fire())

	frame := vt.AwaitFrame(t, frames, 2*time.Second, `nonce="pagenonce"`)
	assert.NotContains(t, frame, `nonce="reqnonce"`,
		"a pushed script must use the page-document nonce, not the SSE/action request nonce")
}

func TestPushedRedirect_carriesPageNonce(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.Use(pathKeyedNonce())
	via.Mount[cspPushPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()
	require.Equal(t, http.StatusOK, tc.Action("GoRedirect").Fire())

	frame := vt.AwaitFrame(t, frames, 2*time.Second, `nonce="pagenonce"`)
	assert.Contains(t, frame, "window.location.href",
		"redirect rides datastar ExecuteScript, which needs the nonce too")
	assert.NotContains(t, frame, `nonce="reqnonce"`)
}

func TestPushedScript_hasNoNonceWithoutCSP(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[cspPushPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()
	require.Equal(t, http.StatusOK, tc.Action("PopToast").Fire())

	frame := vt.AwaitFrame(t, frames, 2*time.Second, "via-toast")
	assert.NotContains(t, frame, "nonce=",
		"with no CSP middleware no nonce is captured, so pushed scripts stay attribute-free")
}

type cspPushNonceViewPage struct{}

func (p *cspPushNonceViewPage) PopToast(ctx *via.Ctx) error {
	ctx.Notify("hi")
	return nil
}

func (p *cspPushNonceViewPage) View(ctx *via.CtxR) h.H {
	// Embedding the nonce lazily generates and caches one even with no CSP
	// middleware; the push path must read the document nonce (still empty),
	// not that minted value.
	return h.Div(h.ID("n"), h.Text(ctx.CSPNonce()))
}

func TestPushedScript_ignoresViewMintedNonceWithoutCSP(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[cspPushNonceViewPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()
	require.Equal(t, http.StatusOK, tc.Action("PopToast").Fire())

	frame := vt.AwaitFrame(t, frames, 2*time.Second, "via-toast")
	assert.NotContains(t, frame, "nonce=",
		"a View-minted nonce with no CSP policy must not leak onto pushed scripts")
}

func TestPushedScript_escapesNonceAttribute(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	// Only reachable if an app threads a non-base64 nonce through the
	// exported RequestWithCSPNonce; the SSE sink must escape it rather than
	// let a quote break out into a new attribute.
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.URL.Path == "/" {
			next.ServeHTTP(w, via.RequestWithCSPNonce(r, `x" onerror="boom`))
			return
		}
		next.ServeHTTP(w, r)
	})
	via.Mount[cspPushPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()
	require.Equal(t, http.StatusOK, tc.Action("PopToast").Fire())

	frame := vt.AwaitFrame(t, frames, 2*time.Second, "via-toast")
	assert.NotContains(t, frame, `onerror="boom`,
		"a quote in the nonce must be escaped, not break out into a live attribute")
	assert.Contains(t, frame, "&#34;",
		"the quote is HTML-escaped at the SSE sink")
}
