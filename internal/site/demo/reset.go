package demo

import (
	"context"
	"time"
)

// Reset wipes shared demo state on a timer, so one visitor's mess does not
// greet the next. It returns immediately; the ticker stops with ctx.
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
