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
// panic there survives is against runLiveStream directly.
func TestRunLiveStream_keepalivePanicDoesNotKillTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reqCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pulse := make(chan func())
		var beats atomic.Int32
		keepalive := func() {
			beats.Add(1)
			panic("via_test: keepalive exploded")
		}
		done := make(chan struct{})
		go func() {
			runLiveStream(reqCtx, nil, pulse, keepalive, time.Millisecond)
			close(done)
		}()

		synctest.Wait()
		time.Sleep(5 * time.Millisecond) // let the beat ticker fire (and panic) at least once
		synctest.Wait()

		ran := make(chan struct{})
		pulse <- func() { close(ran) }
		<-ran // the island goroutine must still be dispatching pulse items

		cancel()
		<-done
	})
}
