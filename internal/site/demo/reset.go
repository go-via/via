package demo

import (
	"context"
	"time"
)

// Reset runs fn on every tick until ctx is done. Shared demo state is wiped
// this way so one visitor's mess does not greet the next.
func Reset(ctx context.Context, every time.Duration, fn func()) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fn()
			}
		}
	}()
}
