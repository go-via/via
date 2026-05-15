package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cookiePage struct{}

func (p *cookiePage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestCookie_returnsEmptyWhenAbsent(t *testing.T) {
	t.Parallel()

	c := &cookiePage{}
	ctx := viatest.NewCtx(t, c)
	assert.Equal(t, "", ctx.Cookie("flavor"),
		"missing cookie should yield \"\"")
}

type cookieEchoPage struct {
	Flavor via.State[string]
}

func (p *cookieEchoPage) OnInit(ctx *via.Ctx) error {
	p.Flavor.Set(ctx, ctx.Cookie("flavor"))
	return nil
}

func (p *cookieEchoPage) View(ctx *via.Ctx) h.H { return h.Div(p.Flavor.Text()) }

func TestCookie_readsValueFromRequest(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[cookieEchoPage](app, "/")
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.AddCookie(&http.Cookie{Name: "flavor", Value: "mint"})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "mint",
		"ctx.Cookie should read the named cookie off the in-flight request")
}

func TestCookie_methodsNoOpOnEdgeInputs(t *testing.T) {
	t.Parallel()

	c := &cookiePage{}
	ctx := viatest.NewCtx(t, c)

	cases := []struct {
		name string
		do   func()
	}{
		{"DelCookie outside action scope", func() { ctx.DelCookie("anything") }},
		{"DelCookie empty name", func() { ctx.DelCookie("") }},
		{"SetCookie outside action scope (Writer nil)",
			func() { ctx.SetCookie(&http.Cookie{Name: "x", Value: "y"}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.NotPanics(t, c.do)
		})
	}
}
