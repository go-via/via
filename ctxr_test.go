package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CtxR is the read-only render context. View(ctx *via.CtxR) must mount,
// render, and let the user reach the same Get/Text surface as before.

type ctxRPage struct {
	Hits  via.StateTabNum[int]
	Theme via.StateSessStr
}

func (p *ctxRPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Span(h.ID("hits"), h.Textf("%d", p.Hits.Read(ctx))),
		h.Span(h.ID("theme"), h.Text(p.Theme.Read(ctx))),
	)
}

func TestCtxR_ViewSignature_mountsAndRenders(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxRPage](app, "/")
	defer server.Close()

	body := vt.NewClient(t, server, "/").HTML()
	assert.Contains(t, body, `<span id="hits">0</span>`,
		"View(ctx *via.CtxR) must produce the same render output as the old *via.Ctx signature")
	assert.Contains(t, body, `<span id="theme"></span>`,
		"StateSess.Text(ctx *via.CtxR) must read the value through the read-only ctx")
}

type ctxRAccessorsPage struct{}

func (p *ctxRAccessorsPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Span(h.ID("id"), h.Text(ctx.ID())),
		h.Span(h.ID("flavor"), h.Text(ctx.Cookie("flavor"))),
	)
}

func TestCtxR_ExposesIDAndCookieReadsToView(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxRAccessorsPage](app, "/")
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.AddCookie(&http.Cookie{Name: "flavor", Value: "mint"})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body := vt.NewClient(t, server, "/").HTML()
	// ID is a non-empty tab id; just confirm the slot rendered something.
	assert.Contains(t, body, `<span id="id">`,
		"CtxR.ID must be reachable from View")

	// Cookie round-trip via a dedicated request that carries the cookie.
	body2 := func() string {
		req, _ := http.NewRequest("GET", server.URL+"/", nil)
		req.AddCookie(&http.Cookie{Name: "flavor", Value: "mint"})
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		buf := make([]byte, 0, 1<<14)
		var chunk [4096]byte
		for {
			n, err := resp.Body.Read(chunk[:])
			if n > 0 {
				buf = append(buf, chunk[:n]...)
			}
			if err != nil {
				break
			}
		}
		return string(buf)
	}()
	assert.Contains(t, body2, `<span id="flavor">mint</span>`,
		"CtxR.Cookie must read the named cookie off the in-flight request")
}

type badViewParamPage struct{}

func (p *badViewParamPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestCtxR_MountRejectsViewWithCtxParam(t *testing.T) {
	t.Parallel()
	// View must take *via.CtxR — accepting *via.Ctx in View would let
	// the body call Set/Update and break the read-only guarantee.
	app := via.New()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Mount with View(ctx *via.Ctx) must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "View has the wrong signature",
			"the panic must mention the View signature contract")
		assert.Contains(t, msg, "via.CtxR",
			"the panic must point at the required parameter type")
	}()
	via.Mount[badViewParamPage](app, "/")
}
