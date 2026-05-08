package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, _ := io.ReadAll(r)
	return string(b)
}

type cspPage struct{}

func (p *cspPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestCSPNonce_returnsRandomBase64URLString(t *testing.T) {
	t.Parallel()

	c := &cspPage{}
	ctx := viatest.NewCtx(t, c)

	n1 := ctx.CSPNonce()
	require.NotEmpty(t, n1)

	urlSafe := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	assert.True(t, urlSafe.MatchString(n1),
		"nonce should be base64url (no = padding); got %q", n1)
	assert.GreaterOrEqual(t, len(n1), 22,
		"16 bytes ≈ 22 url-safe base64 chars")
}

func TestCSPNonce_isStablePerCtx(t *testing.T) {
	t.Parallel()

	c := &cspPage{}
	ctx := viatest.NewCtx(t, c)
	a := ctx.CSPNonce()
	b := ctx.CSPNonce()
	assert.Equal(t, a, b,
		"same Ctx should hand out the same nonce on repeated calls")
}

func TestCSPNonce_differsAcrossCtx(t *testing.T) {
	t.Parallel()

	c := &cspPage{}
	ctx1 := viatest.NewCtx(t, c)
	ctx2 := viatest.NewCtx(t, c)
	assert.NotEqual(t, ctx1.CSPNonce(), ctx2.CSPNonce(),
		"two Ctxs must produce distinct nonces")
}

type cspEchoPage struct{}

func (p *cspEchoPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.ID("nonce"), h.Text(ctx.CSPNonce()))
}

type strictCSPPage struct{}

func (p *strictCSPPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.ID("nonce"), h.Text(ctx.CSPNonce()))
}

func TestStrictCSP_setsHeaderAndMatchesViewNonce(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.StrictCSP())
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
	idx := indexOf(csp, prefix)
	require.NotEqual(t, -1, idx)
	end := indexOf(csp[idx+len(prefix):], "'")
	require.NotEqual(t, -1, end)
	nonce := csp[idx+len(prefix) : idx+len(prefix)+end]
	assert.Contains(t, body, `<div id="nonce">`+nonce+`</div>`)
}

func TestStrictCSP_extraDirectivesAppended(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.StrictCSP("img-src 'self' data:"))
	via.Mount[strictCSPPage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "img-src 'self' data:")
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
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
