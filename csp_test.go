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
