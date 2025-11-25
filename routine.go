package via

import (
	"sync"
	"sync/atomic"
	"time"
)

// Routine allows for defining concurrent goroutines safely. Goroutines started by *Routine
// are tied to the *Context lifecycle.
type Routine struct {
	mu             sync.Mutex
	ctxDisposed    chan struct{}
	localInterrupt chan struct{}
	isRunning      atomic.Bool
	routineFn      func()
	tckDuration    time.Duration
	tkr            *time.Ticker
}

// OnInterval starts a go routine that sets a time.Ticker with the given duration and executes
// the given func() on every tick. Use *Routine.UpdateInterval to update the interval.
// If the routine is running, it is stopped.
func (r *Routine) OnInterval(d time.Duration, fn func()) {
	if r.isRunning.Load() == true {
		r.Stop()
	}
	r.tckDuration = d
	r.routineFn = func() {
		r.tkr = time.NewTicker(r.tckDuration)
		defer r.tkr.Stop() // clean up the ticker when routine stops
		for {
			select {
			case <-r.ctxDisposed: // dispose of the routine when ctx is disposed
				return
			case <-r.localInterrupt: // dispose of the routine on interrupt signal
				return
			case <-r.tkr.C:
				fn()
			}
		}
	}
}

// UpdateInterval sets a new interval duration for the internal *time.Ticker. If the provided
// duration is equal of less than 0, UpdateInterval does nothing.
func (r *Routine) UpdateInterval(d time.Duration) {
	r.tckDuration = d
	r.tkr.Reset(d)

}

// Start executes the predifined goroutine. If no predifined goroutine exists, or it already
// started, Start does nothing.
func (r *Routine) Start() {
	if !r.isRunning.CompareAndSwap(false, true) || r.routineFn == nil {
		return
	}
	go r.routineFn()
}

// Stop interrupts the predifined goroutine. If no predifined goroutine exists, or it already
// ustopped, Stop does nothing.
func (r *Routine) Stop() {
	if !r.isRunning.CompareAndSwap(true, false) || r.routineFn == nil {
		return
	}
	r.localInterrupt <- struct{}{}
}

func newRoutine(ctxDisposedChan chan struct{}) *Routine {
	return &Routine{
		ctxDisposed:    ctxDisposedChan,
		localInterrupt: make(chan struct{}),
	}
}
