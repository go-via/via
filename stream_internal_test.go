package via

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// keepalive is not user-reachable through the public API (it's the SSE
// heartbeat frame, wired internally in via.go), so the only way to prove a
// panic there survives is against runStream directly.
func TestRunStream_keepalivePanicDoesNotKillTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reqCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pushq := make(chan func())
		var beats atomic.Int32
		keepalive := func() {
			beats.Add(1)
			panic("via_test: keepalive exploded")
		}
		done := make(chan struct{})
		go func() {
			runStream(reqCtx, nil, pushq, keepalive, time.Millisecond)
			close(done)
		}()

		synctest.Wait()
		time.Sleep(5 * time.Millisecond) // let the beat ticker fire (and panic) at least once
		synctest.Wait()

		ran := make(chan struct{})
		pushq <- func() { close(ran) }
		<-ran // the stream goroutine must still be dispatching push items

		cancel()
		<-done
	})
}
