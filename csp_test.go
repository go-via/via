package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/mw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, _ := io.ReadAll(r)
	return string(b)
}

type cspEchoPage struct{}

func (p *cspEchoPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.ID("nonce"), h.Text(ctx.CSPNonce()))
}

type strictCSPPage struct{}

func (p *strictCSPPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.ID("nonce"), h.Text(ctx.CSPNonce()))
}

func TestStrictCSP_setsHeaderAndMatchesViewNonce(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(mw.CSP())
	via.Mount[strictCSPPage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	csp := resp.Header.Get("Content-Security-Policy")
	require.NotEmpty(t, csp, "StrictCSP must set the header")
	assert.Contains(t, csp, "default-src 'self'")
	assert.Contains(t, csp, "object-src 'none'")
	assert.Contains(t, csp, "base-uri 'self'")
	assert.Contains(t, csp, "script-src 'self' 'nonce-")

	body := readAll(t, resp.Body)
	// The CSP header has 'nonce-XYZ'; pull the XYZ and confirm it
	// matches the rendered <div>.
	const prefix = "'nonce-"
	idx := strings.Index(csp, prefix)
	require.NotEqual(t, -1, idx)
	end := strings.Index(csp[idx+len(prefix):], "'")
	require.NotEqual(t, -1, end)
	nonce := csp[idx+len(prefix) : idx+len(prefix)+end]
	assert.Contains(t, body, `<div id="nonce">`+nonce+`</div>`)
}

func TestStrictCSP_extraDirectivesAppended(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(mw.CSP("img-src 'self' data:"))
	via.Mount[strictCSPPage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "img-src 'self' data:")
}

func TestCSPNonce_middlewareThreadedNonceReachesView(t *testing.T) {
	t.Parallel()

	const nonce = "test-mw-nonce-XYZ"
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("Content-Security-Policy",
			"script-src 'self' 'nonce-"+nonce+"'")
		next.ServeHTTP(w, via.RequestWithCSPNonce(r, nonce))
	})
	via.Mount[cspEchoPage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "nonce-"+nonce)
	body := readAll(t, resp.Body)
	assert.Contains(t, body, `<div id="nonce">`+nonce+`</div>`,
		"View should observe the nonce middleware injected via r.Context")
}

// CSP nonce — externally observable via rendered HTML in views that
// embed ctx.CSPNonce(). Format / stability / uniqueness all assertable
// without reaching into Ctx internals.

type cspTwoNoncePage struct{}

func (p *cspTwoNoncePage) View(ctx *via.CtxR) h.H {
	// Two embeds within the same render: stability means both spans
	// contain the same value.
	return h.Div(
		h.Span(h.ID("a"), h.Text(ctx.CSPNonce())),
		h.Span(h.ID("b"), h.Text(ctx.CSPNonce())),
	)
}

func extractNonceFromSpan(body, id string) string {
	prefix := `<span id="` + id + `">`
	i := strings.Index(body, prefix)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i+len(prefix):], "</span>")
	if j < 0 {
		return ""
	}
	return body[i+len(prefix) : i+len(prefix)+j]
}

func TestCSPNonce_renderedValueIsBase64URLFormatted(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[cspEchoPage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body := readAll(t, resp.Body)

	prefix := `<div id="nonce">`
	i := strings.Index(body, prefix)
	require.NotEqual(t, -1, i)
	j := strings.Index(body[i+len(prefix):], "</div>")
	require.NotEqual(t, -1, j)
	nonce := body[i+len(prefix) : i+len(prefix)+j]

	require.NotEmpty(t, nonce)
	assert.GreaterOrEqual(t, len(nonce), 22,
		"16 bytes ≈ 22 url-safe base64 chars; got %q", nonce)
	for _, r := range nonce {
		ok := (r >= 'A' && r <= 'Z') ||
			(r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-'
		assert.True(t, ok, "nonce char %q must be url-safe base64", r)
	}
}

func TestCSPNonce_isStableAcrossCallsInSameRequest(t *testing.T) {
	t.Parallel()
	// A view that embeds the nonce twice must observe the same value —
	// otherwise the script tag the view writes and the header the
	// middleware writes would desync on every request.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[cspTwoNoncePage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body := readAll(t, resp.Body)

	a := extractNonceFromSpan(body, "a")
	b := extractNonceFromSpan(body, "b")
	require.NotEmpty(t, a)
	assert.Equal(t, a, b,
		"two ctx.CSPNonce() calls in the same render must return the same value")
}

func TestCSPNonce_differsAcrossRequests(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[cspEchoPage](app, "/")
	defer server.Close()

	get := func() string {
		resp, err := http.Get(server.URL + "/")
		require.NoError(t, err)
		defer resp.Body.Close()
		body := readAll(t, resp.Body)
		prefix := `<div id="nonce">`
		i := strings.Index(body, prefix)
		j := strings.Index(body[i+len(prefix):], "</div>")
		return body[i+len(prefix) : i+len(prefix)+j]
	}

	assert.NotEqual(t, get(), get(),
		"two separate requests must produce distinct nonces")
}
