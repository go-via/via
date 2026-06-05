// Package memevents is the in-memory fault-injection layer for testing any
// [via.Backplane] against the nastiness a real backend exhibits. Pair it with
// backplanetest.RunConformance: a backend that still conforms while wrapped in
// Faulty is robust to the at-least-once redelivery a network log produces.
package memevents

import (
	"context"

	"github.com/go-via/via"
)

// Faulty decorates a via.Backplane to inject the three nastiness modes a real
// at-least-once network log exhibits — redelivery, transient mid-stream
// disconnect, and bounded reorder — so a backend (and the runtime above it) can
// be tested against them with no infrastructure. It embeds the wrapped
// Backplane, so Store/Append/Head/Close delegate unchanged; only Subscribe is
// augmented.
//
//	bp := memevents.Faulty{Backplane: via.InMemory(), Redeliver: 1}
//
// The modes compose and apply in order reorder → redeliver → disconnect.
// Redelivery preserves per-key order (each record is duplicated in place) so a
// conforming backend stays conforming (the runtime dedupes by offset).
// Disconnect and Reorder deliberately VIOLATE the strict-order / complete-stream
// shape, so they are used to test the runtime's resilience (reconnect-rehydrate,
// dedup) rather than fed through the order-checking conformance suite.
type Faulty struct {
	via.Backplane
	// Redeliver is the number of EXTRA times each record is delivered: 0 is an
	// exactly-once passthrough, 1 delivers every record twice, and so on.
	Redeliver int
	// Disconnect, if > 0, closes the Subscribe channel after delivering that
	// many records WITHOUT closing the underlying backplane — a transient drop.
	// The underlying stream is intact, so a runtime that re-subscribes from its
	// cursor resumes gap-free. 0 disables (never drops).
	Disconnect int
	// Reorder, if > 1, permutes delivery order within windows of that size (each
	// window is emitted reversed), so offsets are not strictly increasing. Models
	// a bus that interleaves a redelivery with newer records. 0/1 keep order.
	Reorder int
}

// Subscribe wraps the underlying stream, applying the configured faults. The
// wrapper goroutine cancels the underlying subscription and closes its channel
// on any exit (ctx cancel, backplane Close, or a Disconnect drop), so it cannot
// leak the underlying tailer.
func (f Faulty) Subscribe(ctx context.Context, key string, from via.Offset) (<-chan via.Record, error) {
	subCtx, cancel := context.WithCancel(ctx)
	in, err := f.Backplane.Subscribe(subCtx, key, from)
	if err != nil {
		cancel()
		return nil, err
	}
	out := make(chan via.Record)
	go func() {
		defer close(out)
		defer cancel() // tear down the underlying tailer on any return
		sent := 0
		// emit delivers r Redeliver+1 times; returns false when the wrapper
		// should stop (ctx cancelled or the Disconnect threshold reached).
		emit := func(r via.Record) bool {
			for range f.Redeliver + 1 {
				select {
				case out <- r:
				case <-ctx.Done():
					return false
				}
				if sent++; f.Disconnect > 0 && sent >= f.Disconnect {
					return false // transient drop
				}
			}
			return true
		}
		if f.Reorder > 1 {
			buf := make([]via.Record, 0, f.Reorder)
			flush := func() bool {
				for i := len(buf) - 1; i >= 0; i-- {
					if !emit(buf[i]) {
						return false
					}
				}
				buf = buf[:0]
				return true
			}
			for r := range in {
				if buf = append(buf, r); len(buf) >= f.Reorder {
					if !flush() {
						return
					}
				}
			}
			flush() // emit a partial trailing window when the stream ends
			return
		}
		for r := range in {
			if !emit(r) {
				return
			}
		}
	}()
	return out, nil
}
