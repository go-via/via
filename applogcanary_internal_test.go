package via

import (
	"context"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gaugeSpy captures Gauge(name,value,labels...) calls so the fold-divergence
// canary can be asserted: the (key, offset, digest) triple a pod emits after
// each fold is the cheap cross-pod divergence signal (council T1-SRE-7).
type gaugeSpy struct {
	mu     sync.Mutex
	gauges []gaugeSample
}

type gaugeSample struct {
	name   string
	value  float64
	labels []string
}

func (g *gaugeSpy) Counter(string, ...string) {}
func (g *gaugeSpy) Gauge(name string, value float64, labels ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gauges = append(g.gauges, gaugeSample{name, value, append([]string(nil), labels...)})
}
func (g *gaugeSpy) Histogram(string, float64, ...string) {}

// latest returns the value of the most recent gauge sample named `name` whose
// labels contain key=wantKey, plus whether any was seen.
func (g *gaugeSpy) latest(name, wantKey string) (float64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var v float64
	found := false
	for _, s := range g.gauges {
		if s.name != name {
			continue
		}
		for i := 0; i+1 < len(s.labels); i += 2 {
			if s.labels[i] == "key" && s.labels[i+1] == wantKey {
				v, found = s.value, true
			}
		}
	}
	return v, found
}

// latestLabel returns the value of label `want` on the most recent gauge sample
// named `name` whose labels contain key=wantKey, plus whether such a sample was
// seen carrying that label.
func (g *gaugeSpy) latestLabel(name, wantKey, want string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var got string
	found := false
	for _, s := range g.gauges {
		if s.name != name {
			continue
		}
		hasKey := false
		var label string
		hasLabel := false
		for i := 0; i+1 < len(s.labels); i += 2 {
			switch s.labels[i] {
			case "key":
				hasKey = s.labels[i+1] == wantKey
			case want:
				label, hasLabel = s.labels[i+1], true
			}
		}
		if hasKey && hasLabel {
			got, found = label, true
		}
	}
	return got, found
}

func foldKEvents(t *testing.T, gs *gaugeSpy, key string, ns ...int) (float64, float64) {
	t.Helper()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(gs))
	t.Cleanup(server.Close)
	bindLog(app, key)
	ctx := context.Background()
	for _, n := range ns {
		if _, err := app.backplane.Append(ctx, key, goodEnv(t, envEv{N: n})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	require.Eventually(t, func() bool { return len(projection(app, key)) == len(ns) },
		2*time.Second, 10*time.Millisecond, "all events must fold")
	off, oko := gs.latest("via.fold.offset", key)
	dig, okd := gs.latest("via.fold.digest", key)
	require.True(t, oko, "projector must emit via.fold.offset after folding")
	require.True(t, okd, "projector must emit via.fold.digest after folding")
	return off, dig
}

// The fold-divergence canary is the cheap cross-pod safety net: after every fold
// a pod emits its applied offset AND a digest of the resulting projection. Two
// pods folding the SAME event sequence MUST report the same (offset, digest), so
// an operator comparing the two gauges across pods can detect a non-deterministic
// fold before it corrupts a snapshot. So the digest must be a pure function of
// the folded projection — identical inputs → identical digest.
func TestFoldDigestIsDeterministicForTheSameSequence(t *testing.T) {
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
func TestFoldDigestDiffersForDifferentProjections(t *testing.T) {
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
func TestFoldDigestGaugeCarriesOffsetLabel(t *testing.T) {
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
func TestFoldDigestTracksProjectionGrowth(t *testing.T) {
	t.Parallel()
	off2, dig2 := foldKEvents(t, &gaugeSpy{}, "k", 1, 2)
	off3, dig3 := foldKEvents(t, &gaugeSpy{}, "k", 1, 2, 3)
	require.NotEqual(t, off2, off3, "offset must advance as events accumulate")
	require.NotEqual(t, dig2, dig3, "digest must change as the projection grows")
}
