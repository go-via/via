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

// Disconnect models a transient mid-stream drop: after delivering Disconnect
// records the wrapper closes its channel WITHOUT closing the underlying
// backplane, so a runtime that re-subscribes from its cursor can resume. The
// decorator must actually cut the stream at the boundary, not deliver everything.
func TestDisconnectCutsTheStreamAfterNRecords(t *testing.T) {
	t.Parallel()
	bp := memevents.Faulty{Backplane: via.InMemory(), Disconnect: 2}
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, d := range []string{"a", "b", "c", "d"} {
		if _, err := bp.Append(ctx, "k", []byte(d)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	ch, err := bp.Subscribe(ctx, "k", via.Offset(0))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	got := []string{string(recvWithin(t, ch).Data), string(recvWithin(t, ch).Data)}
	if got[0] != "a" || got[1] != "b" {
		t.Fatalf("first two = %v, want [a b]", got)
	}
	// After Disconnect=2 records the channel must CLOSE (transient drop), even
	// though more records remain in the underlying stream.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				goto reconnect
			}
		case <-deadline:
			t.Fatal("Disconnect must close the stream after N records, but it stayed open")
		}
	}
reconnect:
	// The underlying stream is intact: a fresh Subscribe from the last-seen
	// offset resumes with the remaining records (gap-free).
	ch2, err := bp.Subscribe(ctx, "k", via.Offset(2))
	if err != nil {
		t.Fatalf("re-subscribe: %v", err)
	}
	if r := recvWithin(t, ch2); string(r.Data) != "c" {
		t.Fatalf("resume after disconnect = %q, want c", r.Data)
	}
}

// Reorder lets the decorator deliver records out of order within a bounded
// window (a real at-least-once bus can interleave a redelivery with newer
// records). Every record in the window must still be delivered exactly within
// that window — reorder permutes, it never drops or invents records.
func TestReorderPermutesWithinAWindowWithoutLosingRecords(t *testing.T) {
	t.Parallel()
	bp := memevents.Faulty{Backplane: via.InMemory(), Reorder: 2}
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, d := range []string{"a", "b", "c", "d"} {
		if _, err := bp.Append(ctx, "k", []byte(d)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	ch, err := bp.Subscribe(ctx, "k", via.Offset(0))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	seen := map[string]int{}
	for range 4 {
		r := recvWithin(t, ch)
		seen[string(r.Data)]++
	}
	for _, d := range []string{"a", "b", "c", "d"} {
		if seen[d] != 1 {
			t.Fatalf("reorder must deliver every record exactly once, got %v for %q", seen[d], d)
		}
	}

	// With a window of 2 the order must actually be permuted at least once
	// (offsets are not strictly increasing), or "Reorder" is a no-op.
	chk, _ := bp.Subscribe(ctx, "k", via.Offset(0))
	var offs []via.Offset
	for range 4 {
		offs = append(offs, recvWithin(t, chk).Offset)
	}
	strictlyIncreasing := true
	for i := 1; i < len(offs); i++ {
		if offs[i] <= offs[i-1] {
			strictlyIncreasing = false
		}
	}
	if strictlyIncreasing {
		t.Fatalf("Reorder:2 must permute order within the window, got strictly increasing %v", offs)
	}
}

// Reorder composed with Disconnect exercises the early-stop branches inside the
// reorder flush: emit returns false (Disconnect threshold reached) part-way
// through a window, so the wrapper must stop flushing and close — not keep
// emitting past the drop. The underlying stream stays intact for a reconnect.
func TestReorderWithDisconnectStopsMidFlush(t *testing.T) {
	t.Parallel()
	bp := memevents.Faulty{Backplane: via.InMemory(), Reorder: 2, Disconnect: 1}
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, d := range []string{"a", "b", "c", "d"} {
		if _, err := bp.Append(ctx, "k", []byte(d)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	ch, err := bp.Subscribe(ctx, "k", via.Offset(0))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Exactly Disconnect=1 record is delivered, then the channel must close
	// (the drop fires inside the first reorder flush).
	_ = recvWithin(t, ch)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				goto resumed
			}
			t.Fatal("Disconnect=1 must stop after one record, got more")
		case <-deadline:
			t.Fatal("Disconnect must close the stream, but it stayed open")
		}
	}
resumed:
	// Underlying stream intact: a reconnect from offset 0 still has every record.
	ch2, err := bp.Subscribe(ctx, "k", via.Offset(0))
	if err != nil {
		t.Fatalf("re-subscribe: %v", err)
	}
	_ = recvWithin(t, ch2) // resumes with records still present
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
