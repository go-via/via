package via

import (
	"sync/atomic"
	"time"
)

// Ticker is the handle returned by [Stream]. It lets the caller pause,
// resume, or change the cadence of the running ticker. The underlying
// goroutine stops automatically when ctx is disposed; calling Pause /
// Resume / SetInterval on a stopped ticker is a no-op.
type Ticker struct {
	paused   atomic.Bool
	interval atomic.Int64  // nanoseconds; read by the goroutine after each reset
	reset    chan struct{} // wakes the goroutine when interval changes
}

// Pause stops further callbacks from firing until Resume is called.
// In-flight callbacks complete normally.
func (t *Ticker) Pause() {
	if t == nil {
		return
	}
	t.paused.Store(true)
}

// Resume restarts callbacks after a Pause. No-op on a running ticker.
func (t *Ticker) Resume() {
	if t == nil {
		return
	}
	t.paused.Store(false)
}

// Paused reports whether the ticker is currently paused.
func (t *Ticker) Paused() bool {
	if t == nil {
		return false
	}
	return t.paused.Load()
}

// SetInterval changes the tick cadence to d. The new interval takes
// effect on the next tick boundary; the current in-flight callback
// (if any) is unaffected. Non-positive d is a no-op.
func (t *Ticker) SetInterval(d time.Duration) {
	if t == nil || d <= 0 {
		return
	}
	t.interval.Store(int64(d))
	select {
	case t.reset <- struct{}{}:
	default: // a reset is already pending; the goroutine will pick up the latest value
	}
}

// Stream runs fn on a ticker until ctx is disposed. Use it in OnConnect
// to drive periodic UI updates without managing a goroutine and ticker by
// hand:
//
//	func (p *Page) OnConnect(ctx *via.Ctx) error {
//	    via.Stream(ctx, time.Second, func(ctx *via.Ctx, t time.Time) {
//	        p.Now.Set(ctx, t.Format("15:04:05"))
//	    })
//	    return nil
//	}
//
// fn runs on the same goroutine for every tick; it must return promptly.
// Long work should be offloaded with its own goroutine that observes
// ctx.Done(). After fn returns, dirty signals/state are auto-flushed.
//
// Stream takes the per-Ctx action mutex for the duration of fn, so the
// fn body has the same exclusivity guarantees as an action handler:
// Signal/State writes don't race with concurrent action POSTs or with
// other Stream callbacks on the same Ctx.
//
// The returned [*Ticker] lets the caller pause, resume, or change the
// cadence at runtime. It is safe to ignore the return value if those
// controls are not needed.
func Stream(ctx *Ctx, interval time.Duration, fn func(ctx *Ctx, t time.Time)) *Ticker {
	if ctx == nil || interval <= 0 || fn == nil {
		return nil
	}
	t := &Ticker{reset: make(chan struct{}, 1)}
	t.interval.Store(int64(interval))
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.doneChan:
				return
			case <-t.reset:
				ticker.Reset(time.Duration(t.interval.Load()))
			case now := <-ticker.C:
				if t.paused.Load() {
					continue
				}
				streamTick(ctx, now, fn)
			}
		}
	}()
	return t
}

// streamTick runs one fn invocation under actionMu and flushes any
// dirty state before releasing the lock — same exclusivity as an
// action handler, so fn's reads/writes don't race with a concurrent
// POST or another Stream callback on the same Ctx.
func streamTick(ctx *Ctx, t time.Time, fn func(*Ctx, time.Time)) {
	ctx.actionMu.Lock()
	defer ctx.actionMu.Unlock()
	defer flushDirty(ctx)
	defer recoverLog(ctx, "Stream callback")
	fn(ctx, t)
}
