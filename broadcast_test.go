package via_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
)

type broadcastPage struct{}

func (p *broadcastPage) View(ctx *via.CtxR) h.H { return h.Div() }

// openSSEStreams spins up n test clients on path and opens an SSE stream
// for each. Returns the per-client frame channels and a single cancel
// func that closes them all.
func openSSEStreams(t *testing.T, server *httptest.Server, path string, n int) (frames []<-chan string, cancel func()) {
	t.Helper()
	frames = make([]<-chan string, n)
	cancels := make([]func(), n)
	for i := range n {
		tc := vt.NewClient(t, server, path)
		frames[i], cancels[i] = tc.SSEReady()
	}
	return frames, func() {
		for _, c := range cancels {
			c()
		}
	}
}

// awaitNeedleOnAll waits for needle to appear on every frames channel.
// Channels are buffered (cap 16) so serial waits are safe — frames that
// arrive on later channels while we're waiting on earlier ones queue up.
func awaitNeedleOnAll(t *testing.T, frames []<-chan string, needle string, timeout time.Duration) {
	t.Helper()
	for _, ch := range frames {
		vt.AwaitFrame(t, ch, timeout, needle)
	}
}

// drainFor accumulates everything that arrives on ch for d, then returns it.
// Used to assert on the absence of a needle (nothing leaked) or to catch a
// stray duplicate frame after the expected one already landed.
func drainFor(ch <-chan string, d time.Duration) string {
	deadline := time.After(d)
	var b strings.Builder
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return b.String()
			}
			b.WriteString(f)
		case <-deadline:
			return b.String()
		}
	}
}

func TestBroadcast_pushesScriptToEveryLiveTab(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[broadcastPage](app, "/")

	frames, cancel := openSSEStreams(t, server, "/", 3)
	defer cancel()

	const msg = `console.log("hello broadcast")`
	assert.Equal(t, 3, app.Broadcast(msg),
		"Broadcast should report the tab count it reached")
	awaitNeedleOnAll(t, frames, msg, 2*time.Second)
}

// The canonical broadcast — a site-wide notice — needs a safe path: raw
// Broadcast forces every app to hand-build toast JS and get the XSS escaping
// right. BroadcastNotify reuses the JSON-encoded toast snippet so the message
// is safe even with markup, and reaches every live tab.
func TestBroadcastNotify_pushesAnXSSSafeToastToEveryLiveTab(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[broadcastPage](app, "/")

	frames, cancel := openSSEStreams(t, server, "/", 2)
	defer cancel()

	// A message with markup must arrive JSON/HTML-escaped, never as live HTML.
	assert.Equal(t, 2, app.BroadcastNotify("Deploy soon"),
		"BroadcastNotify should report the tab count it reached")
	// Both needles are in the one script frame (`via-toast` proves the safe
	// toast snippet, "Deploy soon" is its JSON-encoded message argument), so
	// match them in a single AwaitFrame per channel — a second call would wait
	// on a channel whose frame the first already consumed.
	for _, ch := range frames {
		vt.AwaitFrame(t, ch, 2*time.Second, "via-toast", "Deploy soon")
	}
}

func TestBroadcastSignals_pushesPatchToEveryLiveTab(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[broadcastPage](app, "/")

	frames, cancel := openSSEStreams(t, server, "/", 2)
	defer cancel()

	assert.Equal(t, 2, app.BroadcastSignals(map[string]any{
		"_systemNotice": "maintenance soon",
	}))
	awaitNeedleOnAll(t, frames, "maintenance soon", 2*time.Second)
}

func TestBroadcastSignals_emptyMapIsNoOp(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[broadcastPage](app, "/")

	_ = vt.NewClient(t, server, "/")
	assert.Equal(t, 0, app.BroadcastSignals(nil),
		"nil map should be reported as 0 tabs")
	assert.Equal(t, 0, app.BroadcastSignals(map[string]any{}),
		"empty map should be reported as 0 tabs")
}

func TestBroadcast_emptyIsNoOp(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[broadcastPage](app, "/")

	_ = vt.NewClient(t, server, "/")
	assert.Equal(t, 0, app.Broadcast(""),
		"empty script should be reported as 0 tabs")
}

// A site-wide notice must reach live tabs on EVERY pod when the app is
// clustered via a shared backplane, not just the pod the call landed on. A
// Broadcast on pod A has to surface on pod B's live tab — which only happens if
// the payload travels the backplane and B applies it from its own tailer, since
// A never touches B's tab. The originating pod's own tab is reached the same
// way (append-only: the payload is applied through the tailer, never twice).
func TestBroadcast_reachesTabsOnEveryPodWhenClustered(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()

	appA := via.New(via.WithBackplane(shared))
	serverA := vt.Serve(t, appA)
	via.Mount[broadcastPage](appA, "/")

	appB := via.New(via.WithBackplane(shared))
	serverB := vt.Serve(t, appB)
	via.Mount[broadcastPage](appB, "/")

	a := vt.NewClient(t, serverA, "/")
	framesA, cancelA := a.SSEReady()
	defer cancelA()
	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	const msg = `console.log("cluster-wide")`
	appA.Broadcast(msg)

	vt.AwaitFrame(t, framesA, 2*time.Second, msg)
	vt.AwaitFrame(t, framesB, 2*time.Second, msg)
}

// The signal-patch broadcast carries its payload across pods too: a
// BroadcastSignals on pod A must drive the same client-only signal on pod B's
// live tab, proving the values map itself rides the backplane feed.
func TestBroadcastSignals_reachTabsOnEveryPodWhenClustered(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()

	appA := via.New(via.WithBackplane(shared))
	serverA := vt.Serve(t, appA)
	via.Mount[broadcastPage](appA, "/")

	appB := via.New(via.WithBackplane(shared))
	serverB := vt.Serve(t, appB)
	via.Mount[broadcastPage](appB, "/")

	a := vt.NewClient(t, serverA, "/")
	framesA, cancelA := a.SSEReady()
	defer cancelA()
	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	appA.BroadcastSignals(map[string]any{"_systemNotice": "maintenance soon"})

	vt.AwaitFrame(t, framesA, 2*time.Second, "maintenance soon")
	vt.AwaitFrame(t, framesB, 2*time.Second, "maintenance soon")
}

// A pod that joins (boots, opens its tab) AFTER a broadcast was issued must NOT
// receive that historical notice — broadcast is best-effort and ephemeral, so
// the tailer starts at the feed HEAD rather than replaying from offset 0. A
// late tab seeing a stale "maintenance in 30s" alert would be a real bug.
//
// The test pins HEAD-vs-0 by issuing a SECOND broadcast after B has booted: B
// must receive the fresh notice (proving its tailer is live and cross-pod works
// at all) while that same frame stream never carries the stale, pre-boot one. A
// from-0 subscribe would replay the stale notice; a from-HEAD subscribe skips it.
func TestBroadcast_doesNotReplayHistoryToLateJoiners(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()

	appA := via.New(via.WithBackplane(shared))
	_ = vt.Serve(t, appA)
	via.Mount[broadcastPage](appA, "/")

	const stale = `console.log("stale notice")`
	appA.Broadcast(stale)

	// Pod B boots only now, well after A's broadcast already committed to the
	// shared feed; its fresh tab must never see the bygone notice.
	appB := via.New(via.WithBackplane(shared))
	serverB := vt.Serve(t, appB)
	via.Mount[broadcastPage](appB, "/")

	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	const fresh = `console.log("fresh notice")`
	appA.Broadcast(fresh)

	got := vt.AwaitFrame(t, framesB, 2*time.Second, fresh)
	assert.NotContains(t, got, stale,
		"a late-joining pod must receive post-boot broadcasts but never replay one issued before it booted")
}

// Append-only delivery: the originating pod applies a clustered broadcast
// THROUGH its own tailer, never also directly — so a tab on the pod that issued
// the call sees the notice exactly once. A belt-and-suspenders impl that applied
// locally AND re-applied from the tailer would double-fire every site-wide alert.
func TestBroadcast_appliesExactlyOnceOnTheOriginatingPodWhenClustered(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()

	app := via.New(via.WithBackplane(shared))
	server := vt.Serve(t, app)
	via.Mount[broadcastPage](app, "/")

	a := vt.NewClient(t, server, "/")
	frames, cancel := a.SSEReady()
	defer cancel()

	const msg = `console.log("once-only-marker")`
	app.Broadcast(msg)

	// Wait for it to land, then keep draining briefly to catch a stray second
	// copy. Exactly one occurrence across all frames is the contract.
	got := vt.AwaitFrame(t, frames, 2*time.Second, msg)
	got += drainFor(frames, 300*time.Millisecond)
	assert.Equal(t, 1, strings.Count(got, msg),
		"a clustered broadcast must reach the originating pod's tab exactly once")
}

// The "single pod by default" contract: with no shared backplane wired, a
// broadcast stays strictly pod-local — it must never leak into an unrelated App.
// This guards against a future refactor that wires a process-global backplane by
// default and silently turns every Broadcast cluster-wide.
func TestBroadcast_staysPodLocalWithoutASharedBackplane(t *testing.T) {
	t.Parallel()

	appA := via.New()
	serverA := vt.Serve(t, appA)
	via.Mount[broadcastPage](appA, "/")

	appB := via.New()
	serverB := vt.Serve(t, appB)
	via.Mount[broadcastPage](appB, "/")

	_ = vt.NewClient(t, serverA, "/")
	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	const msg = `console.log("local only")`
	appA.Broadcast(msg)

	got := drainFor(framesB, 500*time.Millisecond)
	assert.NotContains(t, got, msg,
		"a broadcast must not reach an unrelated App when no backplane is shared")
}
