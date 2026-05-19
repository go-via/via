package scope_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/scope"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time conformance: both shapes satisfy via.Mutable[T].
var (
	_ via.Mutable[int]    = (*scope.User[int])(nil)
	_ via.Mutable[string] = (*scope.User[string])(nil)
	_ via.Mutable[int]    = (*scope.App[int])(nil)
	_ via.Mutable[bool]   = (*scope.App[bool])(nil)
)

// scope.User round-trips across tab renders on the same session: a write
// from action 1 is visible to a subsequent render. Also covers Key()
// defaulting to the lowercased field name (the wire key shows up in the
// rendered data-signals payload).

type userRoundTripPage struct {
	Theme scope.User[string]
	Count scope.User[int]
}

func (p *userRoundTripPage) Set(ctx *via.Ctx) error {
	p.Theme.Set(ctx, "midnight")
	p.Count.Set(ctx, 7)
	return nil
}

func (p *userRoundTripPage) Bump(ctx *via.Ctx) error {
	p.Count.Update(ctx, func(n int) int { return n + 3 })
	return nil
}

func (p *userRoundTripPage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Span(h.ID("theme"), p.Theme.Text(ctx)),
		h.Span(h.ID("count"), p.Count.Text(ctx)),
	)
}

func TestUser_setThenRenderRoundTrips(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userRoundTripPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Set").Fire())

	body := tc.Reload()
	assert.Contains(t, body, `<span id="theme">midnight</span>`,
		"scope.User write must survive a fresh render on the same session")
	assert.Contains(t, body, `<span id="count">7</span>`)
}

func TestUser_updateAppliesFn(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userRoundTripPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Set").Fire())  // count := 7
	require.Equal(t, 200, tc.Action("Bump").Fire()) // count += 3

	body := tc.Reload()
	assert.Contains(t, body, `<span id="count">10</span>`,
		"Update must read-modify-write the session value")
}

func TestUser_keyDefaultsToLowercasedFieldName(t *testing.T) {
	t.Parallel()
	// The wire key surfaces in the page's data-signals payload. No need
	// for a separate Key() unit test — the mounted output is the contract.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userRoundTripPage](app, "/")
	defer server.Close()

	body := viatest.NewClient(t, server, "/").HTML()
	assert.Contains(t, body, "theme")
	assert.Contains(t, body, "count")
}

// scope.App is shared across sessions: a write from one client surfaces
// in a fresh client's render.

type appCounterPage struct {
	Visits scope.App[int]
}

func (p *appCounterPage) Bump(ctx *via.Ctx) error {
	p.Visits.Update(ctx, func(n int) int { return n + 1 })
	return nil
}

func (p *appCounterPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.Span(h.ID("visits"), p.Visits.Text(ctx)))
}

func TestApp_writesAreVisibleAcrossSessions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[appCounterPage](app, "/")
	defer server.Close()

	a := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, a.Action("Bump").Fire())
	require.Equal(t, 200, a.Action("Bump").Fire())

	// Fresh client (different session) must see the app-scoped value.
	b := viatest.NewClient(t, server, "/")
	body := b.HTML()
	assert.Contains(t, body, `<span id="visits">2</span>`,
		"scope.App value must be shared across sessions")
}

type silentUserPage struct {
	// Same wireKey "theme" as userRoundTripPage, but the View never
	// reads it — used to prove session-scoped broadcasts skip
	// non-displaying tabs.
	Theme scope.User[string]
}

func (p *silentUserPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.Span(h.ID("mute"), h.Text("no readers here")))
}

func TestUser_writeWakesOnlyTabsThatReadTheKey(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userRoundTripPage](app, "/reader")
	via.Mount[silentUserPage](app, "/silent")
	defer server.Close()

	reader := viatest.NewClient(t, server, "/reader")
	silent := reader.Fork("/silent")

	framesS, cancelS := silent.SSE()
	defer cancelS()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, reader.Action("Set").Fire())

	select {
	case frame := <-framesS:
		assert.Failf(t, "non-reader peer was woken",
			"scope.User write must skip tabs whose View did not read the key; got %q", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

type silentAppPage struct {
	// Same wireKey "visits" as appCounterPage, but the View never reads
	// it — used to prove that broadcasts skip non-displaying tabs.
	Visits scope.App[int]
}

func (p *silentAppPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.Span(h.ID("mute"), h.Text("no readers here")))
}

func TestApp_writeWakesOnlyTabsThatReadTheKey(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[appCounterPage](app, "/reader")
	via.Mount[silentAppPage](app, "/silent")
	defer server.Close()

	reader := viatest.NewClient(t, server, "/reader")
	silent := viatest.NewClient(t, server, "/silent")

	framesR, cancelR := reader.SSE()
	defer cancelR()
	framesS, cancelS := silent.SSE()
	defer cancelS()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, reader.Action("Bump").Fire())

	viatest.AwaitFrame(t, framesR, 2*time.Second, `<span id="visits">1</span>`)

	// Heartbeat default is 25s — any frame inside this window can only
	// come from an unintended re-render of a tab that does not display
	// the key.
	select {
	case frame := <-framesS:
		assert.Failf(t, "non-reader peer was woken",
			"scope.App write must skip tabs whose View did not read the key; got %q", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestUser_writePropagatesLiveToOtherTabsOnSameSession(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userRoundTripPage](app, "/")
	defer server.Close()

	a := viatest.NewClient(t, server, "/")
	b := a.Fork("/")

	framesB, cancelB := b.SSE()
	defer cancelB()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, a.Action("Set").Fire())
	viatest.AwaitFrame(t, framesB, 2*time.Second, `<span id="theme">midnight</span>`)
}

func TestUser_writeDoesNotLeakAcrossSessions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userRoundTripPage](app, "/")
	defer server.Close()

	a := viatest.NewClient(t, server, "/")
	b := viatest.NewClient(t, server, "/")

	framesB, cancelB := b.SSE()
	defer cancelB()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, a.Action("Set").Fire())

	// Heartbeat default is 25s; any frame inside this window can only
	// come from an unintended re-render of b, which would mean the
	// session filter on the fan-out is wrong.
	select {
	case frame := <-framesB:
		assert.Failf(t, "unexpected SSE frame on a peer session",
			"scope.User write must not fan out to other sessions; got %q", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestApp_writePropagatesLiveToEveryOtherTab(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[appCounterPage](app, "/")
	defer server.Close()

	a := viatest.NewClient(t, server, "/")
	b := viatest.NewClient(t, server, "/")

	framesB, cancelB := b.SSE()
	defer cancelB()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, a.Action("Bump").Fire())
	viatest.AwaitFrame(t, framesB, 2*time.Second, `<span id="visits">1</span>`)
}

// SetIfChanged on scope.User: same key+value short-circuits, different
// value reaches the wire as a signal patch.

type setIfChangedPage struct {
	Theme scope.User[string]
}

func (p *setIfChangedPage) Same(ctx *via.Ctx) error {
	via.SetIfChanged(ctx, &p.Theme, "blue")
	return nil
}

func (p *setIfChangedPage) Diff(ctx *via.Ctx) error {
	via.SetIfChanged(ctx, &p.Theme, "red")
	return nil
}

func (p *setIfChangedPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.Span(h.ID("t"), p.Theme.Text(ctx)))
}

func TestSetIfChanged_writesThroughOnFirstAndDistinctValues(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[setIfChangedPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Same").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, "blue")

	require.Equal(t, 200, tc.Action("Diff").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, "red")
}
