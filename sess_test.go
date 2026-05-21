package via_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/sess"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cookie defaults

func TestSession_cookieIsSetWithSecureDefaults(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	c := cookies[0]
	assert.Equal(t, "via_session", c.Name)
	assert.Len(t, c.Value, 64, "32 bytes hex-encoded = 64 chars")
	assert.True(t, c.HttpOnly)
	assert.Equal(t, "/", c.Path)
	assert.False(t, c.Secure, "without WithSecureCookies the Secure flag is off")
}

func TestSession_secureFlagWhenWithSecureCookiesEnabled(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server), via.WithSecureCookies())
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	assert.True(t, cookies[0].Secure,
		"WithSecureCookies must mark the session cookie Secure")
}

// Typed session: PutSess / GetSess / ClearSess

type sessUser struct {
	Email string
	Name  string
}

type authPage struct {
	Email via.SignalStr `via:"email"`
}

func (p *authPage) LogIn(ctx *via.Ctx) error {
	sess.Put(ctx, sessUser{Email: p.Email.Read(ctx), Name: "Alice"})
	return nil
}

func (p *authPage) LogOut(ctx *via.Ctx) error {
	sess.Clear[sessUser](ctx)
	return nil
}

func (p *authPage) View(ctx *via.CtxR) h.H {
	if u, ok := sess.Get[sessUser](ctx); ok {
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

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("LogIn").
		WithSignal("email", "alice@example.com").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "hello Alice")
}

func TestGetSess_visibleFromMiddlewareViaRequest(t *testing.T) {
	t.Parallel()

	var sawEmail atomic.Pointer[string]

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if u, ok := sess.Get[sessUser](r); ok {
			s := u.Email
			sawEmail.Store(&s)
		}
		next.ServeHTTP(w, r)
	})
	via.Mount[authPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
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

	tc := vt.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("LogIn").WithSignal("email", "alice").Fire())
	require.Equal(t, 200, tc.Action("LogOut").Fire())

	tc2 := vt.NewClient(t, server, "/")
	body := tc2.HTML()
	assert.NotContains(t, body, "hello",
		"a fresh session should not see the previous user's data")
}

// RotateSession

type loginPage struct {
	UserID via.StateSessStr
}

func (p *loginPage) Login(ctx *via.Ctx) error {
	p.UserID.Update(ctx, func(string) string { return "alice" })
	sess.Rotate(ctx)
	return nil
}

func (p *loginPage) View(ctx *via.CtxR) h.H {
	return h.Div(p.UserID.Text(ctx))
}

func TestRotateSession_changesCookieValue(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[loginPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")

	originalHTML := tc.HTML()
	require.NotEmpty(t, originalHTML)

	require.Equal(t, 200, tc.Action("Login").Fire())

	// A separate client with no shared cookie jar should get a fresh
	// session and not observe the rotated tab's data.
	tc2 := vt.NewClient(t, server, "/")
	body2 := tc2.HTML()
	assert.NotContains(t, body2, ">alice<",
		"a fresh cookie jar should NOT see another session's User-scoped data")

	// The original client's cookie is the rotated value; data carried over.
	frames, cancel := tc.SSE()
	defer cancel()
	require.Equal(t, 200, tc.Action("Login").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, ">alice<")
}
