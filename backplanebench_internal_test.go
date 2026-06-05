package via

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// benchEv is an O(1) reducer (counts events) so a benchmark measures the
// backplane + projector + cross-pod convergence machinery, NOT an O(n) slice
// copy in the fold. The projection is the running count == number folded.
type benchEv struct{ N int }

func (benchEv) Fold(acc int, _ benchEv) int { return acc + 1 }

func bindBench(app *App, key string) {
	var h StateAppEvents[benchEv, int]
	h.bindWireKey(key)
	h.bindApp(app)
}

func benchCount(app *App, key string) int {
	v, ok := app.logProjection(key)
	if !ok {
		return 0
	}
	n, _ := v.(int)
	return n
}

// newPod builds an App bound to a shared backplane WITHOUT an HTTP server — the
// projector machinery (backplane, logs, fold loop) runs independent of serving,
// so a multi-pod load test needs no sockets.
func newPod(bp Backplane, opts ...Option) *App {
	return New(append([]Option{WithBackplane(bp)}, opts...)...)
}

// BenchmarkInMemoryAppendThroughput measures the writer ceiling: concurrent
// Appends into the shared EventLog, no projectors attached. One key = contended
// on the per-key lock (the cross-pod chat/counter case); shardKeys=true gives
// each writer its own key (the independent-aggregate ceiling).
func BenchmarkInMemoryAppendThroughput(b *testing.B) {
	for _, shard := range []bool{false, true} {
		name := "oneKey"
		if shard {
			name = "perWriterKey"
		}
		b.Run(name, func(b *testing.B) {
			bp := InMemory()
			defer bp.Close()
			payload := envFor(benchEv{N: 1})
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			var w int64
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				ctx := context.Background()
				key := "k"
				if shard {
					key = fmt.Sprintf("k%d", atomic.AddInt64(&w, 1))
				}
				for pb.Next() {
					if _, err := bp.Append(ctx, key, payload); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// BenchmarkCrossPodConvergence is the headline multi-node load test: P pods
// share ONE backplane, b.N events are appended concurrently by all writer
// cores, and the timed region ends only when EVERY pod's projector has folded
// all b.N events (full cross-pod convergence). ns/op is the end-to-end
// per-event convergence cost across the whole cluster; events/s and folds/s
// (= pods × events) are reported explicitly.
func BenchmarkCrossPodConvergence(b *testing.B) {
	for _, pods := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("pods=%d", pods), func(b *testing.B) {
			runConvergence(b, pods)
		})
	}
}

// BenchmarkConvergenceFeatureCost isolates the per-fold overhead of WithFoldVerify
// (the double-fold compaction gate) at a fixed 4-pod load, vs baseline (the
// always-on fold-divergence canary encode is in both). Snapshots stay off (the
// default here) so this measures the fold path, not compaction.
func BenchmarkConvergenceFeatureCost(b *testing.B) {
	b.Run("baseline", func(b *testing.B) { runConvergence(b, 4) })
	b.Run("foldVerify", func(b *testing.B) { runConvergence(b, 4, WithFoldVerify()) })
	// Snapshots+compaction ON: a fast pod compacts the shared log while peers
	// lag. Converges correctly only because a lagging projector re-seeds from the
	// snapshot on a compaction gap (applyRecord) — without that fix this variant
	// diverges and stalls.
	b.Run("compactEvery256", func(b *testing.B) { runConvergence(b, 4, WithSnapshotInterval(256)) })
}

func runConvergence(b *testing.B, pods int, opts ...Option) {
	// The in-memory backplane keeps the WHOLE event log in the Go heap, so GC
	// work scales with total events appended in a run (an artifact of an
	// unbounded in-mem log — production bounds it with snapshot+compaction, and a
	// real backend keeps the log off-heap). Relax GC during the timed region so
	// this measures the fold + cross-pod convergence throughput, not GC scanning
	// a growing slice. The durable-backend ceiling is BenchmarkJetStreamFanout.
	defer debug.SetGCPercent(debug.SetGCPercent(800))

	bp := InMemory()
	defer bp.Close()
	apps := make([]*App, pods)
	for i := range apps {
		// Snapshots OFF by default for the throughput measurement; compaction is
		// its own variant (compactEvery256), which converges correctly via the
		// gap-reseed + monotonic-snapshot fixes this bench surfaced.
		apps[i] = newPod(bp, append([]Option{WithSnapshotInterval(0)}, opts...)...)
		bindBench(apps[i], "k")
	}
	payload := envFor(benchEv{N: 1})
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	// Fan b.N appends across all writer cores.
	var issued int64
	var wg sync.WaitGroup
	workers := runtime.GOMAXPROCS(0)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for atomic.AddInt64(&issued, 1) <= int64(b.N) {
				if _, err := bp.Append(ctx, "k", payload); err != nil {
					return
				}
			}
		}()
	}
	wg.Wait()

	// Convergence barrier: every pod must have folded all b.N events. Poll
	// INFREQUENTLY (1ms): benchCount→logProjection takes ls.mu.RLock, which the
	// projector Locks once per fold, so frequent polling perturbs (even starves)
	// the very projectors being measured. 1ms adds negligible tail to a
	// hundreds-of-ms convergence while keeping the RWMutex essentially
	// uncontended by the observer.
	deadline := time.Now().Add(30 * time.Second)
	for i, app := range apps {
		for benchCount(app, "k") < b.N {
			if time.Now().After(deadline) {
				b.Fatalf("convergence stalled: pod %d at %d/%d", i, benchCount(app, "k"), b.N)
			}
			time.Sleep(time.Millisecond) // poll infrequently: logProjection RLocks ls.mu (projector Locks it per fold)
		}
	}
	b.StopTimer()

	secs := b.Elapsed().Seconds()
	if secs > 0 {
		b.ReportMetric(float64(b.N)/secs, "events/s")
		b.ReportMetric(float64(pods*b.N)/secs, "folds/s")
	}
}
