// Package backplanetest provides a parameterized conformance suite that any
// [via.Backplane] implementation must pass. It is the executable contract: a
// backend author wires their backplane into [RunConformance] and gets the
// ordering, gap-free-resume, CAS, and lifecycle guarantees checked for them.
//
//	func TestMyBackend(t *testing.T) {
//	    backplanetest.RunConformance(t, func() via.Backplane { return myBackend() })
//	}
//
// Like fstest/iotest, this lives in a normal (non-_test) file so external
// packages can import it; it imports "testing" deliberately.
package backplanetest

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
)

// recvTimeout bounds every channel read so a broken Subscribe cannot hang the
// suite. Generous enough for a real network backend on a slow CI runner.
const recvTimeout = 3 * time.Second

// RunConformance runs the full Backplane contract suite. newBackplane must
// return a FRESH, empty backplane each call — every subtest gets its own so
// they cannot interfere. A backend that passes RunConformance satisfies the
// ordering, durability, resumability, CAS, and Close guarantees the runtime
// relies on.
//
// Offsets are treated as OPAQUE: the suite reads them back from Append and only
// asserts they are strictly increasing within a key and resumable — it never
// assumes a particular numbering, so a Kafka/JetStream/Postgres backend with
// non-dense offsets conforms.
func RunConformance(t *testing.T, newBackplane func() via.Backplane) {
	t.Helper()
	t.Run("AppendReturnsIncreasingOffsetsAndHeadTracksThem", func(t *testing.T) {
		testAppendOffsetsAndHead(t, newBackplane())
	})
	t.Run("SubscribeFromGenesisDeliversEveryRecordInOrder", func(t *testing.T) {
		testGenesisReplay(t, newBackplane())
	})
	t.Run("SubscribeResumesStrictlyAfterTheGivenOffset", func(t *testing.T) {
		testResumeAfterOffset(t, newBackplane())
	})
	t.Run("SubscribeLiveTailsRecordsAppendedAfterSubscription", func(t *testing.T) {
		testLiveTail(t, newBackplane())
	})
	t.Run("DistinctKeysAreIndependentStreams", func(t *testing.T) {
		testPerKeyIndependence(t, newBackplane())
	})
	t.Run("ConcurrentAppendsToOneKeyGetDistinctOffsets", func(t *testing.T) {
		testConcurrentAppends(t, newBackplane())
	})
	t.Run("IndependentSubscribersSeeTheSameStream", func(t *testing.T) {
		testMultipleSubscribers(t, newBackplane())
	})
	t.Run("CASRejectsAStaleRevisionAndPreservesTheCell", func(t *testing.T) {
		testCAS(t, newBackplane())
	})
	t.Run("ClosedBackplaneRefusesAppend", func(t *testing.T) {
		testClosedRefusesAppend(t, newBackplane())
	})
}

func testAppendOffsetsAndHead(t *testing.T, bp via.Backplane) {
	t.Helper()
	defer bp.Close()
	ctx := context.Background()

	off, _, err := bp.Head(ctx, "k")
	mustNoErr(t, err)
	if off != via.Offset(0) {
		t.Fatalf("Head of an empty key = %d, want Offset(0)", off)
	}

	o1 := mustAppend(t, bp, "k", "a")
	o2 := mustAppend(t, bp, "k", "b")
	o3 := mustAppend(t, bp, "k", "c")
	if o1 >= o2 || o2 >= o3 {
		t.Fatalf("offsets must strictly increase within a key, got %d,%d,%d", o1, o2, o3)
	}

	head, _, err := bp.Head(ctx, "k")
	mustNoErr(t, err)
	if head != o3 {
		t.Fatalf("Head = %d after three appends, want the last offset %d", head, o3)
	}
}

func testGenesisReplay(t *testing.T, bp via.Backplane) {
	t.Helper()
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := []string{"a", "b", "c"}
	for _, v := range want {
		mustAppend(t, bp, "k", v)
	}
	ch := mustSubscribe(t, bp, ctx, "k", via.Offset(0))

	got := collectDistinct(t, ch, len(want))
	for i, w := range want {
		if string(got[i].Data) != w {
			t.Fatalf("genesis replay record %d = %q, want %q", i, got[i].Data, w)
		}
	}
}

func testResumeAfterOffset(t *testing.T, bp via.Backplane) {
	t.Helper()
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mustAppend(t, bp, "k", "a")
	from := mustAppend(t, bp, "k", "b") // resume cursor: committed up to "b"
	mustAppend(t, bp, "k", "c")

	// Resuming from "b" must yield only what came strictly after it ("c"),
	// never "a" or "b" — the resume primitive that retires stranding.
	ch := mustSubscribe(t, bp, ctx, "k", from)
	r := collectDistinct(t, ch, 1)[0]
	if string(r.Data) != "c" {
		t.Fatalf("resume from %d delivered %q, want %q (only records after the cursor)", from, r.Data, "c")
	}
	if r.Offset <= from {
		t.Fatalf("resumed record offset %d must be > the resume cursor %d", r.Offset, from)
	}
}

func testLiveTail(t *testing.T, bp via.Backplane) {
	t.Helper()
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	head := mustAppend(t, bp, "k", "a")
	// Subscribe from head: nothing to replay, so the next record delivered must
	// be one appended AFTER the subscription — proving live tailing.
	ch := mustSubscribe(t, bp, ctx, "k", head)
	mustAppend(t, bp, "k", "b")
	r := collectDistinct(t, ch, 1)[0]
	if string(r.Data) != "b" {
		t.Fatalf("live tail delivered %q, want the post-subscription append %q", r.Data, "b")
	}
}

func testPerKeyIndependence(t *testing.T, bp via.Backplane) {
	t.Helper()
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mustAppend(t, bp, "alpha", "a1")
	mustAppend(t, bp, "beta", "b1")
	mustAppend(t, bp, "alpha", "a2")

	// A subscription to alpha must see only alpha's records, in order, with no
	// beta records bleeding in (no cross-key ordering).
	ch := mustSubscribe(t, bp, ctx, "alpha", via.Offset(0))
	got := collectDistinct(t, ch, 2)
	if string(got[0].Data) != "a1" {
		t.Fatalf("alpha[0] = %q, want a1 (beta must not leak in)", got[0].Data)
	}
	if string(got[1].Data) != "a2" {
		t.Fatalf("alpha[1] = %q, want a2 (beta must not leak in)", got[1].Data)
	}
}

func testConcurrentAppends(t *testing.T, bp via.Backplane) {
	t.Helper()
	defer bp.Close()
	ctx := context.Background()

	// The headline guarantee (#2): N concurrent appends to one key all land and
	// the EventLog totally-orders them — no lost write, no duplicate offset, no
	// CAS to retry-storm. A backend that drops or aliases offsets under
	// concurrency fails here.
	const n = 24
	offs := make(chan via.Offset, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			off, err := bp.Append(ctx, "k", []byte("x"))
			if err != nil {
				errs <- err
				return
			}
			offs <- off
		}()
	}
	wg.Wait()
	close(offs)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent append failed: %v", err)
	}

	seen := make(map[via.Offset]bool, n)
	var max via.Offset
	for off := range offs {
		if seen[off] {
			t.Fatalf("concurrent appends produced a duplicate offset %d (no total order)", off)
		}
		seen[off] = true
		if off > max {
			max = off
		}
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct offsets from %d concurrent appends", len(seen), n)
	}
	head, _, err := bp.Head(ctx, "k")
	mustNoErr(t, err)
	if head != max {
		t.Fatalf("Head = %d, want the largest committed offset %d", head, max)
	}
}

func testMultipleSubscribers(t *testing.T, bp via.Backplane) {
	t.Helper()
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := []string{"a", "b", "c"}
	for _, v := range want {
		mustAppend(t, bp, "k", v)
	}
	// Two independent subscribers from genesis must each see the full stream in
	// the same order with the same offsets — no per-subscriber state bug, no
	// non-deterministic ordering.
	ch1 := mustSubscribe(t, bp, ctx, "k", via.Offset(0))
	ch2 := mustSubscribe(t, bp, ctx, "k", via.Offset(0))
	got1 := collectDistinct(t, ch1, len(want))
	got2 := collectDistinct(t, ch2, len(want))
	for i, w := range want {
		if string(got1[i].Data) != w || string(got2[i].Data) != w {
			t.Fatalf("subscribers disagree on record %d: %q / %q, want %q", i, got1[i].Data, got2[i].Data, w)
		}
		if got1[i].Offset != got2[i].Offset {
			t.Fatalf("subscribers disagree on offset for %q: %d vs %d", w, got1[i].Offset, got2[i].Offset)
		}
	}
}

func testCAS(t *testing.T, bp via.Backplane) {
	t.Helper()
	defer bp.Close()
	ctx := context.Background()

	if _, _, ok, err := bp.LoadSnapshot(ctx, "cell"); err != nil || ok {
		t.Fatalf("LoadSnapshot of an unwritten cell: ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	rev1, err := bp.CAS(ctx, "cell", via.Rev(0), []byte("first"))
	mustNoErr(t, err)
	if rev1 == via.Rev(0) {
		t.Fatal("a successful first CAS must return a non-zero revision")
	}

	// A writer holding the stale Rev(0) must lose and leave the cell intact.
	if _, err := bp.CAS(ctx, "cell", via.Rev(0), []byte("clobber")); !errors.Is(err, via.ErrCASConflict) {
		t.Fatalf("stale CAS error = %v, want ErrCASConflict", err)
	}
	data, rev, ok, err := bp.LoadSnapshot(ctx, "cell")
	mustNoErr(t, err)
	if !ok || !bytes.Equal(data, []byte("first")) || rev != rev1 {
		t.Fatalf("after a rejected CAS: data=%q rev=%d ok=%v, want first/%d/true", data, rev, ok, rev1)
	}

	// A writer with the current revision succeeds and advances it.
	rev2, err := bp.CAS(ctx, "cell", rev1, []byte("second"))
	mustNoErr(t, err)
	if rev2 == rev1 {
		t.Fatalf("a successful CAS must advance the revision, got %d (== %d)", rev2, rev1)
	}
}

func testClosedRefusesAppend(t *testing.T, bp via.Backplane) {
	t.Helper()
	ctx := context.Background()
	mustAppend(t, bp, "k", "a")
	mustNoErr(t, bp.Close())
	if _, err := bp.Append(ctx, "k", []byte("b")); !errors.Is(err, via.ErrClosed) {
		t.Fatalf("Append after Close error = %v, want ErrClosed", err)
	}
}

// --- helpers ---

func mustAppend(t *testing.T, bp via.Backplane, key, data string) via.Offset {
	t.Helper()
	off, err := bp.Append(context.Background(), key, []byte(data))
	mustNoErr(t, err)
	return off
}

func mustSubscribe(t *testing.T, bp via.Backplane, ctx context.Context, key string, from via.Offset) <-chan via.Record {
	t.Helper()
	ch, err := bp.Subscribe(ctx, key, from)
	mustNoErr(t, err)
	return ch
}

// collectDistinct reads until it has seen `count` records with distinct
// offsets, skipping at-least-once duplicates, and asserts each newly-seen
// offset is strictly greater than the previous — i.e. the FIRST delivery of
// each record arrives in per-key offset order. This makes the suite tolerate a
// conforming at-least-once backend (in-order redelivery) while still rejecting
// genuine out-of-order or gap violations.
func collectDistinct(t *testing.T, ch <-chan via.Record, count int) []via.Record {
	t.Helper()
	seen := make(map[via.Offset]bool, count)
	out := make([]via.Record, 0, count)
	var last via.Offset
	for len(out) < count {
		r := recv(t, ch)
		if seen[r.Offset] {
			continue // a tolerated at-least-once duplicate
		}
		if len(out) > 0 && r.Offset <= last {
			t.Fatalf("first delivery of offset %d arrived out of per-key order (after %d)", r.Offset, last)
		}
		seen[r.Offset] = true
		last = r.Offset
		out = append(out, r)
	}
	return out
}

func recv(t *testing.T, ch <-chan via.Record) via.Record {
	t.Helper()
	select {
	case r, ok := <-ch:
		if !ok {
			t.Fatal("Subscribe channel closed before a record arrived")
		}
		return r
	case <-time.After(recvTimeout):
		t.Fatal("timed out waiting for a Record from Subscribe")
		return via.Record{}
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
