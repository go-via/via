package via_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/mw"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kitchenSinkPage exercises the full surface area in one composition:
// Signal[T] + StateTab[T] + a typed query param + a method-value action +
// the typed-via.Log helper.
type kitchenSinkPage struct {
	Q     string `query:"q"`
	N     via.StateTabNum[int]
	Theme via.SignalStr `via:"theme,init=blue"`
}

func (p *kitchenSinkPage) Bump(ctx *via.Ctx) {
	via.Log(ctx).Log(via.LogInfo, "bump", "n", p.N.Read(ctx))
	_ = p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil })
}

func (p *kitchenSinkPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.P(h.Textf("q=%s", p.Q)),
		h.P(p.N.Text(ctx)),
		h.Span(p.Theme.Text()),
		h.Button(h.Text("+"), on.Click(p.Bump)),
	)
}

func TestIntegration_fullProductionStack(t *testing.T) {
	t.Parallel()

	app, server, logger := newLoggedApp(t, via.LogInfo,
		via.WithTitle("KS"),
		via.WithLang("en"),
	)
	mw.Defaults(app)
	app.Use(mw.CSP())
	via.Mount[kitchenSinkPage](app, "/page")

	// Page render with query param.
	resp, err := http.Get(server.URL + "/page?q=hello")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("X-Request-ID"),
		"RequestID middleware should stamp every render")
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"),
		"script-src 'self' 'nonce-",
		"StrictCSP should set the header")

	body := readAll(t, resp.Body)
	assert.Contains(t, body, "q=hello")
	assert.Contains(t, body, `<html lang="en">`)
	assert.Contains(t, body, `&#34;theme&#34;:&#34;blue&#34;`)

	// Action through the test client also rides the full stack.
	tc := vt.NewClient(t, server, "/page?q=world")
	require.Equal(t, 200, tc.Action("Bump").Fire())

	// AccessLog records both the page render and the action POST,
	// each with a rid.
	logs := logger.snapshot()
	pageHits, actionHits := 0, 0
	for _, r := range logs {
		if strings.Contains(r.msg, "GET /page") {
			pageHits++
		}
		if strings.Contains(r.msg, "POST /_action/Bump") {
			actionHits++
		}
	}
	assert.GreaterOrEqual(t, pageHits, 2)
	assert.GreaterOrEqual(t, actionHits, 1)
}
