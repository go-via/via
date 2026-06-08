package via

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A transient mid-stream disconnect must NOT permanently strand a key's
// projector. When the Subscribe channel closes while the app is still running
// (the backend dropped the consumer, not a Shutdown), the projector must
// re-subscribe from its cursor and fold the rest — otherwise one network blip
// freezes a tab's state forever (the deploy-freeze class of bug the backplane
// exists to fix, reappearing one layer down).
func TestProjectorRehydratesAfterTransientDisconnect(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(dropAfter{Backplane: InMemory(), n: 2}))
	defer server.Close()
	bindLog(app, "k")
	ctx := context.Background()

	for _, n := range []int{1, 2, 3, 4, 5} {
		if _, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: n})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	// The first subscription delivers 2 records then drops; the projector must
	// reconnect (from offset 2, then 4) and fold all five.
	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 5 && p[4] == 5
	}, 3*time.Second, 10*time.Millisecond,
		"projector must reconnect after a transient disconnect and fold every record")
}

// The same resilience must hold for a side-effect consumer (OnEvent): a dropped
// subscription mid-stream must reconnect from the committed offset and deliver
// the remaining events, or a deploy blip silently loses side effects (emails,
// payments) — the failure mode OnEvent's at-least-once contract exists to
// prevent.
func TestConsumerRehydratesAfterTransientDisconnect(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(dropAfter{Backplane: InMemory(), n: 2}))
	defer server.Close()

	var hdl StateAppEvents[envEv, []int]
	hdl.bindWireKey("k")
	hdl.bindApp(app)

	var mu sync.Mutex
	var got []int
	hdl.OnEvent("sink", func(_ context.Context, ev envEv, _ Offset) error {
		mu.Lock()
		got = append(got, ev.N)
		mu.Unlock()
		return nil
	})

	ctx := context.Background()
	for _, n := range []int{1, 2, 3, 4, 5} {
		if _, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: n})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 5
	}, 3*time.Second, 10*time.Millisecond,
		"consumer must reconnect after a transient disconnect and deliver every event")
}

// A clean Shutdown must STOP a side-effect consumer too: once the backplane is
// Closed, the consumer's in-flight subscription closes and it must read that as
// a graceful stop (not a transient drop → reconnect) and exit. Without the
// shuttingDown() guard the consumer would reconnect-spin and Shutdown would hang.
func TestConsumerExitsCleanlyOnShutdown(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(dropAfter{Backplane: InMemory(), n: 2}))
	defer server.Close()

	var hdl StateAppEvents[envEv, []int]
	hdl.bindWireKey("k")
	hdl.bindApp(app)

	var mu sync.Mutex
	var got []int
	hdl.OnEvent("sink", func(_ context.Context, ev envEv, _ Offset) error {
		mu.Lock()
		got = append(got, ev.N)
		mu.Unlock()
		return nil
	})

	ctx := context.Background()
	for _, n := range []int{1, 2, 3} {
		_, _ = app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: n}))
	}
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 3
	}, 3*time.Second, 10*time.Millisecond, "consumer should deliver all before shutdown")

	done := make(chan error, 1)
	go func() { done <- app.Shutdown(context.Background()) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown hung — consumer likely reconnect-spinning instead of exiting")
	}
}

// A clean Shutdown must STOP the projector, not trigger an endless reconnect
// spin: once the backplane is Closed, Subscribe returns ErrClosed and the
// projector must exit. This pins the distinction between a transient drop
// (reconnect) and a graceful stop (exit) — without it, Shutdown would never
// quiesce.
func TestProjectorExitsCleanlyOnShutdown(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(dropAfter{Backplane: InMemory(), n: 2}))
	defer server.Close()
	bindLog(app, "k")
	ctx := context.Background()
	for _, n := range []int{1, 2, 3} {
		_, _ = app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: n}))
	}
	require.Eventually(t, func() bool { return len(projection(app, "k")) == 3 },
		3*time.Second, 10*time.Millisecond, "projector should catch up before shutdown")

	// Shutdown must return promptly and not hang on a reconnect loop.
	done := make(chan error, 1)
	go func() { done <- app.Shutdown(context.Background()) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown hung — projector likely reconnect-spinning instead of exiting")
	}
}
