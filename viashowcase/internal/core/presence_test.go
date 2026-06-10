package core_test

import (
	"testing"

	"github.com/go-via/viashowcase/internal/core"
	"github.com/stretchr/testify/assert"
)

// A join bumps the room's watcher count up.
func TestBumpPresenceIncrements(t *testing.T) {
	t.Parallel()
	got := core.BumpPresence(map[string]int{"r": 2}, "r", 1)
	assert.Equal(t, 3, got["r"])
}

// A leave bumps it down.
func TestBumpPresenceDecrements(t *testing.T) {
	t.Parallel()
	got := core.BumpPresence(map[string]int{"r": 2}, "r", -1)
	assert.Equal(t, 1, got["r"])
}

// A dispose without a matching connect (reconnect race) must never drive the
// count negative — a "-1 watching" pill would be nonsense.
func TestBumpPresenceNeverGoesNegative(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 0, core.BumpPresence(map[string]int{"r": 0}, "r", -1)["r"])
	assert.Equal(t, 0, core.BumpPresence(nil, "r", -1)["r"], "missing code starts at 0")
	assert.Equal(t, 0, core.BumpPresence(map[string]int{"r": 1}, "r", -5)["r"])
}

// The first watcher of a brand-new room starts from zero.
func TestBumpPresenceFromMissingKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1, core.BumpPresence(nil, "r", 1)["r"])
	assert.Equal(t, 1, core.BumpPresence(map[string]int{}, "r", 1)["r"])
}

// Other rooms' counts are untouched.
func TestBumpPresencePreservesOtherRooms(t *testing.T) {
	t.Parallel()
	got := core.BumpPresence(map[string]int{"a": 1, "b": 2}, "a", 1)
	assert.Equal(t, 2, got["a"])
	assert.Equal(t, 2, got["b"])
}

// The input map must never be mutated — peers share the prior StateApp value.
// Both paths matter: a clamp/decrement impl that mutates-and-returns the same
// map could hide behind a 0 result, so check the input after a decrement too.
func TestBumpPresenceDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	up := map[string]int{"r": 2}
	_ = core.BumpPresence(up, "r", 1)
	assert.Equal(t, 2, up["r"], "input must be untouched on increment")

	down := map[string]int{"r": 1}
	_ = core.BumpPresence(down, "r", -5) // clamps to 0
	assert.Equal(t, 1, down["r"], "input must be untouched on a clamped decrement")
}

// A no-op delta leaves the count where it was.
func TestBumpPresenceZeroDeltaIsNoChange(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 5, core.BumpPresence(map[string]int{"r": 5}, "r", 0)["r"])
}
