package via_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type redirectingActionPage struct{}

func (p *redirectingActionPage) Go(ctx *via.Ctx) error {
	return via.Redirect("/dashboard")
}

func (p *redirectingActionPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestRedirect_returnedErrorCarriesURL(t *testing.T) {
	t.Parallel()
	err := via.Redirect("/somewhere")
	require.Error(t, err)
	var re *via.RedirectError
	require.True(t, errors.As(err, &re),
		"via.Redirect must yield a *via.RedirectError so dispatcher and tests can unwrap it")
	assert.Equal(t, "/somewhere", re.URL)
}

func TestAction_returningRedirectEmitsRedirectFrame(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[redirectingActionPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, http.StatusOK, tc.Action("Go").Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	viatest.AwaitFrame(t, frames, 2*time.Second, "/dashboard")
}

type toastingActionPage struct{}

func (p *toastingActionPage) Save(ctx *via.Ctx) error {
	return via.Toast("saved!")
}

func (p *toastingActionPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestToast_returnedErrorCarriesMessage(t *testing.T) {
	t.Parallel()
	err := via.Toast("hi")
	require.Error(t, err)
	var te *via.ToastError
	require.True(t, errors.As(err, &te))
	assert.Equal(t, "hi", te.Message)
}

type wrappedToastPage struct{}

func (p *wrappedToastPage) Save(ctx *via.Ctx) error {
	return fmt.Errorf("save flow: %w", via.Toast("wrapped-message"))
}

func (p *wrappedToastPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestToast_intentSurvivesFmtErrorfWrapping(t *testing.T) {
	t.Parallel()
	// Mirror of TestRedirect_intentSurvivesFmtErrorfWrapping for the
	// Toast sentinel — pins the same errors.As contract on the other
	// intent type.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[wrappedToastPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, http.StatusOK, tc.Action("Save").Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	viatest.AwaitFrame(t, frames, 2*time.Second, `alert("wrapped-message")`)
}

type wrappedRedirectPage struct{}

func (p *wrappedRedirectPage) Go(ctx *via.Ctx) error {
	return fmt.Errorf("auth check: %w", via.Redirect("/wrapped-target"))
}

func (p *wrappedRedirectPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestRedirect_intentSurvivesFmtErrorfWrapping(t *testing.T) {
	t.Parallel()
	// intents.go header doc: "the same sentinels survive fmt.Errorf(\"%w\", …)
	// wrapping." A regression swapping errors.As for a direct type-assert
	// in runAction would silently route wrapped sentinels through the
	// error handler instead of applying the intent.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[wrappedRedirectPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, http.StatusOK, tc.Action("Go").Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	viatest.AwaitFrame(t, frames, 2*time.Second, "/wrapped-target")
}

func TestSentinelIntents_doNotFireActionErrorHandler(t *testing.T) {
	t.Parallel()
	// Documented contract: via.Redirect and via.Toast bypass the
	// action-error handler. A regression that started routing sentinels
	// through the handler would flood user loggers with every nav/toast.
	cases := []struct {
		name   string
		method string
		mount  func(app *via.App)
	}{
		{
			"Redirect", "Go",
			func(app *via.App) { via.Mount[redirectingActionPage](app, "/") },
		},
		{
			"Toast", "Save",
			func(app *via.App) { via.Mount[toastingActionPage](app, "/") },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var handlerCalled atomic.Bool
			var server *httptest.Server
			app := via.New(
				via.WithTestServer(&server),
				via.WithActionErrorHandler(func(ctx *via.Ctx, err error) {
					handlerCalled.Store(true)
				}),
			)
			c.mount(app)
			defer server.Close()

			tc := viatest.NewClient(t, server, "/")
			require.Equal(t, http.StatusOK, tc.Action(c.method).Fire())
			// Yield so the dispatch goroutine fully settles.
			time.Sleep(50 * time.Millisecond)
			assert.False(t, handlerCalled.Load(),
				"%s intent must not invoke the action-error handler", c.name)
		})
	}
}

type emptyRedirectPage struct{}

func (p *emptyRedirectPage) Pulse(ctx *via.Ctx) error { return via.Redirect("") }
func (p *emptyRedirectPage) View(ctx *via.Ctx) h.H    { return h.Div() }

func TestRedirect_emptyIntentEnqueuesNothing(t *testing.T) {
	t.Parallel()
	// Symmetric to TestToast_emptyIntentEnqueuesNothing: an empty
	// URL flows through the dispatcher as success but produces no
	// SSE redirect frame — ctx.Redirect guards `url == ""`.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[emptyRedirectPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, http.StatusOK, tc.Action("Pulse").Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	select {
	case f := <-frames:
		assert.NotContains(t, f, "datastar-redirect",
			"via.Redirect(\"\") must produce no SSE redirect frame")
	case <-time.After(150 * time.Millisecond):
		// No frames at all is the success path.
	}
}

type emptyToastPage struct{}

func (p *emptyToastPage) Pulse(ctx *via.Ctx) error { return via.Toast("") }
func (p *emptyToastPage) View(ctx *via.Ctx) h.H    { return h.Div() }

func TestToast_emptyIntentEnqueuesNothing(t *testing.T) {
	t.Parallel()
	// The doc on via.Toast guarantees an empty message is a no-op:
	// the sentinel error still flows back so the action is treated as
	// successful, but no alert reaches the client.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[emptyToastPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, http.StatusOK, tc.Action("Pulse").Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	// Wait briefly — if Toast("") ever started leaking alert(""), it
	// would show up here as `alert("")` within the script frame.
	select {
	case f := <-frames:
		assert.NotContains(t, f, `alert("")`,
			"via.Toast(\"\") must produce no alert script frame")
	case <-time.After(150 * time.Millisecond):
		// No frames at all is the success path.
	}
}

func TestAction_returningToastQueuesPendingToast(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[toastingActionPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, http.StatusOK, tc.Action("Save").Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	viatest.AwaitFrame(t, frames, 2*time.Second, `alert("saved!")`)
}
