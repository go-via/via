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

type setIfChangedScopePage struct {
	Theme scope.User[string]
}

func (p *setIfChangedScopePage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestSetIfChanged_skipsPatchOnScopeUserWhenUnchanged(t *testing.T) {
	t.Parallel()
	p := &setIfChangedScopePage{}
	ctx := viatest.NewCtx(t, p)

	// Seed: first write goes through, no prior value.
	first := via.SetIfChanged(ctx, &p.Theme, "blue")
	require.True(t, first, "first write to an unset scope value reports changed=true")

	// Second identical write must short-circuit.
	second := via.SetIfChanged(ctx, &p.Theme, "blue")
	assert.False(t, second,
		"writing the same value to a scope.User[T] must report changed=false")

	// Third differing write proceeds.
	third := via.SetIfChanged(ctx, &p.Theme, "red")
	assert.True(t, third)
}

func TestApp_textRendersCurrentValue(t *testing.T) {
	t.Parallel()
	// Mirror of TestUser_textRendersCurrentValue — direct check that
	// App.Text renders the current value rather than going through HTTP.
	page := &appPage{}
	c := viatest.NewCtx(t, page)
	page.Visits.Set(c, 99)

	var buf bytes.Buffer
	require.NoError(t, page.Visits.Text(c).Render(&buf))
	assert.Contains(t, buf.String(), "99")
}

func TestApp_setGetRoundtripsUnderTestCtx(t *testing.T) {
	t.Parallel()
	// scope.App parallels scope.User in its test-context fallback: when
	// ctx.app is nil (no real App attached) writes are held on the ctx's
	// own scope so within-request reads work, the same way SessionStore
	// falls back when ctx.session is nil.
	page := &appPage{}
	c := viatest.NewCtx(t, page)
	page.Visits.Set(c, 42)
	assert.Equal(t, 42, page.Visits.Get(c))
}

func TestApp_updateAppliesFn(t *testing.T) {
	t.Parallel()
	page := &appPage{}
	c := viatest.NewCtx(t, page)
	page.Visits.Set(c, 5)
	page.Visits.Update(c, func(n int) int { return n + 3 })
	assert.Equal(t, 8, page.Visits.Get(c))
}

func TestApp_updateNilFnIsNoOp(t *testing.T) {
	t.Parallel()
	page := &appPage{}
	c := viatest.NewCtx(t, page)
	page.Visits.Set(c, 5)
	page.Visits.Update(c, nil)
	assert.Equal(t, 5, page.Visits.Get(c))
}
