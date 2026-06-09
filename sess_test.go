package via_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cookie defaults

func TestSession_cookieIsSetWithSecureDefaults(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	resp, err := server.Client().Get(server.URL + "/test")
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

	app := via.New(via.WithInsecureCookies())
	server := vt.Serve(t, app)
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	resp, err := server.Client().Get(server.URL + "/test")
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

	app := via.New(via.WithSecureCookies())
	server := vt.Serve(t, app)
	app.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	resp, err := server.Client().Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	assert.True(t, cookies[0].Secure,
		"WithSecureCookies must mark the session cookie Secure")
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
	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[rotateRacePage](app, "/")

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
