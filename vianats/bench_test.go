package vianats_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/go-via/vianats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nuid"
	"github.com/stretchr/testify/require"
)

// These benchmarks measure the REAL durable backend (embedded JetStream on one
// in-process server) — the network/durability ceiling under which a multi-pod
// via cluster runs. The in-memory benches in package via measure the runtime +
// fold + convergence machinery; these measure what the durable log itself can
// sustain. (Single embedded server = the server is the bottleneck, so treat
// these as a floor, not a tuned-cluster number.)

func benchBackplane(b *testing.B, url string) *vianats.Backplane {
	b.Helper()
	nc, err := nats.Connect(url)
	require.NoError(b, err)
	b.Cleanup(nc.Close)
	bp, err := vianats.JetStream(nc, vianats.WithPrefix("b"+nuid.Next()))
	require.NoError(b, err)
	b.Cleanup(func() { _ = bp.Close() })
	return bp
}

// BenchmarkJetStreamAppendThroughput: concurrent durable Appends (JetStream
// Publish+ack) to one key — the write ceiling a clustered StateAppEvents.Append
// rides on.
func BenchmarkJetStreamAppendThroughput(b *testing.B) {
	url := startEmbeddedJetStream(b)
	bp := benchBackplane(b, url)
	payload := make([]byte, 64)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			if _, err := bp.Append(ctx, "k", payload); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkJetStreamFanout is the durable-backend analog of cross-pod
// convergence: P independent subscribers (one per simulated pod) each tail the
// SAME key from genesis and must receive all b.N records. ns/op is the
// per-record end-to-end delivery cost; records/s and deliveries/s (= pods×b.N)
// are reported.
func BenchmarkJetStreamFanout(b *testing.B) {
	for _, pods := range []int{1, 4, 8} {
		b.Run(fmt.Sprintf("pods=%d", pods), func(b *testing.B) {
			url := startEmbeddedJetStream(b)
			bp := benchBackplane(b, url)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			payload := make([]byte, 64)

			// Pre-load the log with b.N records, then time the fan-out delivery.
			for i := 0; i < b.N; i++ {
				if _, err := bp.Append(ctx, "k", payload); err != nil {
					b.Fatal(err)
				}
			}
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()

			var wg sync.WaitGroup
			for p := 0; p < pods; p++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ch, err := bp.Subscribe(ctx, "k", 0)
					if err != nil {
						return
					}
					got := 0
					for range ch {
						if got++; got >= b.N {
							return
						}
					}
				}()
			}
			wg.Wait()
			b.StopTimer()

			if secs := b.Elapsed().Seconds(); secs > 0 {
				b.ReportMetric(float64(b.N)/secs, "records/s")
				b.ReportMetric(float64(pods*b.N)/secs, "deliveries/s")
			}
		})
	}
}
