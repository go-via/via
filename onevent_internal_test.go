package via

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// awaitConsumerCommitted blocks until the durable consumer checkpoint reaches
// off — so a "restart" test only swaps pods AFTER the offset is provably
// committed (not merely handled), removing the commit race.
func awaitConsumerCommitted(t *testing.T, bp Backplane, name, key string, off Offset) {
	t.Helper()
	require.Eventually(t, func() bool {
		data, _, ok, _ := bp.LoadSnapshot(context.Background(), consumerKey(name, key))
		if !ok {
			return false
		}
		var got Offset
		return json.Unmarshal(data, &got) == nil && got == off
	}, 2*time.Second, 10*time.Millisecond, "consumer offset must be durably committed")
}

// A side-effect consumer fires once per appended event, receiving the DECODED
// event and its offset (the offset is the idempotency-key seed). The effect runs
// OUTSIDE Fold, so the projection folds independently and stays pure.
func TestOnEventFiresPerAppendedEventWithOffsetAndLeavesFoldPure(t *testing.T) {
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	defer app.backplane.Close() // stop the consumer + projector tailers when the test ends
	ctx := context.Background()

	var mu sync.Mutex
	var got [][2]int // {ev.N, offset}
	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	h.OnEvent("rec", func(_ context.Context, ev envEv, off Offset) error {
		mu.Lock()
		got = append(got, [2]int{ev.N, int(off)})
		mu.Unlock()
		return nil
	})

	for i := 1; i <= 3; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i * 10}))
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 3
	}, 2*time.Second, 10*time.Millisecond, "the consumer must fire once per appended event")
	mu.Lock()
	require.Equal(t, [][2]int{{10, 1}, {20, 2}, {30, 3}}, got, "decoded event + monotone offset")
	mu.Unlock()

	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 3 && p[0] == 10 && p[2] == 30
	}, 2*time.Second, 10*time.Millisecond, "the projection folds independently of the side-effect consumer")
}

// The committed offset is durable, so a restart (a fresh pod on the same
// backplane, same consumer name) resumes from it — events whose effect already
// ran are never re-delivered.
func TestOnEventResumesFromCommittedOffsetAfterRestart(t *testing.T) {
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	defer app.backplane.Close() // stop the consumer + projector tailers when the test ends
	ctx := context.Background()

	var firstSeen int32
	var hA StateAppEvents[envEv, []int]
	hA.bindWireKey("k")
	hA.bindApp(app)
	hA.OnEvent("rec", func(_ context.Context, _ envEv, _ Offset) error {
		atomic.AddInt32(&firstSeen, 1)
		return nil
	})
	for i := 1; i <= 3; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool { return atomic.LoadInt32(&firstSeen) == 3 },
		2*time.Second, 10*time.Millisecond, "the first consumer handles all three")
	// Gate the restart on the DURABLE commit, not just the handler running —
	// otherwise appB could cold-load a stale offset and re-deliver 1..3.
	awaitConsumerCommitted(t, app.backplane, "rec", "k", 3)

	// Restart: a fresh App sharing the backplane, same consumer name.
	appB := New(WithTestServer(&server), WithBackplane(app.backplane))
	var mu sync.Mutex
	var offs []int
	var hB StateAppEvents[envEv, []int]
	hB.bindWireKey("k")
	hB.bindApp(appB)
	hB.OnEvent("rec", func(_ context.Context, _ envEv, off Offset) error {
		mu.Lock()
		offs = append(offs, int(off))
		mu.Unlock()
		return nil
	})

	_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 4}))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(offs) == 1 && offs[0] == 4
	}, 2*time.Second, 10*time.Millisecond, "the restarted consumer resumes at offset 4, skipping the committed 1..3")
}

// A handler error must NOT advance the committed offset: the record is retried
// and later events wait behind it (head-of-line), so a side effect is never
// skipped and ordering is preserved (at-least-once).
func TestOnEventRetriesFailedEventInOrderWithoutAdvancing(t *testing.T) {
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()
	defer app.backplane.Close() // stop the consumer + projector tailers when the test ends
	ctx := context.Background()

	var attemptsOn2 int32
	var mu sync.Mutex
	var handled []int
	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	h.OnEvent("rec", func(_ context.Context, _ envEv, off Offset) error {
		if off == 2 && atomic.AddInt32(&attemptsOn2, 1) < 3 {
			return fmt.Errorf("transient failure on offset 2")
		}
		mu.Lock()
		handled = append(handled, int(off))
		mu.Unlock()
		return nil
	})
	for i := 1; i <= 3; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(handled) == 3
	}, 2*time.Second, 10*time.Millisecond, "all three eventually handled")
	mu.Lock()
	require.Equal(t, []int{1, 2, 3}, handled,
		"offset 3 must not be handled before 2 succeeds — order preserved, none skipped")
	mu.Unlock()
	require.GreaterOrEqual(t, atomic.LoadInt32(&attemptsOn2), int32(3), "offset 2 retried until success")
	require.True(t, spy.saw("via.consumer.error"), "a handler error emits the consumer-error metric")
}

// A newer-version event (forward-incompatible) must NOT be skipped by a
// consumer: skipping would silently drop the side effect on a deploy-skewed pod.
// Roll-forward-only — the consumer blocks (does not advance) until a rolled-
// forward binary can decode it, mirroring the projector's halt.
func TestOnEventBlocksOnForwardIncompatibleRecord(t *testing.T) {
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()
	defer app.backplane.Close()
	ctx := context.Background()

	var handled int32
	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	h.OnEvent("rec", func(_ context.Context, _ envEv, _ Offset) error {
		atomic.AddInt32(&handled, 1)
		return nil
	})

	_, err := app.backplane.Append(ctx, "k", futureEnv(t, envEv{N: 1})) // newer than this binary
	require.NoError(t, err)

	require.Eventually(t, func() bool { return spy.saw("via.consumer.forward_incompatible") },
		2*time.Second, 10*time.Millisecond, "a forward-incompatible record emits its own metric")
	require.Never(t, func() bool { return atomic.LoadInt32(&handled) > 0 },
		300*time.Millisecond, 50*time.Millisecond,
		"the handler must never run for an undecodable-by-this-binary event")
	_, _, ok, _ := app.backplane.LoadSnapshot(ctx, consumerKey("rec", "k"))
	require.False(t, ok, "the consumer must not commit past a forward-incompatible record")
}

// Two pods running the same-named consumer both fire (at-least-once); the loser
// of the commit CAS adopts the peer's offset so the shared checkpoint converges
// and neither pod re-fires the committed offset.
func TestOnEventAdoptsPeerOffsetOnCommitConflict(t *testing.T) {
	bp := InMemory()
	defer bp.Close()
	var serverA, serverB *httptest.Server
	appA := New(WithTestServer(&serverA), WithBackplane(bp))
	defer serverA.Close()
	appB := New(WithTestServer(&serverB), WithBackplane(bp))
	defer serverB.Close()
	ctx := context.Background()

	var fires int32
	handler := func(_ context.Context, _ envEv, _ Offset) error {
		atomic.AddInt32(&fires, 1)
		return nil
	}
	var hA, hB StateAppEvents[envEv, []int]
	hA.bindWireKey("k")
	hA.bindApp(appA)
	hA.OnEvent("rec", handler)
	hB.bindWireKey("k")
	hB.bindApp(appB)
	hB.OnEvent("rec", handler)

	_, err := bp.Append(ctx, "k", goodEnv(t, envEv{N: 1}))
	require.NoError(t, err)

	// The shared checkpoint converges to 1 (one pod won the CAS, the other adopted).
	require.Eventually(t, func() bool {
		data, _, ok, _ := bp.LoadSnapshot(ctx, consumerKey("rec", "k"))
		if !ok {
			return false
		}
		var off Offset
		return json.Unmarshal(data, &off) == nil && off == 1
	}, 2*time.Second, 10*time.Millisecond, "the shared committed offset converges to 1")

	// At-least-once with per-pod dedup (rec.Offset <= committed → skip): the one
	// appended offset fires AT MOST once per pod, so total fires never exceeds 2,
	// ever — including after the CAS-loser adopts the winner's offset. Asserting
	// the absolute ceiling (not a mid-flight `settled` baseline) is race-free: a
	// baseline snapshot read before the second pod's in-flight fire would spuriously
	// trip on a legitimate second fire.
	require.Never(t, func() bool { return atomic.LoadInt32(&fires) > 2 },
		300*time.Millisecond, 50*time.Millisecond, "neither pod re-fires the committed offset (≤1 per pod)")
	require.GreaterOrEqual(t, atomic.LoadInt32(&fires), int32(1), "the event must fire at least once")
}

// A poison/undecodable record is skipped and the offset advanced (never wedging
// the consumer on a record it can never decode) — the same drop-on-undecodable
// the fold uses.
func TestOnEventSkipsPoisonRecordAndAdvances(t *testing.T) {
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()
	defer app.backplane.Close() // stop the consumer + projector tailers when the test ends
	ctx := context.Background()

	var mu sync.Mutex
	var ns []int
	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	h.OnEvent("rec", func(_ context.Context, ev envEv, _ Offset) error {
		mu.Lock()
		ns = append(ns, ev.N)
		mu.Unlock()
		return nil
	})

	_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 1}))
	require.NoError(t, err)
	_, err = app.backplane.Append(ctx, "k", []byte("not-a-valid-envelope")) // poison @offset 2
	require.NoError(t, err)
	_, err = app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 3}))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(ns) == 2
	}, 2*time.Second, 10*time.Millisecond, "the poison record is skipped, the good ones handled")
	mu.Lock()
	require.Equal(t, []int{1, 3}, ns, "the handler never sees the undecodable record")
	mu.Unlock()
	require.True(t, spy.saw("via.consumer.undecodable"), "a skipped poison record emits the consumer metric")
}

// Compaction must never discard an event a registered consumer has not yet
// processed: the Compactor floor clamps to the slowest consumer's committed
// offset. A consumer stuck at offset 0 (its handler never succeeds) pins the
// floor, so no prefix is reclaimed.
func TestCompactionFloorRespectsSlowConsumer(t *testing.T) {
	var server *httptest.Server
	app := New(WithTestServer(&server), WithSnapshotInterval(1))
	defer server.Close()
	defer app.backplane.Close() // stop the consumer + projector tailers when the test ends
	ctx := context.Background()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	h.OnEvent("stuck", func(_ context.Context, _ envEv, _ Offset) error {
		return fmt.Errorf("never commits") // committed offset stays 0
	})
	for i := 1; i <= 6; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}

	// The Never condition must be a pure predicate with NO require/t.Fatal: a
	// late poll-tick can fire after the test body returns and closes the
	// backplane (defer above), and a require/t.Fatal from that goroutine panics
	// with "Fail in goroutine after test completed". lowestRetained tolerates a
	// closed backplane by reporting 0 (nothing retained beyond the floor).
	lowestRetained := func() Offset {
		c, cancel := context.WithCancel(context.Background())
		defer cancel()
		sub, err := app.backplane.Subscribe(c, "k", 0)
		if err != nil {
			return 0 // backplane closed (teardown) → treat as nothing retained
		}
		select {
		case r, ok := <-sub:
			if !ok {
				return 0
			}
			return r.Offset
		case <-time.After(time.Second):
			return 0
		}
	}
	require.Never(t, func() bool {
		return lowestRetained() > 1
	}, 600*time.Millisecond, 50*time.Millisecond,
		"compaction must not pass the slow consumer's committed offset")
}

// The clamp is min(snapshot floor, consumer offset) — NOT "block forever". A
// consumer that keeps up advances its committed offset, so compaction proceeds:
// proving the floor tracks the consumer rather than pinning at genesis.
func TestCompactionAdvancesWhenConsumerKeepsUp(t *testing.T) {
	var server *httptest.Server
	app := New(WithTestServer(&server), WithSnapshotInterval(1))
	defer server.Close()
	defer app.backplane.Close()
	ctx := context.Background()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	h.OnEvent("keeps-up", func(_ context.Context, _ envEv, _ Offset) error {
		return nil // commits every offset → floor follows it
	})
	for i := 1; i <= 6; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	// Compaction is driven by snapshot writes (one per fold); after the last fold
	// no further compaction is attempted. So barrier on the consumer durably
	// committing the whole prefix FIRST — then a single extra append drives one
	// more snapshot+compact whose floor reflects the now-caught-up consumer,
	// making the assertion deterministic rather than racing the consumer's
	// async commit against the final snapshot.
	awaitConsumerCommitted(t, app.backplane, "keeps-up", "k", 6)
	if _, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 7})); err != nil {
		t.Fatalf("append: %v", err)
	}

	require.Eventually(t, func() bool {
		return lowestRetainedOffset(t, app.backplane, "k") > 1
	}, 2*time.Second, 10*time.Millisecond,
		"a keeping-up consumer must not pin the compaction floor at genesis")
}
