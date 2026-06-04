package memevents_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/backplanetest"
	"github.com/go-via/via/memevents"
)

// A backend that redelivers records in order is still a CONFORMING backend —
// the EventLog contract is at-least-once and the runtime dedupes by offset. If
// the conformance suite passes against Faulty{Redeliver:1}, it can gate a real
// at-least-once backend (NATS) the same way.
func TestFaultyRedeliveryStillConforms(t *testing.T) {
	t.Parallel()
	backplanetest.RunConformance(t, func() via.Backplane {
		return memevents.Faulty{Backplane: via.InMemory(), Redeliver: 1}
	})
}

// recvWithin reads one record or fails the test on timeout, so a broken
// decorator can never hang the suite.
func recvWithin(t *testing.T, ch <-chan via.Record) via.Record {
	t.Helper()
	select {
	case r, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before a record arrived")
		}
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a record")
		return via.Record{}
	}
}

// The decorator must actually inject redelivery, not silently pass through: one
// appended record is delivered at least twice (same offset, same data) when
// Redeliver is 1.
func TestRedeliverActuallyDuplicatesRecords(t *testing.T) {
	t.Parallel()
	bp := memevents.Faulty{Backplane: via.InMemory(), Redeliver: 1}
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	off, err := bp.Append(ctx, "k", []byte("a"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	ch, err := bp.Subscribe(ctx, "k", via.Offset(0))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	first := recvWithin(t, ch)
	second := recvWithin(t, ch)
	if first.Offset != off || second.Offset != off {
		t.Fatalf("redelivery must repeat the same offset %d, got %d then %d", off, first.Offset, second.Offset)
	}
	if string(first.Data) != "a" || string(second.Data) != "a" {
		t.Fatalf("redelivery must repeat the same data %q, got %q then %q", "a", first.Data, second.Data)
	}
}

// The redelivery wrapper spawns a goroutine per Subscribe; it must exit when
// the consumer's ctx is cancelled, or every disconnected tab leaks a goroutine.
func TestFaultySubscribeUnblocksOnContextCancel(t *testing.T) {
	t.Parallel()
	bp := memevents.Faulty{Backplane: via.InMemory(), Redeliver: 1}
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())

	_, err := bp.Append(ctx, "k", []byte("a"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	ch, err := bp.Subscribe(ctx, "k", via.Offset(0))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_ = recvWithin(t, ch) // drain at least one delivery so we're mid-stream

	cancel()
	// The wrapper channel must close promptly once ctx is cancelled.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed — goroutine unwound
			}
		case <-deadline:
			t.Fatal("Subscribe channel did not close after ctx cancel — goroutine leak")
		}
	}
}

// Subscribe must surface the wrapped backplane's error rather than swallow it
// and hand back a silently-empty stream — a closed backplane's ErrClosed has to
// propagate through the decorator.
func TestFaultySubscribePropagatesUnderlyingError(t *testing.T) {
	t.Parallel()
	bp := memevents.Faulty{Backplane: via.InMemory(), Redeliver: 1}
	if err := bp.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := bp.Subscribe(context.Background(), "k", via.Offset(0)); err != via.ErrClosed {
		t.Fatalf("Subscribe after Close error = %v, want ErrClosed (must propagate)", err)
	}
}

// Redeliver:0 is a faithful exactly-once passthrough: each record arrives once,
// in order, with no spurious duplicate.
func TestRedeliverZeroIsExactlyOncePassthrough(t *testing.T) {
	t.Parallel()
	bp := memevents.Faulty{Backplane: via.InMemory(), Redeliver: 0}
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o1, _ := bp.Append(ctx, "k", []byte("a"))
	o2, _ := bp.Append(ctx, "k", []byte("b"))
	ch, err := bp.Subscribe(ctx, "k", via.Offset(0))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	r1 := recvWithin(t, ch)
	r2 := recvWithin(t, ch)
	if r1.Offset != o1 || string(r1.Data) != "a" {
		t.Fatalf("first = offset %d %q, want %d a", r1.Offset, r1.Data, o1)
	}
	if r2.Offset != o2 || string(r2.Data) != "b" {
		t.Fatalf("second = offset %d %q, want %d b", r2.Offset, r2.Data, o2)
	}

	// No spurious third record (a duplicate) should arrive promptly.
	select {
	case extra := <-ch:
		t.Fatalf("Redeliver:0 must not duplicate, got an extra record %+v", extra)
	case <-time.After(150 * time.Millisecond):
	}
}
