package via

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// The fold-divergence canary is the cheap cross-pod safety net: after every fold
// a pod emits its applied offset AND a digest of the resulting projection. Two
// pods folding the SAME event sequence MUST report the same (offset, digest), so
// an operator comparing the two gauges across pods can detect a non-deterministic
// fold before it corrupts a snapshot. So the digest must be a pure function of
// the folded projection — identical inputs → identical digest.
func TestFoldDigest_isDeterministicForTheSameSequence(t *testing.T) {
	t.Parallel()
	off1, dig1 := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 3)
	off2, dig2 := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 3)
	require.Equal(t, off1, off2, "same sequence → same applied offset")
	require.Equal(t, dig1, dig2, "same sequence → identical projection digest")
	require.NotZero(t, off1)
}

// A DIFFERENT projection at the SAME offset must produce a DIFFERENT digest, or
// the canary is useless — it would report agreement even when two pods diverged.
// Both sequences fold three events (→ offset 3), so a digest that merely echoes
// the offset would compare equal here and be rejected.
func TestFoldDigest_differsForDifferentProjections(t *testing.T) {
	t.Parallel()
	offA, digA := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 3)
	offB, digB := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 4)
	require.Equal(t, offA, offB, "both fold three events → same applied offset 3")
	require.NotEqual(t, digA, digB, "different projections at the same offset must yield different digests")
}

// The digest gauge must carry an "offset" label matching the applied cursor:
// the canary triple is (key, offset, digest), and an operator correlates a
// cross-pod digest MISMATCH to the exact offset it occurred at. A digest gauge
// without the offset label would force comparing two unanchored hash streams.
func TestFoldDigest_gaugeCarriesOffsetLabel(t *testing.T) {
	t.Parallel()
	gs := &gaugeSpy{}
	off, _ := foldKEvents(t, gs, "k", 1, 2, 3)
	gotOff, ok := gs.latestLabel("via.fold.digest", "k", "offset")
	require.True(t, ok, "via.fold.digest must carry an offset label")
	require.Equal(t, strconv.FormatUint(uint64(off), 10), gotOff,
		"digest offset label must match the applied cursor (the via.fold.offset gauge)")
}

// Within a single pod the digest must track the projection as it grows — a
// digest that hashes only part of the state (or a constant) would not change as
// events accumulate, blinding the canary to a fold that stopped advancing.
func TestFoldDigest_tracksProjectionGrowth(t *testing.T) {
	t.Parallel()
	off2, dig2 := foldKEvents(t, &gaugeSpy{}, "k", 1, 2)
	off3, dig3 := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 3)
	require.NotEqual(t, off2, off3, "offset must advance as events accumulate")
	require.NotEqual(t, dig2, dig3, "digest must change as the projection grows")
}
