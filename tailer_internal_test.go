package via

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A tailer facing a dead backend must spread its re-subscribes out (ceiling
// grows per attempt) yet recover promptly when the backend returns (ceiling
// is capped) — the same contract as the CAS backoff, at outage scale.
func TestTailerBackoffCeiling_growsAndIsCapped(t *testing.T) {
	t.Parallel()

	assert.Equal(t, tailerBackoffBase, tailerBackoffCeiling(1),
		"the first retry starts at the base delay")
	assert.Equal(t, 2*tailerBackoffBase, tailerBackoffCeiling(2),
		"each attempt doubles the ceiling")
	assert.GreaterOrEqual(t, tailerBackoffCeiling(4), tailerBackoffCeiling(3),
		"the ceiling must be monotonically non-decreasing")
	assert.Equal(t, tailerBackoffCap, tailerBackoffCeiling(99),
		"a large attempt is clamped to the cap, never overflowing to a negative")
	assert.Positive(t, int64(tailerBackoffCeiling(64)),
		"a huge shift must not overflow to zero or negative")
}

func TestTailerBackoffCeiling_nonPositiveAttemptIsBase(t *testing.T) {
	t.Parallel()

	assert.Equal(t, tailerBackoffBase, tailerBackoffCeiling(0),
		"a defensive zero attempt floors to the base, not a panic")
	assert.Equal(t, tailerBackoffBase, tailerBackoffCeiling(-3),
		"a defensive negative attempt floors to the base, not a panic")
}
