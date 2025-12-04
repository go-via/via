package via

import (
	"sync"
	"sync/atomic"
	"time"
)

// OnIntervalRoutine allows for defining concurrent goroutines safely. Goroutines started by *OnIntervalRoutine
// are tied to the *Context lifecycle.
type OnIntervalRoutine struct {
	mu             sync.RWMutex
	ctxDisposed    chan struct{}
	localInterrupt chan struct{}
	isRunning      atomic.Bool
	routineFn      func()
	tckDuration    time.Duration
	updateTkrChan  chan time.Duration
}

// UpdateInterval sets a new interval duration for the internal *time.Ticker. If the provided
// duration is equal of less than 0, UpdateInterval does nothing.
func (r *OnIntervalRoutine) UpdateInterval(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tckDuration = d
	r.updateTkrChan <- d

}

// Start executes the predifined goroutine. If no predifined goroutine exists, or it already
// started, Start does nothing.
func (r *OnIntervalRoutine) Start() {
	if !r.isRunning.CompareAndSwap(false, true) || r.routineFn == nil {
		return
	}
	go r.routineFn()
}

// Stop interrupts the predifined goroutine. If no predifined goroutine exists, or it already
// ustopped, Stop does nothing.
func (r *OnIntervalRoutine) Stop() {
	if !r.isRunning.CompareAndSwap(true, false) || r.routineFn == nil {
		return
	}
	r.localInterrupt <- struct{}{}
}

func newOnIntervalRoutine(ctxDisposedChan chan struct{}, duration time.Duration, handler func()) *OnIntervalRoutine {
	r := &OnIntervalRoutine{
		ctxDisposed:    ctxDisposedChan,
		localInterrupt: make(chan struct{}),
		updateTkrChan:  make(chan time.Duration),
	}
	r.tckDuration = duration
	r.routineFn = func() {
		r.mu.RLock()
		tkr := time.NewTicker(r.tckDuration)
		r.mu.RUnlock()
		defer tkr.Stop() // clean up the ticker when routine stops
		for {
			select {
			case <-r.ctxDisposed: // dispose of the routine when ctx is disposed
				return
			case <-r.localInterrupt: // dispose of the routine on interrupt signal
				return
			case d := <-r.updateTkrChan:
				tkr.Reset(d)
			case <-tkr.C:
				handler()
			}
		}
	}
	return r
}
