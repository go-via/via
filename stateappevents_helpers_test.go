package via

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gaugeSpy captures Gauge(name,value,labels...) calls so the fold-divergence
// canary can be asserted: the (key, offset, digest) triple a pod emits after
// each fold is the cheap cross-pod divergence signal.
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
	app := New(WithMetrics(gs))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	t.Cleanup(server.Close)
	bindLog(app, key)
	ctx := context.Background()
	for _, n := range ns {
		_, err := app.backplane.Append(ctx, key, goodEnv(t, envEv{N: n}))
		require.NoError(t, err, "append")
	}
	require.Eventually(t, func() bool { return len(projection(app, key)) == len(ns) },
		2*time.Second, 10*time.Millisecond, "all events must fold")
	off, oko := gs.latest("via.fold.offset", key)
	dig, okd := gs.latest("via.fold.digest", key)
	require.True(t, oko, "projector must emit via.fold.offset after folding")
	require.True(t, okd, "projector must emit via.fold.digest after folding")
	return off, dig
}

// recvOffset reads one record off a subscription within a timeout, returning its
// offset — so a test can assert WHICH offset a compacted log resumes at.
func recvOffset(t *testing.T, sub <-chan Record) Offset {
	t.Helper()
	select {
	case r := <-sub:
		return r.Offset
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for a record")
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

// dropAfter wraps a Backplane and closes each Subscribe channel after delivering
// n records, WITHOUT closing the backplane itself — a transient connection drop
// (the JetStream OrderedConsumer dies, the stream survives). The underlying
// stream is intact, so a runtime that re-subscribes from its cursor must resume
// gap-free. It models the mid-Subscribe-disconnect fault the
// reconnect-rehydrate path requires (#3/#7).
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
func goodBenchEnv() []byte { return envFor(benchEv{N: 1}) }

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
// reducer that reads cross-process state. These two tests are the
// determinism gates: a fuzz that folding the SAME bytes is
// deterministic + never panics, and a SUBPROCESS replay that two independent
// processes fold a fixed log to the identical digest.
// replayFixedLogDigest folds a fixed, deterministic event sequence through the
// real projector decode+fold path and returns an fnv digest of the projection —
// the value an independent pod must reproduce exactly.
func replayFixedLogDigest() uint32 {
	app := New()
	server := httptest.NewServer(app)
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
	cmd := exec.Command(os.Args[0], "-test.run=^TestFold_convergesAcrossProcesses$", "-test.v")
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
	require.FailNow(t, fmt.Sprintf("child did not print a digest:\n%s", out))
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

// writeContrivedSnapshot stores a checkpoint at snapKey(key) directly, so a
// cold-starting projector can be observed seeding from it.
func writeContrivedSnapshot(t *testing.T, app *App, key string, cp checkpoint) {
	t.Helper()
	b, err := json.Marshal(cp)
	require.NoError(t, err)
	_, err = app.backplane.CAS(context.Background(), snapKey(key), 0, b)
	require.NoError(t, err)
}
