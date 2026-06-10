package via

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The CAS retry loop spun with no delay, burning CPU under contention. The
// backoff ceiling must grow with the attempt (so contenders spread out) yet
// stay bounded (so a hot key never sleeps unboundedly) — the sleep itself
// jitters within [0, ceiling) to de-correlate concurrent retriers.
func TestCasBackoffCeiling_growsAndIsCapped(t *testing.T) {
	t.Parallel()

	assert.Equal(t, casBackoffBase, casBackoffCeiling(0),
		"attempt 0 starts at the base delay")
	assert.Equal(t, 2*casBackoffBase, casBackoffCeiling(1),
		"each attempt doubles the ceiling")

	assert.GreaterOrEqual(t, casBackoffCeiling(3), casBackoffCeiling(2),
		"the ceiling must be monotonically non-decreasing")

	assert.Equal(t, casBackoffCap, casBackoffCeiling(99),
		"a large attempt is clamped to the cap, never overflowing to a negative")
	assert.LessOrEqual(t, casBackoffCeiling(50), casBackoffCap,
		"the ceiling never exceeds the cap")
	assert.Positive(t, int64(casBackoffCeiling(64)),
		"a huge shift must not overflow to zero or negative")
}

func TestCasBackoffCeiling_negativeAttemptIsBase(t *testing.T) {
	t.Parallel()

	assert.Equal(t, casBackoffBase, casBackoffCeiling(-1),
		"a defensive negative attempt floors to the base, not a panic")
}

func TestCasSleep_staysWithinCeiling(t *testing.T) {
	t.Parallel()

	// casSleep must never block longer than the cap — bound the worst case so
	// a contended Update can't stall an action for an unbounded time.
	start := time.Now()
	casSleep(99)
	assert.Less(t, time.Since(start), casBackoffCap+50*time.Millisecond,
		"a single backoff sleep stays bounded by the cap (plus scheduler slack)")
}
