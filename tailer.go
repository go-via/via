package via

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// Tailer reconnect backoff bounds. Deliberately NOT the CAS constants in
// backoff.go: CAS contention resolves in microseconds, a backend outage in
// milliseconds-to-seconds, so the tailer base matches the projector's
// historical 10ms re-subscribe pause and the cap keeps a dead backend polled
// rather than hammered — while a recovered one is re-tailed within a couple
// of seconds.
const (
	tailerBackoffBase = 10 * time.Millisecond
	tailerBackoffCap  = 2 * time.Second
)

// tailerBackoffCeiling is the exponential backoff ceiling for 1-based retry
// attempt n: base<<(n-1), clamped to the cap. The clamp also absorbs the
// int64 overflow a large shift would produce.
func tailerBackoffCeiling(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 32 {
		return tailerBackoffCap
	}
	d := tailerBackoffBase << (attempt - 1)
	if d <= 0 || d > tailerBackoffCap {
		return tailerBackoffCap
	}
	return d
}

// tailer describes one backplane feed consumer for tailLoop: which key it
// tails, where to resume on each (re)connect, and how to fold one record.
// resumeFrom and onRecord are both invoked only from the tailLoop goroutine,
// so state they share (a resume cursor) needs no synchronization of its own.
type tailer struct {
	// feed labels the via.backplane.tailer_* metrics: "changes", "broadcast",
	// or "projector:<key>".
	feed string
	// key is the EventLog key the tailer subscribes to.
	key string
	// resumeFrom yields the offset for the next Subscribe. Stateful tailers
	// (changes, projector) return their last-applied offset so a reconnect is
	// gap-free; the ephemeral broadcast tailer re-Heads so frames missed
	// during the gap are skipped, never replayed.
	resumeFrom func(ctx context.Context) (Offset, error)
	// onRecord folds one delivered record.
	onRecord func(rec Record)
}

// startTailer runs t's tail loop on a background goroutine tracked by bgWG,
// so Shutdown's drain waits for it like every other long-lived worker.
func (a *App) startTailer(t tailer) {
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		a.tailLoop(t)
	}()
}

// tailLoop is the ONE reconnect loop behind every backplane tailer (changes,
// broadcast, per-key projector), so a blip can never permanently strand one
// of them while the others survive. Each Subscribe tails until its channel
// closes; a close while the app is still running is a TRANSIENT disconnect
// (the backend dropped the consumer, the stream survives) — re-subscribe
// from resumeFrom with jittered exponential backoff. Boot-time resumeFrom /
// Subscribe failures retry under the same backoff instead of giving up
// forever. A close during Shutdown (backplaneDone) or an ErrClosed is a
// graceful stop — exit without reconnect churn.
//
// Observability: via.backplane.tailer_up{feed} is 1 while a subscription is
// live; via.backplane.tailer_reconnect{feed} counts re-establishments and is
// emitted only AFTER the fresh subscription is live, so an observer that saw
// it move can rely on subsequent appends being delivered.
func (a *App) tailLoop(t tailer) {
	m := a.metricsOrNoop()
	defer m.Gauge("via.backplane.tailer_up", 0, "feed", t.feed)
	connected := false
	attempt := 0
	for {
		if a.shuttingDown() {
			return
		}
		from, err := t.resumeFrom(a.backplaneCtx)
		var ch <-chan Record
		if err == nil {
			ch, err = a.backplane.Subscribe(a.backplaneCtx, t.key, from)
		}
		if err != nil {
			if errors.Is(err, ErrClosed) || a.shuttingDown() {
				return // the backplane is gone for good; nothing to reconnect to
			}
			m.Gauge("via.backplane.tailer_up", 0, "feed", t.feed)
			attempt++
			a.logWarn(nil, "via: backplane subscribe failed for %s tailer (attempt %d), retrying: %v", t.feed, attempt, err)
			if !a.tailerSleep(attempt) {
				return
			}
			continue
		}
		attempt = 0
		m.Gauge("via.backplane.tailer_up", 1, "feed", t.feed)
		if connected {
			m.Counter("via.backplane.tailer_reconnect", "feed", t.feed)
		}
		connected = true

		// Range the channel, but also wake on backplaneDone so teardown is
		// prompt even against a backend that does not close the channel on
		// ctx cancel.
		open := true
		for open {
			select {
			case rec, ok := <-ch:
				if !ok {
					open = false // transient disconnect (or a close racing shutdown)
				} else {
					t.onRecord(rec)
				}
			case <-a.backplaneDone:
				return // graceful stop: don't wait for a slow backend to close ch
			}
		}
		m.Gauge("via.backplane.tailer_up", 0, "feed", t.feed)
		if a.shuttingDown() {
			return
		}
		attempt = 1
		if !a.tailerSleep(attempt) {
			return
		}
	}
}

// tailerSleep waits a jittered duration within [ceiling/2, ceiling] for the
// attempt before the next subscribe — half-jitter de-correlates a herd of
// pods re-tailing one recovered backend without ever retrying instantly.
// Returns false when the app began shutting down mid-wait, so a tailer never
// holds the drain hostage for a backoff interval.
func (a *App) tailerSleep(attempt int) bool {
	ceiling := tailerBackoffCeiling(attempt)
	half := ceiling / 2
	timer := time.NewTimer(half + time.Duration(rand.Int64N(int64(half)+1)))
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-a.backplaneDone:
		return false
	case <-a.backplaneCtx.Done():
		return false
	}
}
