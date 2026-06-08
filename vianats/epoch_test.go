package vianats_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/vianats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nuid"
	"github.com/stretchr/testify/require"
)

// The projector's offset-space-reset detection (via applog.go projectRecord)
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
