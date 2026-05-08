package via_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/scope"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type userScopedPage struct {
	Theme scope.User[string]
}

func (p *userScopedPage) UseRed(ctx *via.Ctx) error {
	p.Theme.Set(ctx, "red")
	return nil
}

func (p *userScopedPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.P(h.Text("theme="), p.Theme.Text(ctx)), h.Button(h.Text("red"), on.Click(p.UseRed)))
}

func TestScopeUser_writeFromActionAppearsInRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userScopedPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE(t)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("UseRed").Fire())

	deadline := time.After(2 * time.Second)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed early; got %q", got.String())
			}
			got.WriteString(f)
			if strings.Contains(got.String(), "theme=red") {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for theme=red; got %q", got.String())
		}
	}
}

type appScopedPage struct {
	Visits scope.App[int]
}

func (p *appScopedPage) Bump(ctx *via.Ctx) error {
	p.Visits.Set(ctx, p.Visits.Get(ctx)+1)
	return nil
}

func (p *appScopedPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.Visits.Text(ctx))
}

func TestScopeApp_sharedAcrossSessions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[appScopedPage](app, "/")
	defer server.Close()

	a := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, a.Action("Bump").Fire())
	require.Equal(t, 200, a.Action("Bump").Fire())

	b := viatest.NewClient(t, server, "/")
	body := b.HTML()
	assert.Contains(t, body, ">2<",
		"App-scoped Visits must be 2 even on a fresh session")
}

type scopeMutablePage struct {
	Score   scope.User[int]
	Visible scope.User[bool]
}

func (p *scopeMutablePage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestScopeUser_satisfiesMutableForAddHelper(t *testing.T) {
	t.Parallel()

	// Unit-style: no App attached, so SessionLoad falls back to the
	// Ctx's localScope. scope.App can't be exercised this way (no App
	// means no app store) — its Mutable conformance is enforced by the
	// type system at compile time.
	page := &scopeMutablePage{}
	c := viatest.NewCtx(t, page)

	via.Add(c, &page.Score, 5)
	via.Add(c, &page.Score, 3)
	assert.Equal(t, 8, page.Score.Get(c),
		"scope.User[int] must satisfy via.Mutable[int] so via.Add works on it")
}

func TestScopeUser_satisfiesMutableForToggleHelper(t *testing.T) {
	t.Parallel()

	page := &scopeMutablePage{}
	c := viatest.NewCtx(t, page)

	require.False(t, page.Visible.Get(c))
	via.Toggle(c, &page.Visible)
	assert.True(t, page.Visible.Get(c),
		"scope.User[bool] must satisfy via.Mutable[bool] so via.Toggle works on it")
}

// Compile-time assertion: scope.App[T] satisfies via.Mutable[T]. If a
// future refactor breaks this we'd get a build error here, which is
// stronger than any runtime test could be.
var _ via.Mutable[bool] = (*scope.App[bool])(nil)
