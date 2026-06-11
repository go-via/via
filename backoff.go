package via

import (
	"context"
	"math/rand/v2"
	"time"
)

// CAS retry backoff bounds. The retry loop used to spin with no delay, so a
// contended key (many tabs/pods Updating at once) burned CPU racing through
// all 100 attempts. The ceiling grows exponentially per attempt and is capped
// small — CAS contention resolves in microseconds, so the cap stays well under
// a frame to avoid adding perceptible action latency.
const (
	casBackoffBase = 100 * time.Microsecond
	casBackoffCap  = 10 * time.Millisecond
)

// casBackoffCeiling is the exponential backoff ceiling for 0-based retry
// attempt n: base<<n, clamped to the cap. The clamp also absorbs the int64
// overflow a large shift would produce (which would otherwise wrap negative).
func casBackoffCeiling(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= 32 {
		return casBackoffCap
	}
	d := casBackoffBase << attempt
	if d <= 0 || d > casBackoffCap {
		return casBackoffCap
	}
	return d
}

// casSleep waits a jittered duration in [0, ceiling) before the next CAS retry,
// so concurrent retriers de-correlate instead of colliding in lockstep. It
// returns early if ctx is cancelled — a contended Update mid-retry must not add
// up to the ceiling to a shutdown drain.
func casSleep(ctx context.Context, attempt int) {
	ceiling := casBackoffCeiling(attempt)
	if ceiling <= 0 {
		return
	}
	t := time.NewTimer(time.Duration(rand.Int64N(int64(ceiling))))
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
