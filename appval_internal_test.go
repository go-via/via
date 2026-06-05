package via

import (
	"context"
	"encoding/json"
	"testing"
)

// loadStub is a minimal Backplane whose LoadSnapshot returns a scripted
// (bytes, rev, ok) so applyChange's reconcile gates can be exercised in
// isolation — no real backend needed. Other methods are inert.
type loadStub struct {
	data  []byte
	rev   Rev
	ok    bool
	loads []string // keys LoadSnapshot was asked for (call recorder)
}

func (s *loadStub) LoadSnapshot(_ context.Context, key string) ([]byte, Rev, bool, error) {
	s.loads = append(s.loads, key)
	return s.data, s.rev, s.ok, nil
}
func (s *loadStub) CAS(context.Context, string, Rev, []byte) (Rev, error)  { return 0, nil }
func (s *loadStub) Append(context.Context, string, []byte) (Offset, error) { return 0, nil }
func (s *loadStub) Head(context.Context, string) (Offset, Epoch, error)    { return 0, 0, nil }
func (s *loadStub) Subscribe(context.Context, string, Offset) (<-chan Record, error) {
	return make(chan Record), nil
}
func (s *loadStub) Close() error { return nil }

func intCell() *valCell {
	return &valCell{decode: func(b []byte) (any, error) {
		var i int
		if err := json.Unmarshal(b, &i); err != nil {
			return nil, err
		}
		return i, nil
	}}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// applyChange must treat the changes feed as a pure liveness hint that triggers
// a re-pull of the authoritative Store cell — never as the value carrier. Its
// two gates are load-bearing for cluster correctness: a stale replica read
// (storeRev < the hint's rev) must NOT be applied (T1-SRE-5), and a redelivered
// or out-of-order older change must NOT regress L1 (T3-SRE-1 monotone gate).
func TestApplyChangeDropsStaleReadsAndNeverRegressesL1(t *testing.T) {
	stub := &loadStub{}
	app := &App{backplane: stub, valStates: map[string]*valCell{"k": intCell()}}
	vc := app.valStates["k"]

	// Stale replica: the hint promises rev 5 but the Store read lags at rev 3.
	// Applying it would surface a value older than the cluster already advanced
	// past — drop and wait.
	stub.data, stub.rev, stub.ok = mustJSON(70), 3, true
	app.applyChange(change{Key: "k", Rev: 5})
	if vc.l1 != nil {
		t.Fatalf("stale read (storeRev 3 < hint rev 5) must not apply; l1 = %v", vc.l1)
	}

	// The Store catches up to rev 5 → now the re-pull applies.
	stub.data, stub.rev, stub.ok = mustJSON(77), 5, true
	app.applyChange(change{Key: "k", Rev: 5})
	if vc.l1 != 77 || vc.l1Rev != 5 {
		t.Fatalf("caught-up read must apply; l1=%v l1Rev=%d, want 77/5", vc.l1, vc.l1Rev)
	}

	// An older, redelivered change (rev 3) must be ignored — L1 is monotone.
	stub.data, stub.rev, stub.ok = mustJSON(33), 3, true
	app.applyChange(change{Key: "k", Rev: 3})
	if vc.l1 != 77 || vc.l1Rev != 5 {
		t.Fatalf("older change must not regress L1; l1=%v l1Rev=%d, want 77/5", vc.l1, vc.l1Rev)
	}

	// A poison Store snapshot (undecodable) must leave the last good value
	// intact rather than corrupt or panic the projection.
	stub.data, stub.rev, stub.ok = []byte("not-an-int"), 9, true
	app.applyChange(change{Key: "k", Rev: 9})
	if vc.l1 != 77 || vc.l1Rev != 5 {
		t.Fatalf("undecodable snapshot must keep the last good value; l1=%v l1Rev=%d, want 77/5", vc.l1, vc.l1Rev)
	}
}

// The reconcile sweep re-pulls a key to Store HEAD unconditionally, so its only
// guards are the monotone gate (never regress L1) and decode-safety (never
// corrupt on a poison snapshot). It must advance L1 when the Store moved ahead,
// and leave it untouched (and NOT signal a change) otherwise.
func TestReconcileKeyAdvancesOnlyForwardAndSurvivesPoison(t *testing.T) {
	stub := &loadStub{}
	app := &App{backplane: stub, valStates: map[string]*valCell{"k": intCell()}}
	vc := app.valStates["k"]

	// Store ahead of L1 → advance.
	stub.data, stub.rev, stub.ok = mustJSON(42), 4, true
	app.reconcileKey("k")
	if vc.l1 != 42 || vc.l1Rev != 4 {
		t.Fatalf("reconcile must advance to Store HEAD; l1=%v l1Rev=%d, want 42/4", vc.l1, vc.l1Rev)
	}

	// Store not ahead (same rev) → no-op, no regression.
	stub.data, stub.rev, stub.ok = mustJSON(999), 4, true
	app.reconcileKey("k")
	if vc.l1 != 42 || vc.l1Rev != 4 {
		t.Fatalf("reconcile at an unchanged rev must be a no-op; l1=%v l1Rev=%d, want 42/4", vc.l1, vc.l1Rev)
	}

	// Poison snapshot at a higher rev → keep the last good value.
	stub.data, stub.rev, stub.ok = []byte("nope"), 7, true
	app.reconcileKey("k")
	if vc.l1 != 42 || vc.l1Rev != 4 {
		t.Fatalf("reconcile must survive a poison snapshot; l1=%v l1Rev=%d, want 42/4", vc.l1, vc.l1Rev)
	}

	// Absent cell → no panic.
	app.reconcileKey("missing")
}
