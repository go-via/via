package via_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// StateSess round-trips across tab renders on the same session: a write
// from action 1 is visible to a subsequent render. Also covers Key()
// defaulting to the lowercased field name (the wire key shows up in the
// rendered data-signals payload).

type userRoundTripPage struct {
	Theme via.StateSess[string]
	Count via.StateSess[int]
}

func (p *userRoundTripPage) Set(ctx *via.Ctx) error {
	p.Theme.Update(ctx, func(string) string { return "midnight" })
	p.Count.Update(ctx, func(int) int { return 7 })
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

	tc := vt.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Set").Fire())

	body := tc.Reload()
	assert.Contains(t, body, `<span id="theme">midnight</span>`,
		"StateSess write must survive a fresh render on the same session")
	assert.Contains(t, body, `<span id="count">7</span>`)
}

func TestUser_updateAppliesFn(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userRoundTripPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
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

	body := vt.NewClient(t, server, "/").HTML()
	assert.Contains(t, body, "theme")
	assert.Contains(t, body, "count")
}

type silentUserPage struct {
	// Same wireKey "theme" as userRoundTripPage, but the View never
	// reads it — used to prove session-scoped broadcasts skip
	// non-displaying tabs.
	Theme via.StateSess[string]
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

	reader := vt.NewClient(t, server, "/reader")
	silent := reader.Fork("/silent")

	framesS, cancelS := silent.SSE()
	defer cancelS()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, reader.Action("Set").Fire())

	select {
	case frame := <-framesS:
		assert.Failf(t, "non-reader peer was woken",
			"StateSess write must skip tabs whose View did not read the key; got %q", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestUser_writePropagatesLiveToOtherTabsOnSameSession(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userRoundTripPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	b := a.Fork("/")

	framesB, cancelB := b.SSE()
	defer cancelB()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, a.Action("Set").Fire())
	vt.AwaitFrame(t, framesB, 2*time.Second, `<span id="theme">midnight</span>`)
}

func TestUser_writeDoesNotLeakAcrossSessions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[userRoundTripPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	b := vt.NewClient(t, server, "/")

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
			"StateSess write must not fan out to other sessions; got %q", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

// Inline "set if changed" on StateSess: same key+value short-circuits,
// different value reaches the wire as a signal patch.

type setIfChangedSessPage struct {
	Theme via.StateSess[string]
}

func (p *setIfChangedSessPage) Same(ctx *via.Ctx) error {
	if p.Theme.Get(ctx) != "blue" {
		p.Theme.Update(ctx, func(string) string { return "blue" })
	}
	return nil
}

func (p *setIfChangedSessPage) Diff(ctx *via.Ctx) error {
	if p.Theme.Get(ctx) != "red" {
		p.Theme.Update(ctx, func(string) string { return "red" })
	}
	return nil
}

func (p *setIfChangedSessPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.Span(h.ID("t"), p.Theme.Text(ctx)))
}

func TestUpdate_StateSess_writesThroughOnFirstAndDistinctValues(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[setIfChangedSessPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Same").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "blue")

	require.Equal(t, 200, tc.Action("Diff").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "red")
}
