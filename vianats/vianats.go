// Package vianats is the NATS JetStream reference backend for the Via state
// backplane. It implements via.Backplane with a JetStream KV bucket for the
// value Store and a single subject-per-key stream for the EventLog — one
// dependency providing both the durable CAS cell and the ordered, resumable
// log. It is verified against backplanetest.RunConformance.
//
//	nc, _ := nats.Connect(url)
//	bp, _ := vianats.JetStream(nc)
//	app := via.New(via.WithBackplane(bp))
//
// SECURITY (multi-tenant, MANDATORY on a shared bus): JetStream takes the
// caller's *nats.Conn, so transport security is configured at Connect and is the
// operator's responsibility — vianats never downgrades it. For any deployment
// where pods are not on a fully trusted network, supply per-pod credentials and
// mTLS, and give each tenant its own backplane prefix (WithPrefix) so subjects
// and the KV bucket are namespaced and the broker rejects cross-tenant access:
//
//	nc, _ := nats.Connect(url,
//	    nats.Secure(tlsConfig),              // mTLS
//	    nats.UserCredentials("pod.creds"),   // per-pod identity
//	)
//	bp, _ := vianats.JetStream(nc, vianats.WithPrefix("tenant-acme"))
//
// In-band session-isolation checks (full-sid exact match, fail-closed) are
// defence-in-depth; the broker's per-pod authz is the load-bearing boundary.
package vianats

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-via/via"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Backplane is a via.Backplane backed by NATS JetStream (KV + stream).
type Backplane struct {
	js     jetstream.JetStream
	kv     jetstream.KeyValue
	stream jetstream.Stream
	epoch  via.Epoch // per-stream generation: the stream's creation identity
	prefix string
	nc     *nats.Conn

	mu     sync.Mutex
	closed bool
	done   chan struct{} // closed by Close to unwind live subscriptions
}

// Option configures a Backplane at construction.
type Option func(*config)

type config struct{ prefix string }

// WithPrefix sets the name prefix for the KV bucket (`<prefix>_store`), the
// stream (`<prefix>_ev`), and its subjects (`<prefix>.ev.>`). Distinct prefixes
// give fully isolated backplanes on one server. Default "via".
func WithPrefix(p string) Option { return func(c *config) { c.prefix = p } }

// JetStream constructs a Backplane over an existing NATS connection, ensuring
// the KV bucket and stream exist. The caller owns nc; Close does not close it.
func JetStream(nc *nats.Conn, opts ...Option) (*Backplane, error) {
	cfg := config{prefix: "via"}
	for _, o := range opts {
		o(&cfg)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("vianats: jetstream: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	kv, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: cfg.prefix + "_store",
	})
	if err != nil {
		return nil, fmt.Errorf("vianats: create kv: %w", err)
	}
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        cfg.prefix + "_ev",
		Subjects:    []string{cfg.prefix + ".ev.>"},
		AllowDirect: true, // fast GetLastMsgForSubject (Head)
	})
	if err != nil {
		return nil, fmt.Errorf("vianats: create stream: %w", err)
	}
	// Epoch = the stream's creation identity. JetStream purge keeps sequence
	// numbers monotone (no offset-space reset), so the ONLY reset is a stream
	// delete+recreate, which yields a fresh Created timestamp. Stamping it on
	// Head + every Record lets the projector detect that reset (applog.go);
	// every client on the same live stream reads the same Created → same epoch.
	//
	// CreateOrUpdateStream returns a Stream whose CachedInfo is populated from
	// the server's success response (non-nil in nats.go v1.52.0). Guard against
	// a malformed/empty response so a nil StreamInfo surfaces as an error rather
	// than a nil-deref panic at construction.
	info := stream.CachedInfo()
	if info == nil {
		return nil, fmt.Errorf("vianats: stream %q returned no info (cannot derive epoch)", cfg.prefix+"_ev")
	}
	return &Backplane{
		js:     js,
		kv:     kv,
		stream: stream,
		epoch:  via.Epoch(info.Created.UnixNano()),
		prefix: cfg.prefix,
		nc:     nc,
		done:   make(chan struct{}),
	}, nil
}

// --- Store ---

func (b *Backplane) LoadSnapshot(ctx context.Context, key string) ([]byte, via.Rev, bool, error) {
	entry, err := b.kv.Get(ctx, b.storeKey(key))
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	return entry.Value(), via.Rev(entry.Revision()), true, nil
}

func (b *Backplane) CAS(ctx context.Context, key string, expectedRev via.Rev, data []byte) (via.Rev, error) {
	var rev uint64
	var err error
	if expectedRev == 0 {
		rev, err = b.kv.Create(ctx, b.storeKey(key), data)
	} else {
		rev, err = b.kv.Update(ctx, b.storeKey(key), data, uint64(expectedRev))
	}
	if err != nil {
		// A create-on-existing key AND a wrong-revision update both satisfy
		// errors.Is(ErrKeyExists) in nats.go (KV CAS = expected-last-sequence).
		if errors.Is(err, jetstream.ErrKeyExists) {
			return 0, via.ErrCASConflict
		}
		return 0, err
	}
	return via.Rev(rev), nil
}

// --- EventLog ---

func (b *Backplane) Append(ctx context.Context, key string, record []byte) (via.Offset, error) {
	if b.isClosed() {
		return 0, via.ErrClosed
	}
	ack, err := b.js.Publish(ctx, b.subjectFor(key), record)
	if err != nil {
		return 0, err
	}
	return via.Offset(ack.Sequence), nil
}

func (b *Backplane) Head(ctx context.Context, key string) (via.Offset, via.Epoch, error) {
	msg, err := b.stream.GetLastMsgForSubject(ctx, b.subjectFor(key))
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		// Empty key: no committed offset yet, but the stream still has its
		// generation — report it so empty→non-empty is not seen as a reset.
		return 0, b.epoch, nil
	}
	if err != nil {
		return 0, 0, err
	}
	return via.Offset(msg.Sequence), b.epoch, nil
}

func (b *Backplane) Subscribe(ctx context.Context, key string, from via.Offset) (<-chan via.Record, error) {
	if b.isClosed() {
		return nil, via.ErrClosed
	}
	cfg := jetstream.OrderedConsumerConfig{FilterSubjects: []string{b.subjectFor(key)}}
	if from == 0 {
		cfg.DeliverPolicy = jetstream.DeliverAllPolicy
	} else {
		cfg.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
		cfg.OptStartSeq = uint64(from) + 1
	}
	oc, err := b.stream.OrderedConsumer(ctx, cfg)
	if err != nil {
		return nil, err
	}
	it, err := oc.Messages()
	if err != nil {
		return nil, err
	}

	out := make(chan via.Record)
	// Unblock it.Next() on either the caller's ctx OR a backplane Close.
	go func() {
		select {
		case <-ctx.Done():
		case <-b.done:
		}
		it.Stop()
	}()
	go func() {
		defer close(out)
		defer it.Stop()
		for {
			msg, err := it.Next()
			if err != nil {
				return // iterator stopped (ctx/Close) or stream error
			}
			meta, err := msg.Metadata()
			if err != nil {
				return
			}
			rec := via.Record{Key: key, Epoch: b.epoch, Offset: via.Offset(meta.Sequence.Stream), Data: msg.Data()}
			select {
			case out <- rec:
			case <-ctx.Done():
				return
			case <-b.done:
				return
			}
		}
	}()
	return out, nil
}

// --- io.Closer ---

// Close marks the backplane closed (further Append/Subscribe return
// via.ErrClosed) and unwinds live subscriptions. It does NOT close the caller's
// nats.Conn — the caller owns the connection.
func (b *Backplane) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	close(b.done)
	return nil
}

func (b *Backplane) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// --- key mapping ---

func (b *Backplane) storeKey(key string) string   { return sanitize(key) }
func (b *Backplane) subjectFor(key string) string { return b.prefix + ".ev." + sanitize(key) }

// sanitize maps an arbitrary via wire key into a single safe NATS subject token
// / KV key: characters outside [A-Za-z0-9_-] become `_<hex>_`, so '.', '*',
// '>', and the like cannot break subject structure. Deterministic and
// reversible-enough for isolation (collision-free for distinct inputs).
func sanitize(key string) string {
	var sb strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			sb.WriteRune(r)
		default:
			fmt.Fprintf(&sb, "_%x_", r)
		}
	}
	if sb.Len() == 0 {
		return "_empty_"
	}
	return sb.String()
}
