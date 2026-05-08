package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sessUser struct {
	Email string
	Name  string
}

type authPage struct {
	Email via.Signal[string] `via:"email"`
}

func (p *authPage) LogIn(ctx *via.Ctx) error {
	via.PutSess(ctx, sessUser{Email: p.Email.Get(ctx), Name: "Alice"})
	return nil
}

func (p *authPage) LogOut(ctx *via.Ctx) error {
	via.ClearSess[sessUser](ctx)
	return nil
}

func (p *authPage) View(ctx *via.Ctx) h.H {
	if u, ok := via.GetSess[sessUser](ctx); ok {
		return h.Div(h.P(h.Textf("hello %s", u.Name)),
			h.Button(h.Text("logout"), on.Click(p.LogOut)))
	}
	return h.Div(
		h.Input(h.Type("email"), p.Email.Bind()),
		h.Button(h.Text("login"), on.Click(p.LogIn)),
	)
}

func TestPutSess_makesValueAvailableInRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[authPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE(t)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("LogIn").
		WithSignal("email", "alice@example.com").Fire())

	deadline := time.After(2 * time.Second)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed early; got %q", got.String())
			}
			got.WriteString(f)
			if strings.Contains(got.String(), "hello Alice") {
				return
			}
		case <-deadline:
			t.Fatalf("expected re-render with hello Alice; got %q", got.String())
		}
	}
}

func TestGetSess_visibleFromMiddlewareViaRequest(t *testing.T) {
	t.Parallel()

	var sawEmail atomic.Pointer[string]

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if u, ok := via.GetSess[sessUser](r); ok {
			s := u.Email
			sawEmail.Store(&s)
		}
		next.ServeHTTP(w, r)
	})
	via.Mount[authPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("LogIn").WithSignal("email", "bob@example.com").Fire())

	// Subsequent action POST through the same client should run through
	// middleware with the session populated.
	require.Equal(t, 200, tc.Action("LogIn").WithSignal("email", "bob@example.com").Fire())

	v := sawEmail.Load()
	require.NotNil(t, v, "middleware never observed any session value")
	assert.Equal(t, "bob@example.com", *v,
		"middleware should see the typed-session user via *http.Request")
}

func TestPutSess_andClearSess_roundTrip(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[authPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("LogIn").WithSignal("email", "alice").Fire())
	require.Equal(t, 200, tc.Action("LogOut").Fire())

	tc2 := viatest.NewClient(t, server, "/")
	body := tc2.HTML()
	assert.NotContains(t, body, "hello",
		"a fresh session should not see the previous user's data")
}
