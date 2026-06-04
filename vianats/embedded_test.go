package vianats_test

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// startEmbeddedJetStream boots an in-process JetStream-enabled nats-server on a
// random port with a temp file store, so the backend's conformance run needs no
// external server or container. Returns the client URL; the server is shut down
// at test end.
func startEmbeddedJetStream(t *testing.T) string {
	t.Helper()
	opts := &server.Options{
		Port:      -1, // random free port
		JetStream: true,
		StoreDir:  t.TempDir(),
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("new embedded nats-server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("embedded nats-server not ready")
	}
	t.Cleanup(srv.Shutdown)
	return srv.ClientURL()
}

// Before building the backend, prove the embedded JetStream primitives the
// backend rests on actually work in this environment: a KV bucket with
// create/get/CAS-revision, and a stream with ordered publish + consume.
func TestEmbeddedJetStreamPrimitivesWork(t *testing.T) {
	t.Parallel()
	url := startEmbeddedJetStream(t)
	ctx := context.Background()

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	// KV: create returns a revision; a stale-revision Update must conflict.
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "store"})
	if err != nil {
		t.Fatalf("create kv: %v", err)
	}
	rev, err := kv.Create(ctx, "cell", []byte("first"))
	if err != nil {
		t.Fatalf("kv create: %v", err)
	}
	if _, err := kv.Update(ctx, "cell", []byte("clobber"), rev-1+0); err == nil {
		// rev is >=1; updating with a wrong (0) revision must fail.
		t.Fatal("kv Update with a stale revision should have failed")
	}
	entry, err := kv.Get(ctx, "cell")
	if err != nil || string(entry.Value()) != "first" {
		t.Fatalf("kv get = %q err=%v, want first", entry.Value(), err)
	}

	// Stream: ordered publish returns a monotonic sequence; a subject-filtered
	// consumer replays in order.
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     "evlog",
		Subjects: []string{"ev.>"},
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}
	a1, err := js.Publish(ctx, "ev.alpha", []byte("a1"))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	a2, _ := js.Publish(ctx, "ev.alpha", []byte("a2"))
	if !(a2.Sequence > a1.Sequence) {
		t.Fatalf("stream sequences must increase, got %d then %d", a1.Sequence, a2.Sequence)
	}

	cons, err := js.CreateOrUpdateConsumer(ctx, "evlog", jetstream.ConsumerConfig{
		FilterSubject: "ev.alpha",
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
	})
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	batch, err := cons.Fetch(2, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	var got []string
	for msg := range batch.Messages() {
		got = append(got, string(msg.Data()))
	}
	if len(got) != 2 || got[0] != "a1" || got[1] != "a2" {
		t.Fatalf("consumer replay = %v, want [a1 a2] in order", got)
	}
}
