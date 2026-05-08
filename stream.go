package via

import "time"

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
func Stream(ctx *Ctx, interval time.Duration, fn func(ctx *Ctx, t time.Time)) {
	if ctx == nil || interval <= 0 || fn == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.doneChan:
				return
			case t := <-ticker.C:
				safeStreamFn(ctx, t, fn)
				if ctx.stateDirty || ctx.dirtySignals.any() {
					flushDirty(ctx)
				}
			}
		}
	}()
}

func safeStreamFn(ctx *Ctx, t time.Time, fn func(*Ctx, time.Time)) {
	defer func() {
		if rec := recover(); rec != nil && ctx.app != nil {
			ctx.app.logErr(ctx, "Stream callback panicked: %v", rec)
		}
	}()
	fn(ctx, t)
}
