package scope_test

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/scope"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type userPage struct {
	Theme scope.User[string]
	Count scope.User[int]
}

func (p *userPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestUser_keyExposesWireKeyAfterMount(t *testing.T) {
	t.Parallel()

	page := &userPage{}
	viatest.NewCtx(t, page)
	assert.Equal(t, "theme", page.Theme.Key())
	assert.Equal(t, "count", page.Count.Key())
}

func TestUser_getReturnsZeroWhenUnset(t *testing.T) {
	t.Parallel()

	page := &userPage{}
	c := viatest.NewCtx(t, page)
	assert.Equal(t, "", page.Theme.Get(c))
	assert.Equal(t, 0, page.Count.Get(c))
}

func TestUser_setThenGetRoundTrips(t *testing.T) {
	t.Parallel()

	page := &userPage{}
	c := viatest.NewCtx(t, page)
	page.Theme.Set(c, "dark")
	assert.Equal(t, "dark", page.Theme.Get(c))
}

func TestUser_updateAppliesFn(t *testing.T) {
	t.Parallel()

	page := &userPage{}
	c := viatest.NewCtx(t, page)
	page.Count.Set(c, 7)
	page.Count.Update(c, func(n int) int { return n + 3 })
	assert.Equal(t, 10, page.Count.Get(c))
}

func TestUser_updateNilFnIsNoOp(t *testing.T) {
	t.Parallel()

	page := &userPage{}
	c := viatest.NewCtx(t, page)
	page.Count.Set(c, 42)
	page.Count.Update(c, nil)
	assert.Equal(t, 42, page.Count.Get(c))
}

func TestUser_textRendersCurrentValue(t *testing.T) {
	t.Parallel()

	page := &userPage{}
	c := viatest.NewCtx(t, page)
	page.Theme.Set(c, "midnight")

	var buf bytes.Buffer
	require.NoError(t, page.Theme.Text(c).Render(&buf))
	assert.Contains(t, buf.String(), "midnight")
}

type appPage struct {
	Visits scope.App[int]
}

func (p *appPage) Bump(ctx *via.Ctx) error {
	p.Visits.Set(ctx, p.Visits.Get(ctx)+1)
	return nil
}

func (p *appPage) View(ctx *via.Ctx) h.H { return h.Div(p.Visits.Text(ctx)) }

func TestApp_keyMatchesFieldName(t *testing.T) {
	t.Parallel()

	page := &appPage{}
	viatest.NewCtx(t, page)
	assert.Equal(t, "visits", page.Visits.Key())
}

func TestApp_writesAreVisibleOnReload(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[appPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Bump").Fire())
	assert.Contains(t, tc.Reload(), ">1<")
}
