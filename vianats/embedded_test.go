package vianats_test

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
