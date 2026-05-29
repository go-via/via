package via_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
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
	assert.True(t, c.Secure,
		"safe-by-default: the session cookie is Secure unless WithInsecureCookies opts out")
}

func TestSession_insecureCookiesDisablesSecureFlag(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server), via.WithInsecureCookies())
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	assert.False(t, cookies[0].Secure,
		"WithInsecureCookies must drop the Secure flag for local http development")
}

func TestSession_rejectsConflictingCookieOptions(t *testing.T) {
	t.Parallel()

	const want = "via: conflicting cookie security options"
	assert.PanicsWithValue(t, want, func() {
		via.New(via.WithSecureCookies(), via.WithInsecureCookies())
	}, "secure-then-insecure must fail at registration, not silently override")
	assert.PanicsWithValue(t, want, func() {
		via.New(via.WithInsecureCookies(), via.WithSecureCookies())
	}, "the conflict must be detected regardless of option order")
}

func TestSession_repeatedCookieOptionIsIdempotent(t *testing.T) {
	t.Parallel()

	// Conditionally appended options can repeat the same choice; only a
	// genuine secure-vs-insecure conflict should fail, not a redundant set.
	assert.NotPanics(t, func() {
		via.New(via.WithSecureCookies(), via.WithSecureCookies())
	}, "the same option twice is redundant, not a conflict")
	assert.NotPanics(t, func() {
		via.New(via.WithInsecureCookies(), via.WithInsecureCookies())
	}, "the same option twice is redundant, not a conflict")
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
	frames, cancel := tc.SSEReady()
	defer cancel()

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
	_ = p.UserID.Update(ctx, func(string) (string, error) { return "alice", nil })
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

// RotateSession data race (#31)

type rotateRacePage struct {
	User via.StateSessStr
}

func (p *rotateRacePage) View(ctx *via.CtxR) h.H { return h.Div(p.User.Text(ctx)) }

func (p *rotateRacePage) Rotate(ctx *via.Ctx) error {
	for i := 0; i < 100; i++ {
		ctx.Session().Rotate()
	}
	return nil
}

func (p *rotateRacePage) WriteSess(ctx *via.Ctx) error {
	for i := 0; i < 100; i++ {
		_ = p.User.Update(ctx, func(string) (string, error) { return "v", nil })
	}
	return nil
}

func TestRotateSession_doesNotRaceWithSiblingSessionBroadcast(t *testing.T) {
	t.Parallel()
	// One tab rotates — writing its ctx's session pointer — while another
	// tab's session write fans out through broadcastRender, which reads
	// every live ctx's session pointer (before any session-equality
	// filter), including the rotating one. The two tabs sit on distinct
	// sessions so neither invalidates the other, isolating the pointer
	// race: a plain *session field trips -race; the contract is that
	// concurrent rotate + fan-out stays goroutine-safe.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[rotateRacePage](app, "/")
	defer server.Close()

	tabA := vt.NewClient(t, server, "/")
	_, cancelA := tabA.SSEReady()
	defer cancelA()

	tabB := vt.NewClient(t, server, "/")
	_, cancelB := tabB.SSEReady()
	defer cancelB()

	var wg sync.WaitGroup
	var statusA, statusB int
	wg.Add(2)
	go func() { defer wg.Done(); statusA = tabA.Action("Rotate").Fire() }()
	go func() { defer wg.Done(); statusB = tabB.Action("WriteSess").Fire() }()
	wg.Wait()

	assert.Equal(t, http.StatusOK, statusA)
	assert.Equal(t, http.StatusOK, statusB)
}
