package vianats_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/backplanetest"
	"github.com/go-via/vianats"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The NATS backend must satisfy the SAME Backplane contract as the in-memory
// reference — this is the RELEASE-GATING real-backend conformance run: a green
// in-mem suite never stands in for a real ordered, durable, resumable log.
// Each conformance subtest gets a freshly-named bucket+stream (unique prefix) on
// one embedded JetStream server, so subtests are isolated.
func TestJetStream_passesBackplaneConformance(t *testing.T) {
	t.Parallel()
	url := startEmbeddedJetStream(t)

	backplanetest.RunConformance(t, func() via.Backplane {
		nc, err := nats.Connect(url)
		require.NoError(t, err)
		t.Cleanup(nc.Close)
		bp, err := vianats.JetStream(nc, vianats.WithPrefix("t"+nuid.Next()))
		require.NoError(t, err)
		return bp
	})
}

// startEmbeddedJetStream boots an in-process JetStream-enabled nats-server on a
// random port with a temp file store, so the backend's conformance run needs no
// external server or container. Returns the client URL; the server is shut down
// at test end.
func startEmbeddedJetStream(t testing.TB) string {
	t.Helper()
	opts := &server.Options{
		Port:      -1, // random free port
		JetStream: true,
		StoreDir:  t.TempDir(),
	}
	srv, err := server.NewServer(opts)
	require.NoError(t, err, "new embedded nats-server")
	go srv.Start()
	require.True(t, srv.ReadyForConnections(10*time.Second), "embedded nats-server not ready")
	t.Cleanup(srv.Shutdown)
	return srv.ClientURL()
}

// Before building the backend, prove the embedded JetStream primitives the
// backend rests on actually work in this environment: a KV bucket with
// create/get/CAS-revision, and a stream with ordered publish + consume.
func TestJetStream_primitivesWorkOnEmbeddedServer(t *testing.T) {
	t.Parallel()
	url := startEmbeddedJetStream(t)
	ctx := context.Background()

	nc, err := nats.Connect(url)
	require.NoError(t, err, "connect")
	defer nc.Close()
	js, err := jetstream.New(nc)
	require.NoError(t, err, "jetstream")

	// KV: create returns a revision; a stale-revision Update must conflict.
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "store"})
	require.NoError(t, err, "create kv")
	rev, err := kv.Create(ctx, "cell", []byte("first"))
	require.NoError(t, err, "kv create")
	// rev is >=1; updating with a wrong (0) revision must fail.
	_, err = kv.Update(ctx, "cell", []byte("clobber"), rev-1)
	require.Error(t, err, "kv Update with a stale revision should have failed")
	entry, err := kv.Get(ctx, "cell")
	require.NoError(t, err, "kv get")
	assert.Equal(t, "first", string(entry.Value()))

	// Stream: ordered publish returns a monotonic sequence; a subject-filtered
	// consumer replays in order.
	_, err = js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     "evlog",
		Subjects: []string{"ev.>"},
	})
	require.NoError(t, err, "create stream")
	a1, err := js.Publish(ctx, "ev.alpha", []byte("a1"))
	require.NoError(t, err, "publish")
	a2, err := js.Publish(ctx, "ev.alpha", []byte("a2"))
	require.NoError(t, err, "publish")
	assert.Greater(t, a2.Sequence, a1.Sequence, "stream sequences must increase")

	cons, err := js.CreateOrUpdateConsumer(ctx, "evlog", jetstream.ConsumerConfig{
		FilterSubject: "ev.alpha",
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
	})
	require.NoError(t, err, "consumer")
	batch, err := cons.Fetch(2, jetstream.FetchMaxWait(2*time.Second))
	require.NoError(t, err, "fetch")
	var got []string
	for msg := range batch.Messages() {
		got = append(got, string(msg.Data()))
	}
	assert.Equal(t, []string{"a1", "a2"}, got, "consumer must replay in order")
}

// The projector's offset-space-reset detection (stateappevents_projector.go,
// projectRecord)
// fires only when a Record's Epoch differs from the last-seen epoch. A backend
// that always reports Epoch(0) makes a stream delete+recreate (which restarts the
// offset space) silently undetectable — the projector keeps its stale
// high-water-mark and skips every "new" record. So vianats MUST stamp a real,
// non-zero per-stream generation on Head and on every delivered Record, and that
// generation must be identical for any two clients looking at the SAME live
// stream (else two pods would each think the other reset).
func TestEpoch_isNonZeroAndStableAcrossClients(t *testing.T) {
	t.Parallel()
	url := startEmbeddedJetStream(t)
	ctx := context.Background()
	prefix := "t" + nuid.Next()

	bp1 := dialBackplane(t, url, prefix)
	off, err := bp1.Append(ctx, "k", []byte("e1"))
	require.NoError(t, err)
	require.NotZero(t, off)

	_, headEpoch, err := bp1.Head(ctx, "k")
	require.NoError(t, err)
	require.NotZero(t, headEpoch, "Head epoch must be a real stream generation, not 0")

	// Records delivered by Subscribe must carry the same generation.
	rec := firstRecord(t, bp1, "k")
	require.Equal(t, headEpoch, rec.Epoch, "Subscribe record epoch must match Head epoch")

	// A SECOND, independent client on the same stream must observe the SAME
	// generation — the epoch identifies the stream, not the connection.
	bp2 := dialBackplane(t, url, prefix)
	_, headEpoch2, err := bp2.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, headEpoch, headEpoch2, "two clients on one live stream must agree on the epoch")
}

// A stream delete+recreate restarts the offset space (a real backend reset:
// recreated JetStream stream, Redis XTRIM-to-empty, PG restore). The epoch MUST
// change across that boundary, otherwise the projector cannot tell "offset 1 of
// the new generation" from "already-applied offset 1" and would strand the key.
func TestEpoch_differsAfterStreamDeleteAndRecreate(t *testing.T) {
	t.Parallel()
	url := startEmbeddedJetStream(t)
	ctx := context.Background()
	prefix := "t" + nuid.Next()

	bp1 := dialBackplane(t, url, prefix)
	if _, err := bp1.Append(ctx, "k", []byte("gen1")); err != nil {
		t.Fatalf("append gen1: %v", err)
	}
	_, epoch1, err := bp1.Head(ctx, "k")
	require.NoError(t, err)
	require.NotZero(t, epoch1)

	// Delete the underlying stream out-of-band, then reconstruct the backplane,
	// which recreates the stream with a fresh creation identity.
	deleteStream(t, url, prefix)
	bp2 := dialBackplane(t, url, prefix)
	_, epoch2, err := bp2.Head(ctx, "k")
	require.NoError(t, err)

	require.NotEqual(t, epoch1, epoch2, "epoch must change across a stream delete+recreate")
}

// An empty key has no records yet, but the stream it lives on still has a
// generation. Head must report that same stream epoch for an empty key, so the
// first Append (empty→non-empty) does NOT look like an offset-space reset: a
// reader that saw epoch 0 on the empty key and then the real epoch on the first
// record would spuriously re-snapshot.
func TestHead_reportsStreamEpochForEmptyKey(t *testing.T) {
	t.Parallel()
	url := startEmbeddedJetStream(t)
	ctx := context.Background()
	prefix := "t" + nuid.Next()

	bp := dialBackplane(t, url, prefix)
	off, emptyEpoch, err := bp.Head(ctx, "never-written")
	require.NoError(t, err)
	require.Zero(t, off, "empty key has no committed offset")
	require.NotZero(t, emptyEpoch, "empty key must still report the stream generation")

	// After the first append, the epoch must be unchanged (no spurious reset).
	if _, err := bp.Append(ctx, "never-written", []byte("first")); err != nil {
		t.Fatalf("append: %v", err)
	}
	_, afterEpoch, err := bp.Head(ctx, "never-written")
	require.NoError(t, err)
	require.Equal(t, emptyEpoch, afterEpoch, "epoch must not change across empty→non-empty")
}

func dialBackplane(t *testing.T, url, prefix string) *vianats.Backplane {
	t.Helper()
	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	bp, err := vianats.JetStream(nc, vianats.WithPrefix(prefix))
	require.NoError(t, err)
	t.Cleanup(func() { _ = bp.Close() })
	return bp
}

func deleteStream(t *testing.T, url, prefix string) {
	t.Helper()
	nc, err := nats.Connect(url)
	require.NoError(t, err)
	defer nc.Close()
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	require.NoError(t, js.DeleteStream(context.Background(), prefix+"_ev"))
}

func firstRecord(t *testing.T, bp via.Backplane, key string) via.Record {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := bp.Subscribe(ctx, key, 0)
	require.NoError(t, err)
	rec, ok := <-ch
	require.True(t, ok, "expected at least one record")
	return rec
}
