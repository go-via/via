package via_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type protectedPage struct {
	N via.State[int]
}

func (p *protectedPage) Bump(ctx *via.Ctx) error {
	p.N.Set(ctx, p.N.Get(ctx)+1)
	return nil
}

func (p *protectedPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.N.Text(), h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestGroupMiddleware_appliesToActionPOST(t *testing.T) {
	t.Parallel()

	var seenAuth atomic.Bool
	var allowed atomic.Bool
	allowed.Store(true)

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))

	g := app.Group("/p")
	g.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		seenAuth.Store(true)
		if !allowed.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
	via.MountOn[protectedPage](g, "/secret")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/p/secret")
	require.True(t, seenAuth.Load(), "middleware must run on the page render")

	seenAuth.Store(false)
	require.Equal(t, 200, tc.Action("Bump").Fire())
	require.True(t, seenAuth.Load(),
		"group middleware must run on the action POST too — not only on the page render")

	// Now flip the flag and the next action POST must be 403'd by the
	// middleware before runAction touches state.
	allowed.Store(false)
	got := tc.Action("Bump").Fire()
	assert.Equal(t, http.StatusForbidden, got,
		"middleware short-circuit on action POST should return its status")
}
