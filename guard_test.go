package via_test

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rendered State is what makes this root live; there is no OnInit.
type guardedRoot struct{ n via.State[int] }

func (g *guardedRoot) Bump(*via.Ctx) { g.n.Set(g.n.Get() + 1) }

func (g *guardedRoot) View() h.H {
	return h.Div(g.n.Display(), h.Button(via.On("click", g.Bump), h.Str("+")))
}

// No State, no Tick/Listen, so its action runs over the plain-dispatch path
// with no tab id.
type plainGuardedRoot struct{}

func (p *plainGuardedRoot) Bump(*via.Ctx) {}

func (p *plainGuardedRoot) View() h.H {
	return h.Div(h.Button(via.On("click", p.Bump), h.Str("+")))
}

func flagGuard(allow *atomic.Bool) via.Guard {
	return func(*via.Ctx) error {
		if allow.Load() {
			return nil
		}
		return errors.New("denied")
	}
}

func redirectGuard(allow *atomic.Bool) via.Guard {
	return func(ctx *via.Ctx) error {
		if !allow.Load() {
			ctx.Redirect("/login")
		}
		return nil
	}
}

// Bypasses vt.Connect, which fatals on a non-200, so a denial's own status
// code is observable.
func rawConnect(t *testing.T, app *vt.App) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, app.URL()+"/_via/sse", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestGuard_redirectsAnAnonymousPageGet(t *testing.T) {
	t.Parallel()
	allow := &atomic.Bool{}
	r := via.NewRouter()
	via.Mount(r, "/app", plainGuardedRoot{}, via.Protect(redirectGuard(allow)))
	app := vt.Serve(t, r)

	client := app.Client()
	client.CheckRedirect = noFollow
	req, err := http.NewRequest(http.MethodGet, app.URL()+"/app", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

func TestGuard_redirectsAnAnonymousDatastarAction(t *testing.T) {
	t.Parallel()
	allow := &atomic.Bool{}
	allow.Store(true)
	r := via.NewRouter()
	via.Mount(r, "/", plainGuardedRoot{}, via.Protect(redirectGuard(allow)))
	app := vt.Serve(t, r)

	// Primes the cached action URL while allowed; otherwise the second Fire
	// would re-fetch the (by then denied) page to discover it.
	status, _ := app.Action(0).Fire()
	require.Less(t, status, 300, "precondition: the action succeeds while allowed")

	allow.Store(false)
	status, body := app.Action(0).Fire()
	assert.Equal(t, http.StatusOK, status, "a datastar denial is a navigation script, not a redirect status")
	assert.Contains(t, body, "data-via-to")
}

func TestGuard_reauthorizesEveryLiveAction(t *testing.T) {
	t.Parallel()
	allow := &atomic.Bool{}
	allow.Store(true)
	r := via.NewRouter()
	via.Mount(r, "/", guardedRoot{}, via.Protect(flagGuard(allow)))
	app := vt.Serve(t, r)

	conn := app.Connect()
	defer conn.Close()

	status, _ := app.Action(0).Over(conn).Fire()
	require.Less(t, status, 300, "precondition: the live action succeeds while allowed")

	allow.Store(false)
	status, _ = app.Action(0).Over(conn).Fire()
	assert.Equal(t, http.StatusForbidden, status,
		"OnInit ran once at connect; only a Guard re-checked on this click catches a mid-stream revocation")
}

func TestGuard_runsAfterTheOriginFloor(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithTrustedOrigin("https://trusted.example"))
	// Denies only the POST, so the page GET that discovers the action URL
	// still succeeds.
	via.Mount(r, "/", plainGuardedRoot{}, via.Protect(func(ctx *via.Ctx) error {
		if ctx.Request().Method == http.MethodPost {
			return errors.New("guard ran")
		}
		return nil
	}))
	app := vt.Serve(t, r)

	status, body := app.Action(0).NoOrigin().Fire()
	assert.Equal(t, http.StatusForbidden, status)
	assert.Contains(t, body, "forbidden origin",
		"the origin floor's own message must answer — a guard denial reads just \"forbidden\", so this "+
			"proves the guard never ran")
}

func TestGuard_deniesBeforeTheBodyIsParsed(t *testing.T) {
	t.Parallel()
	allow := &atomic.Bool{}
	allow.Store(true)
	r := via.NewRouter()
	via.Mount(r, "/", plainGuardedRoot{}, via.Protect(flagGuard(allow)))
	app := vt.Serve(t, r)

	// Primes app's cached action URL while allowed.
	status, _ := app.Action(0).Fire()
	require.Less(t, status, 300, "precondition: the action succeeds while allowed")

	allow.Store(false)
	big := `{"f0":"` + strings.Repeat("a", 2<<20) + `"}`
	status, body := app.Action(0).Body(big).Fire()
	assert.Equal(t, http.StatusForbidden, status,
		"the guard must deny before decodeSignals ever reads the oversize body")
	assert.NotContains(t, body, "too large")
}

func TestGuard_deniesAConnectWithoutConsumingAStreamSlot(t *testing.T) {
	t.Parallel()
	allow := &atomic.Bool{}
	r := via.NewRouter(via.WithMaxSSEConn(1))
	via.Mount(r, "/", guardedRoot{}, via.Protect(flagGuard(allow)))
	app := vt.Serve(t, r)

	assert.Equal(t, http.StatusForbidden, rawConnect(t, app))

	allow.Store(true)
	conn := app.Connect() // fatals if the denied attempt above had burned the one slot
	defer conn.Close()
}

func TestGuard_doesNotGateTheDatastarAsset(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithGuard(func(*via.Ctx) error { return errors.New("denied") }))
	via.Mount(r, "/", plainGuardedRoot{})
	app := vt.Serve(t, r)

	status, _ := app.Get("/_via/datastar.js")
	assert.Equal(t, http.StatusOK, status)
}

func TestGuard_doesNotGateASiblingHandler(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithGuard(func(*via.Ctx) error { return errors.New("denied") }))
	via.Mount(r, "/", plainGuardedRoot{})

	mux := http.NewServeMux()
	mux.HandleFunc("/sibling", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("sibling ok")) })
	mux.Handle("/", r)

	app := vt.Serve(t, mux)
	status, body := app.Get("/sibling")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "sibling ok")
}

func TestGuard_composesRouterThenMountInOrder(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var seen []string
	record := func(name string) via.Guard {
		return func(*via.Ctx) error {
			mu.Lock()
			seen = append(seen, name)
			mu.Unlock()
			return nil
		}
	}
	deny := func(*via.Ctx) error { return errors.New("denied") }

	r := via.NewRouter(via.WithGuard(record("router")))
	via.Mount(r, "/", plainGuardedRoot{}, via.Protect(record("mount1"), deny, record("mount2")))
	app := vt.Serve(t, r)

	status, _ := app.Get("/")
	require.Equal(t, http.StatusForbidden, status)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"router", "mount1"}, seen,
		"router-then-mount order, short-circuited at the first denial")
}

// OK must be exported: Session.Put marshals to JSON, and an unexported field
// marshals to "{}".
type sharedMarker struct{ OK bool }

func TestGuard_sharesOneCtxAcrossTheChain(t *testing.T) {
	t.Parallel()
	first := func(ctx *via.Ctx) error { ctx.Session().Put(sharedMarker{OK: true}); return nil }
	second := func(ctx *via.Ctx) error {
		m, ok := ctx.Session().Get[sharedMarker]()
		if !ok || !m.OK {
			return errors.New("marker missing")
		}
		return nil
	}
	r := via.NewRouter(via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long")))
	via.Mount(r, "/", plainGuardedRoot{}, via.Protect(first, second))
	app := vt.Serve(t, r)

	status, _ := app.Get("/")
	assert.Equal(t, http.StatusOK, status,
		"the second guard must see the session write the first guard made on the same Ctx")
}

func TestGuard_mountedAtRootDoesNotGateASiblingMount(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/", plainGuardedRoot{}, via.Protect(func(*via.Ctx) error { return errors.New("denied") }))
	via.Mount(r, "/admin", plainGuardedRoot{})
	app := vt.Serve(t, r)

	status, _ := app.Get("/")
	assert.Equal(t, http.StatusForbidden, status)

	status, _ = app.Get("/admin")
	assert.Equal(t, http.StatusOK, status)
}
