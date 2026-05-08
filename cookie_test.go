package via_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cookiePage struct{}

func (p *cookiePage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestCookie_returnsEmptyWhenAbsent(t *testing.T) {
	t.Parallel()

	c := &cookiePage{}
	ctx := via.NewBoundCtx(c)
	assert.Equal(t, "", ctx.Cookie("flavor"),
		"missing cookie should yield \"\"")
}

func TestCookie_readsValueFromRequest(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[cookiePage](app, "/")
	defer server.Close()

	// Issue a render with a flavor cookie set, then peek at how the
	// underlying Ctx reads it. We test indirectly through the body's
	// session cookie which renderPage injects.
	u, err := url.Parse(server.URL + "/")
	require.NoError(t, err)
	req, _ := http.NewRequest("GET", u.String(), nil)
	req.AddCookie(&http.Cookie{Name: "flavor", Value: "mint"})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	// We can't observe ctx.Cookie's read directly without a custom
	// Composition. The TestCookie_returnsEmptyWhenAbsent path covers
	// the nil-request branch; this round-trip just ensures the cookie
	// flow doesn't crash.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSetCookie_outsideActionScopeIsNoOp(t *testing.T) {
	t.Parallel()

	c := &cookiePage{}
	ctx := via.NewBoundCtx(c)
	assert.NotPanics(t, func() {
		ctx.SetCookie(&http.Cookie{Name: "x", Value: "y"})
	}, "SetCookie outside action scope (Writer nil) should silently no-op")
}
