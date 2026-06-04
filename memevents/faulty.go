// Package memevents is the in-memory fault-injection layer for testing any
// [via.Backplane] against the nastiness a real backend exhibits. Pair it with
// backplanetest.RunConformance: a backend that still conforms while wrapped in
// Faulty is robust to the at-least-once redelivery a network log produces.
package memevents

import (
	"context"

	"github.com/go-via/via"
)

// Faulty decorates a via.Backplane to inject controllable redelivery on
// Subscribe. It embeds the wrapped Backplane, so Store/Append/Head/Close
// delegate unchanged; only Subscribe is augmented.
//
//	bp := memevents.Faulty{Backplane: via.InMemory(), Redeliver: 1}
//
// Redelivery preserves per-key order (each record is duplicated in place), so a
// conforming backend stays conforming — the runtime dedupes redelivered records
// by offset. (Disconnect and reorder faults are intentionally not modelled
// here yet.)
type Faulty struct {
	via.Backplane
	// Redeliver is the number of EXTRA times each record is delivered: 0 is an
	// exactly-once passthrough, 1 delivers every record twice, and so on.
	Redeliver int
}

// Subscribe wraps the underlying stream, emitting each record Redeliver+1 times
// in order. The wrapper goroutine exits when the underlying channel closes
// (backplane Close) or ctx is cancelled, so it cannot leak.
func (f Faulty) Subscribe(ctx context.Context, key string, from via.Offset) (<-chan via.Record, error) {
	in, err := f.Backplane.Subscribe(ctx, key, from)
	if err != nil {
		return nil, err
	}
	out := make(chan via.Record)
	go func() {
		defer close(out)
		for r := range in {
			for range f.Redeliver + 1 {
				select {
				case out <- r:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}
