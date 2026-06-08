package via

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gaugeSpy captures Gauge(name,value,labels...) calls so the fold-divergence
// canary can be asserted: the (key, offset, digest) triple a pod emits after
// each fold is the cheap cross-pod divergence signal (council T1-SRE-7).
type gaugeSpy struct {
	mu     sync.Mutex
	gauges []gaugeSample
}

type gaugeSample struct {
	name   string
	value  float64
	labels []string
}

func (g *gaugeSpy) Counter(string, ...string) {}
func (g *gaugeSpy) Gauge(name string, value float64, labels ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gauges = append(g.gauges, gaugeSample{name, value, append([]string(nil), labels...)})
}
func (g *gaugeSpy) Histogram(string, float64, ...string) {}

// latest returns the value of the most recent gauge sample named `name` whose
// labels contain key=wantKey, plus whether any was seen.
func (g *gaugeSpy) latest(name, wantKey string) (float64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var v float64
	found := false
	for _, s := range g.gauges {
		if s.name != name {
			continue
		}
		for i := 0; i+1 < len(s.labels); i += 2 {
			if s.labels[i] == "key" && s.labels[i+1] == wantKey {
				v, found = s.value, true
			}
		}
	}
	return v, found
}

// latestLabel returns the value of label `want` on the most recent gauge sample
// named `name` whose labels contain key=wantKey, plus whether such a sample was
// seen carrying that label.
func (g *gaugeSpy) latestLabel(name, wantKey, want string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var got string
	found := false
	for _, s := range g.gauges {
		if s.name != name {
			continue
		}
		hasKey := false
		var label string
		hasLabel := false
		for i := 0; i+1 < len(s.labels); i += 2 {
			switch s.labels[i] {
			case "key":
				hasKey = s.labels[i+1] == wantKey
			case want:
				label, hasLabel = s.labels[i+1], true
			}
		}
		if hasKey && hasLabel {
			got, found = label, true
		}
	}
	return got, found
}

func foldKEvents(t *testing.T, gs *gaugeSpy, key string, ns ...int) (float64, float64) {
	t.Helper()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(gs))
	t.Cleanup(server.Close)
	bindLog(app, key)
	ctx := context.Background()
	for _, n := range ns {
		if _, err := app.backplane.Append(ctx, key, goodEnv(t, envEv{N: n})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	require.Eventually(t, func() bool { return len(projection(app, key)) == len(ns) },
		2*time.Second, 10*time.Millisecond, "all events must fold")
	off, oko := gs.latest("via.fold.offset", key)
	dig, okd := gs.latest("via.fold.digest", key)
	require.True(t, oko, "projector must emit via.fold.offset after folding")
	require.True(t, okd, "projector must emit via.fold.digest after folding")
	return off, dig
}

// The fold-divergence canary is the cheap cross-pod safety net: after every fold
// a pod emits its applied offset AND a digest of the resulting projection. Two
// pods folding the SAME event sequence MUST report the same (offset, digest), so
// an operator comparing the two gauges across pods can detect a non-deterministic
// fold before it corrupts a snapshot. So the digest must be a pure function of
// the folded projection — identical inputs → identical digest.
func TestFoldDigestIsDeterministicForTheSameSequence(t *testing.T) {
	t.Parallel()
	off1, dig1 := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 3)
	off2, dig2 := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 3)
	require.Equal(t, off1, off2, "same sequence → same applied offset")
	require.Equal(t, dig1, dig2, "same sequence → identical projection digest")
	require.NotZero(t, off1)
}

// A DIFFERENT projection at the SAME offset must produce a DIFFERENT digest, or
// the canary is useless — it would report agreement even when two pods diverged.
// Both sequences fold three events (→ offset 3), so a digest that merely echoes
// the offset would compare equal here and be rejected.
func TestFoldDigestDiffersForDifferentProjections(t *testing.T) {
	t.Parallel()
	offA, digA := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 3)
	offB, digB := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 4)
	require.Equal(t, offA, offB, "both fold three events → same applied offset 3")
	require.NotEqual(t, digA, digB, "different projections at the same offset must yield different digests")
}

// The digest gauge must carry an "offset" label matching the applied cursor:
// the canary triple is (key, offset, digest), and an operator correlates a
// cross-pod digest MISMATCH to the exact offset it occurred at. A digest gauge
// without the offset label would force comparing two unanchored hash streams.
func TestFoldDigestGaugeCarriesOffsetLabel(t *testing.T) {
	t.Parallel()
	gs := &gaugeSpy{}
	off, _ := foldKEvents(t, gs, "k", 1, 2, 3)
	gotOff, ok := gs.latestLabel("via.fold.digest", "k", "offset")
	require.True(t, ok, "via.fold.digest must carry an offset label")
	require.Equal(t, strconv.FormatUint(uint64(off), 10), gotOff,
		"digest offset label must match the applied cursor (the via.fold.offset gauge)")
}

// Within a single pod the digest must track the projection as it grows — a
// digest that hashes only part of the state (or a constant) would not change as
// events accumulate, blinding the canary to a fold that stopped advancing.
func TestFoldDigestTracksProjectionGrowth(t *testing.T) {
	t.Parallel()
	off2, dig2 := foldKEvents(t, &gaugeSpy{}, "k", 1, 2)
	off3, dig3 := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 3)
	require.NotEqual(t, off2, off3, "offset must advance as events accumulate")
	require.NotEqual(t, dig2, dig3, "digest must change as the projection grows")
}

// recvOffset reads one record off a subscription within a timeout, returning its
// offset — so a test can assert WHICH offset a compacted log resumes at.
func recvOffset(t *testing.T, sub <-chan Record) Offset {
	t.Helper()
	select {
	case r := <-sub:
		return r.Offset
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a record")
		return 0
	}
}

// lowestRetainedOffset subscribes from genesis and returns the FIRST delivered
// record's offset — the lowest offset the log still holds after any compaction
// (0 if the log delivered nothing within the window).
func lowestRetainedOffset(t *testing.T, bp Backplane, key string) Offset {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := bp.Subscribe(ctx, key, 0)
	if err != nil {
		// A torn-down backplane (Close during a require.Never/Eventually poll,
		// whose check fn runs in a straggler goroutine that can outlive the test
		// body's deferred Close) retains nothing — report 0 rather than failing
		// the test from inside the polled goroutine.
		return 0
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

// nonCompactingBackplane embeds the Backplane interface but NOT Compact, so the
// runtime's type-assert to Compactor fails — modelling a backend that declines
// compaction and must still snapshot.
type nonCompactingBackplane struct{ Backplane }

// Compaction reclaims the prefix a snapshot already covers, but retained records
// MUST keep their original offsets — a resuming pod that passed offset N must
// still resume exactly after N. Re-numbering the survivors to 1 would silently
// re-deliver or skip events.
func TestCompactDropsPrefixButKeepsRetainedOffsetsStable(t *testing.T) {
	t.Parallel()
	b := InMemory()
	defer b.Close()
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		off, err := b.Append(ctx, "k", []byte{byte(i)})
		require.NoError(t, err)
		require.Equal(t, Offset(i), off)
	}
	c, ok := b.(Compactor)
	require.True(t, ok, "the in-memory backplane must offer compaction")
	require.NoError(t, c.Compact(ctx, "k", 4)) // discard offsets < 4 (1,2,3)

	// Head is unchanged — compaction reclaims the prefix, never the head.
	head, _, err := b.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, Offset(5), head)

	// A genesis subscriber resumes at the lowest RETAINED offset, gap-free, with
	// offsets UNCHANGED (4, 5) — never renumbered to 1, 2.
	sub, err := b.Subscribe(ctx, "k", 0)
	require.NoError(t, err)
	require.Equal(t, Offset(4), recvOffset(t, sub))
	require.Equal(t, Offset(5), recvOffset(t, sub))
}

// A re-issued, stale, or over-large beforeOffset must never corrupt the log or
// move the head — compaction is idempotent and clamped to committed offsets.
func TestCompactIsIdempotentAndClampedToCommitted(t *testing.T) {
	t.Parallel()
	b := InMemory()
	defer b.Close()
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		_, err := b.Append(ctx, "k", []byte{byte(i)})
		require.NoError(t, err)
	}
	c := b.(Compactor)
	require.NoError(t, c.Compact(ctx, "k", 0))   // before:0 (first-snapshot floor) → no-op
	require.NoError(t, c.Compact(ctx, "k", 2))   // drop offset 1
	require.NoError(t, c.Compact(ctx, "k", 2))   // again → no-op
	require.NoError(t, c.Compact(ctx, "k", 1))   // below the floor → no-op
	require.NoError(t, c.Compact(ctx, "k", 999)) // beyond head → clamp to committed

	head, _, err := b.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, Offset(3), head, "head must never move under compaction")
}

// Offsets are a monotone resume primitive: an append AFTER compaction must
// continue from the head, never restart base-relative. A renumbered offset would
// let a resuming pod silently re-process or skip events.
func TestAppendAfterCompactContinuesMonotoneOffsets(t *testing.T) {
	t.Parallel()
	b := InMemory()
	defer b.Close()
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		_, err := b.Append(ctx, "k", []byte{byte(i)})
		require.NoError(t, err)
	}
	require.NoError(t, b.(Compactor).Compact(ctx, "k", 4)) // drop 1..3, retain 4,5

	off, err := b.Append(ctx, "k", []byte{6})
	require.NoError(t, err)
	require.Equal(t, Offset(6), off, "append after compaction must continue from the head, not restart")

	sub, err := b.Subscribe(ctx, "k", 0)
	require.NoError(t, err)
	require.Equal(t, Offset(4), recvOffset(t, sub))
	require.Equal(t, Offset(5), recvOffset(t, sub))
	require.Equal(t, Offset(6), recvOffset(t, sub))
}

// Compaction is monotone: the retained floor only ever rises. A stale or
// out-of-order beforeOffset must not move the floor backward (which would
// "resurrect" discarded offsets) or move the head.
func TestSequentialCompactionsAdvanceMonotonically(t *testing.T) {
	t.Parallel()
	b := InMemory()
	defer b.Close()
	ctx := context.Background()
	for i := 1; i <= 10; i++ {
		_, err := b.Append(ctx, "k", []byte{byte(i)})
		require.NoError(t, err)
	}
	c := b.(Compactor)
	require.NoError(t, c.Compact(ctx, "k", 4))
	require.Equal(t, Offset(4), lowestRetainedOffset(t, b, "k"))
	require.NoError(t, c.Compact(ctx, "k", 7))
	require.Equal(t, Offset(7), lowestRetainedOffset(t, b, "k"))
	require.NoError(t, c.Compact(ctx, "k", 5)) // backward → no-op
	require.Equal(t, Offset(7), lowestRetainedOffset(t, b, "k"), "the floor must never move backward")

	head, _, err := b.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, Offset(10), head, "head is invariant under compaction")
}

// The projector reclaims storage automatically, but compaction must trail a
// DURABLE snapshot (snapshot-FIRST, compact-SECOND) — it must never discard at
// or beyond the covered offset, or a cold start could not resume.
func TestProjectorAutoCompactsTrailingTheDurableSnapshot(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithSnapshotInterval(1))
	defer server.Close()
	ctx := context.Background()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}

	// Compaction ran: the lowest retained offset rose above genesis.
	require.Eventually(t, func() bool {
		return lowestRetainedOffset(t, app.backplane, "k") > 1
	}, 2*time.Second, 10*time.Millisecond, "the projector must compact the prefix")

	head, _, err := app.backplane.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, Offset(5), head)

	// The durable snapshot's covered offset is never compacted away (the floor
	// lags one generation), so a cold start can always resume from it.
	data, _, ok, _ := app.backplane.LoadSnapshot(ctx, snapKey("k"))
	require.True(t, ok)
	var cp checkpoint
	require.NoError(t, json.Unmarshal(data, &cp))
	require.LessOrEqual(t, lowestRetainedOffset(t, app.backplane, "k"), cp.CoveredOffset,
		"compaction must never pass the durable snapshot's covered offset")
}

// The payoff: a fresh pod sharing a backplane whose prefix has been compacted
// still cold-starts to the full projection — it seeds from the snapshot and
// never needs the discarded events.
func TestFreshProjectorColdStartsAfterPrefixCompacted(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithSnapshotInterval(1))
	defer server.Close()
	ctx := context.Background()

	var hA StateAppEvents[envEv, []int]
	hA.bindWireKey("k")
	hA.bindApp(app)
	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool {
		return lowestRetainedOffset(t, app.backplane, "k") > 1
	}, 2*time.Second, 10*time.Millisecond, "the prefix must be compacted before the fresh pod starts")

	appB := New(WithTestServer(&server), WithBackplane(app.backplane))
	var hB StateAppEvents[envEv, []int]
	hB.bindWireKey("k")
	hB.bindApp(appB)

	require.Eventually(t, func() bool {
		p := projection(appB, "k")
		return len(p) == 5 && p[0] == 1 && p[4] == 5
	}, 2*time.Second, 10*time.Millisecond,
		"the fresh pod must reach the full projection from the snapshot despite compaction")
}

// A backend that declines Compactor must still snapshot — the runtime falls back
// to snapshot-only and never panics or wedges, leaving the prefix intact.
func TestSnapshotOnlyWhenBackendDeclinesCompaction(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	bp := nonCompactingBackplane{InMemory()}
	app := New(WithTestServer(&server), WithBackplane(bp), WithSnapshotInterval(1))
	defer server.Close()
	ctx := context.Background()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		_, _, ok, _ := app.backplane.LoadSnapshot(ctx, snapKey("k"))
		return ok
	}, 2*time.Second, 10*time.Millisecond, "snapshot must still be written")
	require.Equal(t, Offset(1), lowestRetainedOffset(t, app.backplane, "k"),
		"a backend without Compactor leaves the prefix intact")
}

// gappedOffsets wraps a Backplane and rewrites each delivered record's offset to
// a NON-CONTIGUOUS sequence (×stride), modelling a backend (e.g. NATS JetStream)
// whose per-key offsets are a GLOBAL stream sequence with gaps once several keys
// share one stream. Offsets stay strictly increasing and nothing is lost — the
// "gaps" are simply other subjects' sequence numbers.
type gappedOffsets struct {
	Backplane
	stride Offset
}

// Precondition: from is always 0 (cold start) or a previously-delivered (×stride)
// offset, so from/stride inverts the rewrite exactly.
func (g gappedOffsets) Subscribe(ctx context.Context, key string, from Offset) (<-chan Record, error) {
	in, err := g.Backplane.Subscribe(ctx, key, from/g.stride)
	if err != nil {
		return nil, err
	}
	out := make(chan Record)
	go func() {
		defer close(out)
		for r := range in {
			r.Offset *= g.stride
			select {
			case out <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// A backend whose per-key offsets are non-contiguous (a shared JetStream stream
// sequenced across subjects) must NOT be mistaken for a compacted log: with no
// compaction — hence no snapshot — every event must still fold. This is the
// viashowcase multi-log bug, where the projector halted on the very first event
// (offset > 1) and silently dropped them all.
func TestProjectorFoldsNonContiguousOffsetsWhenNothingWasCompacted(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(gappedOffsets{Backplane: InMemory(), stride: 3}))
	defer server.Close()
	bindLog(app, "k")
	ctx := context.Background()

	for _, n := range []int{1, 2, 3, 4, 5} {
		if _, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: n})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 5 && p[4] == 5
	}, 3*time.Second, 10*time.Millisecond,
		"non-contiguous offsets with no compaction must fold every record, not halt on the first gap")
	require.False(t, app.logs["k"].halted, "a benign non-contiguous gap must not halt the projector")
}

// A gap with NO snapshot at all cannot be a lost prefix: compaction always writes
// a snapshot FIRST (snapshot-FIRST invariant), so no snapshot ⇒ no compaction.
// The record must fold, not halt.
func TestGapWithNoSnapshotFoldsInsteadOfHalting(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "no snapshot ⇒ no compaction ⇒ the record must fold")
	require.False(t, ls.halted)
	require.False(t, spy.saw("via.events.compaction_gap_halt"))
}

// An UNCOMPACTED snapshot that cannot bridge a gap is a disposable cache, not
// evidence of a discarded prefix → benign fold, never halt.
func TestGapWithUncompactedUnbridgeableSnapshotFolds(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Forward of cur (5) but short of needCovered (8); UNCOMPACTED → just a cache.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 6, CodecHash: "h", V: mustJSON(6), Compacted: false})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "an uncompacted snapshot that can't bridge is a cache, not a lost prefix → fold")
	require.False(t, ls.halted)
}

// Safety preserved: a COMPACTED snapshot that cannot bridge a gap means a real
// prefix was discarded → HALT rather than silently truncate to the surviving tail.
func TestGapWithCompactedUnbridgeableSnapshotStillHalts(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 6, CodecHash: "h", V: mustJSON(6), Compacted: true})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.False(t, advanced)
	require.True(t, ls.halted, "a compacted prefix we can't bridge must halt, never truncate")
	require.True(t, spy.saw("via.events.compaction_gap_halt"))
}

// countingSnapshots counts LoadSnapshot calls, so a test can prove the projector
// does NOT re-read a snapshot on every non-contiguous record.
type countingSnapshots struct {
	Backplane
	loads *int32
}

func (c countingSnapshots) LoadSnapshot(ctx context.Context, key string) ([]byte, Rev, bool, error) {
	atomic.AddInt32(c.loads, 1)
	return c.Backplane.LoadSnapshot(ctx, key)
}

// An UNREADABLE snapshot (corrupt bytes) is treated as no usable snapshot — the
// same as cold start, which ignores a snapshot it cannot unmarshal. A gap behind
// it is benign (a disposable cache can't prove a lost prefix), so fold, not halt.
func TestGapWithUnreadableSnapshotFolds(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	if _, err := app.backplane.CAS(context.Background(), snapKey("k"), 0, []byte("not-json")); err != nil {
		t.Fatalf("seed corrupt snapshot: %v", err)
	}
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "an unreadable snapshot is not evidence of compaction → fold")
	require.False(t, ls.halted)
}

// A snapshot that bridges the gap and matches the codec but whose payload fails
// to DECODE is an unusable cache; if it is UNCOMPACTED no prefix was lost, so the
// gap is benign (fold), mirroring cold start's re-fold-from-genesis on a decode
// failure rather than halting.
func TestGapWithUndecodableUncompactedSnapshotFolds(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Bridges (CoveredOffset 8 >= needCovered 8, > cur 5), codec matches, but V is
	// a JSON string — decodeSnap (unmarshal into the int counter) fails; UNCOMPACTED → benign.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 8, CodecHash: "h", V: mustJSON("not-an-int"), Compacted: false})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "an undecodable uncompacted snapshot is a cache, not a lost prefix → fold")
	require.False(t, ls.halted)
}

// Once a benign gap is seen the projector must keep folding subsequent
// non-contiguous records WITHOUT re-reading a snapshot or re-halting — a busy
// shared stream produces a gap on essentially every record, so a per-record
// LoadSnapshot would be a real cost. The latch must read the snapshot at most
// once.
func TestProjectorKeepsFoldingAfterFirstBenignGapWithoutRereadingSnapshot(t *testing.T) {
	t.Parallel()
	var loads int32
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(countingSnapshots{Backplane: InMemory(), loads: &loads}))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	for _, off := range []Offset{9, 13, 21, 40} { // all non-contiguous
		require.True(t, app.applyRecord(ls, "k", Record{Key: "k", Offset: off, Data: goodBenchEnv()}),
			"record at offset %d must fold", off)
		require.False(t, ls.halted)
	}
	require.LessOrEqual(t, atomic.LoadInt32(&loads), int32(1),
		"a non-contiguous backend must not pay a LoadSnapshot per record — the benign-gap latch must hold")
}

// dropAfter wraps a Backplane and closes each Subscribe channel after delivering
// n records, WITHOUT closing the backplane itself — a transient connection drop
// (the JetStream OrderedConsumer dies, the stream survives). The underlying
// stream is intact, so a runtime that re-subscribes from its cursor must resume
// gap-free. It models the mid-Subscribe-disconnect fault the council's keystone
// requires (reconnect-rehydrate, #3/#7).
type dropAfter struct {
	Backplane
	n int
}

func (d dropAfter) Subscribe(ctx context.Context, key string, from Offset) (<-chan Record, error) {
	in, err := d.Backplane.Subscribe(ctx, key, from)
	if err != nil {
		return nil, err
	}
	out := make(chan Record)
	go func() {
		defer close(out)
		sent := 0
		for r := range in {
			select {
			case out <- r:
			case <-ctx.Done():
				return
			}
			if sent++; sent >= d.n {
				return // drop the stream after n records (out closes)
			}
		}
	}()
	return out, nil
}

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

// manualLogState wires a logState for benchEv/[]int-style int-counter folds
// WITHOUT starting the live projector goroutine, so applyRecord can be driven
// deterministically (no race with a background tailer).
func manualLogState(app *App, key string, cursor Offset, codecHash string) *logState {
	ls := &logState{
		projection: int(cursor), // counter fold: projection == events folded so far
		seed:       0,
		cursor:     cursor,
		epochSeen:  true,
		codecHash:  codecHash,
		foldBytes: func(acc any, data []byte) (any, error) {
			ev, err := decodeEvent[benchEv](data, nil)
			if err != nil {
				return acc, err
			}
			cur, _ := acc.(int)
			return ev.Fold(cur, ev), nil
		},
		decodeSnap: func(b []byte) (any, error) {
			var v int
			if err := json.Unmarshal(b, &v); err != nil {
				return nil, err
			}
			return v, nil
		},
		encodeSnap: func(p any) ([]byte, error) { v, _ := p.(int); return json.Marshal(v) },
	}
	app.logsMu.Lock()
	app.logs[key] = ls
	app.logsMu.Unlock()
	return ls
}

func writeSnap(t *testing.T, app *App, key string, cp checkpoint) {
	t.Helper()
	b, _ := json.Marshal(cp)
	_, err := app.backplane.CAS(context.Background(), snapKey(key), 0, b)
	require.NoError(t, err)
}

// When compaction overtakes a live projector — it has folded only up to cursor C
// but the log has dropped records and the next delivered record has an offset
// well beyond C+1 (a GAP) — the projector must RE-SEED from the durable snapshot
// (which covers the gap) before folding, NOT fold the post-gap record onto its
// stale projection. Folding onto the stale value silently and permanently
// diverges this pod from its peers (the cross-pod truncation bug the multi-node
// load test surfaced).
func TestProjectorReseedsFromSnapshotOnCompactionGap(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	// Projector has folded up to offset 5 (projection == 5).
	ls := manualLogState(app, "k", 5, "h")
	// A durable snapshot covers offset 8 with the correct folded value (8).
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 8, CodecHash: "h", V: mustJSON(8)})

	// The next delivered record is offset 9 — a GAP (offsets 6,7,8 were dropped
	// by compaction and never folded by this projector).
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "folding the post-reseed record advances")
	require.Equal(t, 9, ls.projection, "must re-seed to the snapshot (8) then fold offset 9 → 9, NOT stale-fold 5→6")
	require.Equal(t, Offset(9), ls.cursor)
	require.True(t, spy.saw("via.events.compaction_reseed"), "a compaction-gap reseed must be metered")
}

// If compaction overtakes the projector but NO snapshot can recover the gap
// (missing, or a codec mismatch that can't be bridged live), the projector must
// HALT (roll-forward-only) rather than fold onto a stale projection — fail safe,
// never silently diverge.
func TestProjectorHaltsOnCompactionGapWithoutRecoverableSnapshot(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Snapshot is COMPACTED (a prefix was discarded) but under a DIFFERENT codec
	// hash → cannot bridge live; a genuine lost prefix must halt, not truncate.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 8, CodecHash: "OTHER", V: mustJSON(8), Compacted: true})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.False(t, advanced)
	require.Equal(t, 5, ls.projection, "must NOT fold onto the stale projection")
	require.True(t, ls.halted, "an unrecoverable compaction gap halts the projector")
	require.True(t, spy.saw("via.events.compaction_gap_halt"))
}

// A contiguous record (offset == cursor+1) is the normal case: fold directly, no
// reseed, no snapshot read.
func TestProjectorFoldsContiguousRecordWithoutReseed(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 6, Data: goodBenchEnv()})

	require.True(t, advanced)
	require.Equal(t, 6, ls.projection, "contiguous fold: 5 → 6")
	require.False(t, spy.saw("via.events.compaction_reseed"), "no reseed on a contiguous record")
}

func goodBenchEnv() []byte { return envFor(benchEv{N: 1}) }

// Boundary: the snapshot already covers the INCOMING record (CoveredOffset ==
// rec.Offset). The reseed must still fire (it bridges the gap and moves forward),
// but the post-reseed projectRecord must DEDUP the record (rec.Offset <= cursor)
// rather than double-fold it — the projection lands exactly at the snapshot value.
func TestProjectorReseedDedupsWhenSnapshotCoversIncomingRecord(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Snapshot covers offset 9 — the very record about to be delivered.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 9, CodecHash: "h", V: mustJSON(9)})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.False(t, advanced, "the incoming record is already in the snapshot → no advance, no broadcast")
	require.Equal(t, 9, ls.projection, "projection lands at the snapshot value, NOT folded to 10")
	require.Equal(t, Offset(9), ls.cursor)
	require.True(t, spy.saw("via.events.compaction_reseed"), "the reseed itself still fires")
}

// Boundary: a snapshot that moves forward (CoveredOffset > cur) but does NOT
// bridge the whole gap (CoveredOffset < rec.Offset-1) must HALT, not partial-seed
// and fold — folding rec onto a projection short of the gap still diverges.
func TestProjectorHaltsWhenSnapshotDoesNotBridgeWholeGap(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Gap is 6..19 (rec at 20). COMPACTED snapshot only covers 12 — forward of cur
	// (5) but short of needCovered (19): folding 20 onto 12 would skip 13..19, and
	// the discarded prefix means we cannot recover them → halt.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 12, CodecHash: "h", V: mustJSON(12), Compacted: true})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 20, Data: goodBenchEnv()})

	require.False(t, advanced)
	require.Equal(t, 5, ls.projection, "must NOT partial-seed to 12 and fold — that skips 13..19")
	require.True(t, ls.halted, "a snapshot that does not bridge the WHOLE gap halts")
	require.True(t, spy.saw("via.events.compaction_gap_halt"))
}

// A FRESH projector (cursor 0, nothing folded yet) whose FIRST delivered record
// has an offset > 1 means the log's prefix was compacted before its subscriber
// first read — it must re-seed from the snapshot, NOT fold offset N onto the
// seed (which would set proj=1,cursor=N and diverge by N-1 forever). This is the
// exact multi-node truncation the load test caught: a slow pod whose first read
// races a peer's compaction.
func TestFreshProjectorReseedsWhenFirstRecordIsPostCompaction(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 0, "h") // fresh: cursor 0, projection 0, never folded
	ls.epochSeen = false
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 255, CodecHash: "h", V: mustJSON(255)})

	// First delivered record is offset 256 (offsets 1..255 were compacted away).
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 256, Data: goodBenchEnv()})

	require.True(t, advanced)
	require.Equal(t, 256, ls.projection, "must re-seed to 255 then fold offset 256 → 256, NOT fold onto seed → 1")
	require.Equal(t, Offset(256), ls.cursor)
	require.True(t, spy.saw("via.events.compaction_reseed"))
}

// The shared snapshot cell must be MONOTONIC in CoveredOffset: a lagging pod must
// not overwrite a leader's higher-covered snapshot with a lower one. If it could,
// compaction (which trusts the durable snapshot to cover the prefix it drops) and
// a peer's gap-reseed would recover a snapshot that doesn't bridge the gap.
func TestSnapshotWriteNeverRegressesCoveredOffset(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	ctx := context.Background()

	// A leader's snapshot already covers offset 100.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 100, CodecHash: "h", V: mustJSON(100)})

	// A lagging pod (cursor 40) attempts to write — must NOT regress the cell.
	ls := manualLogState(app, "k", 40, "h")
	app.writeSnapshot(ls, "k")

	data, _, ok, _ := app.backplane.LoadSnapshot(ctx, snapKey("k"))
	require.True(t, ok)
	var cp checkpoint
	require.NoError(t, json.Unmarshal(data, &cp))
	require.Equal(t, Offset(100), cp.CoveredOffset, "a lagging pod must not regress the shared snapshot's CoveredOffset")

	// A pod that genuinely advances it (cursor 150) DOES write.
	ls2 := manualLogState(app, "k", 150, "h")
	app.writeSnapshot(ls2, "k")
	data, _, _, _ = app.backplane.LoadSnapshot(ctx, snapKey("k"))
	require.NoError(t, json.Unmarshal(data, &cp))
	require.Equal(t, Offset(150), cp.CoveredOffset, "a higher-covered snapshot DOES advance the cell")
}

// When a key's underlying stream is recreated / trimmed-to-empty / restored, its
// offset space restarts at 1 under a NEW epoch. A bare offset high-water-mark
// would then skip every new record (their offsets are <= the old cursor),
// silently freezing the projection. The projector must DETECT the epoch change
// and re-snapshot from genesis so the projection re-converges — emitting
// via.events.epoch_reset.
func TestProjectorReSnapshotsOnEpochReset(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	ls := app.logs["k"]

	// Two records in the original epoch (0) fold normally.
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 0, Offset: 1, Data: goodEnv(t, envEv{N: 1})})
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 0, Offset: 2, Data: goodEnv(t, envEv{N: 2})})
	require.Equal(t, []int{1, 2}, projection(app, "k"), "same-epoch records fold normally")
	require.False(t, spy.saw("via.events.epoch_reset"), "the baseline epoch must not be mistaken for a reset")

	// The stream resets: a NEW epoch (1) whose offsets restart at 1. This MUST
	// re-snapshot from genesis — the projection becomes just the new-epoch event,
	// NOT [1,2,9] (which a bare HWM that appended would produce) and NOT frozen at
	// [1,2] (which a bare HWM that skipped offset 1 <= cursor 2 would produce).
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 1, Offset: 1, Data: goodEnv(t, envEv{N: 9})})
	require.Equal(t, []int{9}, projection(app, "k"), "an epoch reset must re-snapshot from genesis")
	require.Equal(t, Offset(1), logCursor(app, "k"), "cursor restarts in the new epoch")
	require.True(t, spy.saw("via.events.epoch_reset"), "an offset-space reset must emit via.events.epoch_reset")

	// Subsequent new-epoch records fold onto the re-snapshotted projection.
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 1, Offset: 2, Data: goodEnv(t, envEv{N: 10})})
	require.Equal(t, []int{9, 10}, projection(app, "k"), "new-epoch records fold after the reset")

	// A reset is detected by ANY epoch change, not only a forward bump — a
	// restore can roll the epoch backward and still restart the offset space.
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 0, Offset: 1, Data: goodEnv(t, envEv{N: 5})})
	require.Equal(t, []int{5}, projection(app, "k"), "a backward epoch change also re-snapshots from genesis")
}

// goodEnvBytes builds a current-version envelope for envEv{N:n} without a
// *testing.T, so it can seed the fuzz corpus and the cross-process replay.
func goodEnvBytes(n int) []byte {
	d, _ := json.Marshal(envEv{N: n})
	b, err := json.Marshal(eventEnvelope{T: "envEv", V: currentEventVersion, D: d})
	if err != nil {
		panic(err)
	}
	return b
}

// Fold determinism is the #1 correctness risk of the event-log model: if a
// reducer is impure (reads time, a package global, a map iteration order, an
// RNG) two pods replaying the same log diverge, and a snapshot crystallizes the
// divergence permanently. WithFoldVerify is same-process and can't catch a
// reducer that reads cross-process state. These two tests are the gates the
// council mandated (T1-TEST-1): a fuzz that folding the SAME bytes is
// deterministic + never panics, and a SUBPROCESS replay that two independent
// processes fold a fixed log to the identical digest.

// FuzzFoldIsDeterministicAndNeverPanics drives arbitrary bytes through the real
// projector decode+fold path. The decode must classify every input (fold,
// ErrUndecodable, or ErrForwardIncompatible) WITHOUT panicking — a poison record
// must never crash a pod — and folding the same bytes from the same accumulator
// twice must yield the identical result and error (purity). A reducer that read
// a clock or RNG would fail the second-fold equality.
func FuzzFoldIsDeterministicAndNeverPanics(f *testing.F) {
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	fold := bindLog(app, "k")

	// Seed with well-formed envelopes and assorted poison.
	for _, n := range []int{0, 1, 7, -3, 1000} {
		f.Add(goodEnvBytes(n))
	}
	f.Add([]byte("garbage"))
	f.Add([]byte(`{"t":"envEv","v":99,"d":{"N":1}}`)) // forward-incompatible
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		acc := []int{1, 2, 3}
		r1, e1 := fold(append([]int(nil), acc...), data)
		r2, e2 := fold(append([]int(nil), acc...), data)

		// Same input → same error classification.
		require.Equal(t, e1 == nil, e2 == nil, "fold error determinism for %q", data)
		if e1 != nil {
			require.Equal(t, e1.Error(), e2.Error(), "fold error must be deterministic for %q", data)
			return
		}
		// Same input + same acc → identical projection (purity).
		got1, _ := r1.([]int)
		got2, _ := r2.([]int)
		require.Equal(t, got1, got2, "fold must be deterministic for %q", data)
	})
}

// TestFoldConvergesAcrossProcesses replays a fixed event log in two SEPARATE OS
// processes and asserts both reach the identical projection digest. Same-process
// determinism (the fuzz above) cannot catch a reducer that reads process-global
// state (a package var, an env-seeded value) — two goroutines share it, two
// processes do not. This is the only gate that distinguishes "pure" from "agrees
// with itself in one process".
func TestFoldConvergesAcrossProcesses(t *testing.T) {
	t.Parallel()
	if os.Getenv("VIA_FOLD_REPLAY_CHILD") == "1" {
		fmt.Printf("VIA_FOLD_DIGEST=%d\n", replayFixedLogDigest())
		return
	}
	d1 := runReplayChild(t)
	d2 := runReplayChild(t)
	require.NotZero(t, d1, "child must produce a digest")
	require.Equal(t, d1, d2, "two independent processes must fold the same log to the same digest")
}

// replayFixedLogDigest folds a fixed, deterministic event sequence through the
// real projector decode+fold path and returns an fnv digest of the projection —
// the value an independent pod must reproduce exactly.
func replayFixedLogDigest() uint32 {
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	fold := bindLog(app, "k")

	var acc any = []int(nil)
	for n := range 100 {
		next, err := fold(acc, goodEnvBytes(n*7-13))
		if err != nil {
			panic(err) // the fixed corpus is all well-formed; an error is a regression
		}
		acc = next
	}
	got, _ := acc.([]int)
	h := fnv.New32a()
	for _, v := range got {
		_, _ = fmt.Fprintf(h, "%d,", v)
	}
	return h.Sum32()
}

// runReplayChild re-execs this test binary, running only this test with the
// child env set, and parses the digest line the child prints.
func runReplayChild(t *testing.T) uint32 {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestFoldConvergesAcrossProcesses$", "-test.v")
	cmd.Env = append(os.Environ(), "VIA_FOLD_REPLAY_CHILD=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "child process failed: %s", out)
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "VIA_FOLD_DIGEST="); ok {
			var d uint32
			_, err := fmt.Sscanf(v, "%d", &d)
			require.NoError(t, err)
			return d
		}
	}
	t.Fatalf("child did not print a digest:\n%s", out)
	return 0
}

// flakyEv is a deliberately IMPURE reducer: it appends a per-call counter, so
// folding the same (acc, ev) twice yields different results — exactly the bug
// fold-verify must catch (a real fold that read time/RNG/a mutable global).
var flakyFoldCounter int32

type flakyEv struct{ N int }

func (flakyEv) Fold(acc []int, e flakyEv) []int {
	return append(append([]int(nil), acc...), e.N, int(atomic.AddInt32(&flakyFoldCounter, 1)))
}

// envFor builds a current-version envelope for any event type, exactly as Append
// does on the wire (so the projector's decode path is exercised).
func envFor[E any](e E) []byte {
	d, _ := json.Marshal(e)
	b, err := json.Marshal(eventEnvelope{T: eventTypeTag[E](), V: currentVersionFor[E](), D: d})
	if err != nil {
		panic(err)
	}
	return b
}

func bindFlaky(app *App, key string) {
	var h StateAppEvents[flakyEv, []int]
	h.bindWireKey(key)
	h.bindApp(app)
}

// A snapshot crystallizes the projection into durable genesis once a key
// compacts; if the fold that produced it is non-deterministic, that corruption
// is frozen forever and re-folds can never recover. WithFoldVerify is the guard:
// it re-folds each record and, on a mismatch, emits via.fold.divergence AND
// REFUSES to compact the key — never letting a proven-non-deterministic fold
// reach durable genesis.
func TestFoldVerifyDetectsImpureFoldAndBlocksCompaction(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy),
		WithSnapshotInterval(1), WithFoldVerify())
	defer server.Close()
	defer app.backplane.Close()
	bindFlaky(app, "k")
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		if _, err := app.backplane.Append(ctx, "k", envFor(flakyEv{N: i})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	// The impurity is detected.
	require.Eventually(t, func() bool { return spy.saw("via.fold.divergence") },
		2*time.Second, 10*time.Millisecond, "WithFoldVerify must detect a non-deterministic fold")

	// And the key must NEVER compact — the prefix stays at genesis (offset 1),
	// so the non-deterministic projection is never crystallized into durable
	// genesis.
	require.Never(t, func() bool { return lowestRetainedOffset(t, app.backplane, "k") > 1 },
		500*time.Millisecond, 50*time.Millisecond,
		"a fold proven non-deterministic must never be compacted")
}

// A PURE fold under WithFoldVerify must behave exactly as without it: no
// divergence signal, and compaction proceeds normally. The guard must not flag
// or block correct reducers.
func TestFoldVerifyAllowsPureFoldToCompact(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy),
		WithSnapshotInterval(1), WithFoldVerify())
	defer server.Close()
	defer app.backplane.Close()
	bindLog(app, "k") // envEv.Fold is pure
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		if _, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	require.Eventually(t, func() bool { return lowestRetainedOffset(t, app.backplane, "k") > 1 },
		2*time.Second, 10*time.Millisecond, "a pure fold must still compact under WithFoldVerify")
	require.False(t, spy.saw("via.fold.divergence"), "a pure fold must not be flagged as divergent")
}

// Fold-verify is OPT-IN: by default (no WithFoldVerify) the projector does NOT
// pay the double-fold cost, so an impure fold is NOT flagged and compaction is
// not blocked by it. This pins the cost as opt-in (the council's dev-mode
// framing) rather than always-on.
func TestFoldVerifyIsOptInAndOffByDefault(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy), WithSnapshotInterval(1))
	defer server.Close()
	defer app.backplane.Close()
	bindFlaky(app, "k")
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		if _, err := app.backplane.Append(ctx, "k", envFor(flakyEv{N: i})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	// Without WithFoldVerify the divergence signal is never raised (no double-fold).
	require.Never(t, func() bool { return spy.saw("via.fold.divergence") },
		400*time.Millisecond, 50*time.Millisecond,
		"fold-verify must be opt-in: no divergence signal when disabled")
}
