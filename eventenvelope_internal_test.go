package via

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// envEv is a sample StateAppEvents event for envelope/projector tests: its fold
// appends N to the running list.
type envEv struct{ N int }

func (envEv) Fold(acc []int, e envEv) []int {
	return append(append([]int(nil), acc...), e.N)
}

// goodEnv wraps an event in a current-version (v1) envelope, exactly as Append
// will on the wire.
func goodEnv(t *testing.T, e envEv) []byte {
	t.Helper()
	d, _ := json.Marshal(e)
	b, err := json.Marshal(eventEnvelope{T: "envEv", V: currentEventVersion, D: d})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// futureEnv wraps an event in a version NEWER than this binary understands.
func futureEnv(t *testing.T, e envEv) []byte {
	t.Helper()
	d, _ := json.Marshal(e)
	b, _ := json.Marshal(eventEnvelope{T: "envEv", V: currentEventVersion + 1, D: d})
	return b
}

// spyMetrics records the names of Counter() calls so tests can assert which
// observability signals fired.
type spyMetrics struct {
	mu       sync.Mutex
	counters []string
}

func (m *spyMetrics) Counter(name string, _ ...string)          { m.mu.Lock(); m.counters = append(m.counters, name); m.mu.Unlock() }
func (m *spyMetrics) Gauge(string, float64, ...string)          {}
func (m *spyMetrics) Histogram(string, float64, ...string)      {}
func (m *spyMetrics) saw(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.counters {
		if c == name {
			return true
		}
	}
	return false
}

// bindLog wires a StateAppEvents[envEv,[]int] to app under key and returns the
// per-key foldBytes the projector uses, so its decode outcomes can be asserted
// directly.
func bindLog(app *App, key string) func(any, []byte) (any, error) {
	var h StateAppEvents[envEv, []int]
	h.bindWireKey(key)
	h.bindApp(app)
	return app.logs[key].foldBytes
}

// The version envelope is the whole point of P4: foldBytes must decode a
// current-version record into a fold, DROP a poison/unknown record as
// ErrUndecodable (so it can be skipped, never panicking the pod), and refuse a
// NEWER-than-this-binary record as ErrForwardIncompatible (so an old binary
// halts rather than mis-folds a future event).
func TestFoldBytesClassifiesEnvelopeByVersion(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	fold := bindLog(app, "k")

	// (a) well-formed current-version envelope → folds.
	next, err := fold([]int(nil), goodEnv(t, envEv{N: 7}))
	if err != nil {
		t.Fatalf("good envelope must fold, got err %v", err)
	}
	if got, _ := next.([]int); len(got) != 1 || got[0] != 7 {
		t.Fatalf("fold result = %v, want [7]", next)
	}

	// (b) garbage bytes (no envelope) → ErrUndecodable.
	if _, err := fold([]int{1}, []byte("garbage")); err != ErrUndecodable {
		t.Fatalf("garbage err = %v, want ErrUndecodable", err)
	}

	// (c) newer version → ErrForwardIncompatible.
	if _, err := fold([]int{1}, futureEnv(t, envEv{N: 9})); err != ErrForwardIncompatible {
		t.Fatalf("future-version err = %v, want ErrForwardIncompatible", err)
	}

	// (d) current envelope with an undecodable payload → ErrUndecodable.
	bad, _ := json.Marshal(eventEnvelope{T: "envEv", V: currentEventVersion, D: json.RawMessage(`"not-an-object"`)})
	if _, err := fold([]int{1}, bad); err != ErrUndecodable {
		t.Fatalf("bad-payload err = %v, want ErrUndecodable", err)
	}
}

func projection(app *App, key string) []int {
	v, ok := app.logProjection(key)
	if !ok {
		return nil
	}
	got, _ := v.([]int)
	return got
}

// logCursor reads the projector's applied-offset cursor under its lock, so the
// roll-forward invariants (poison advances the cursor, a halt freezes it) can be
// asserted race-free.
func logCursor(app *App, key string) Offset {
	ls := app.logs[key]
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.cursor
}

// A poison record in the log must NEVER wedge the projector: it is skipped
// (cursor advances past it, projection unaffected) and the following good
// events still fold — so one bad write can't freeze the whole key for every
// pod. A via.events.undecodable metric records that it happened.
func TestProjectorSkipsPoisonRecordAndKeepsFolding(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()
	bindLog(app, "k")
	ctx := context.Background()

	_, _ = app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 1})) // offset 1
	_, _ = app.backplane.Append(ctx, "k", []byte("garbage"))       // offset 2 — poison
	_, _ = app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 2})) // offset 3

	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 2 && p[0] == 1 && p[1] == 2
	}, 2*time.Second, 10*time.Millisecond,
		"projector must skip the poison and fold both good events")

	// The cursor must have ADVANCED past the poison (to offset 3), so the poison
	// is never retried forever.
	require.Equal(t, Offset(3), logCursor(app, "k"), "cursor must advance past a skipped poison record")
	require.True(t, spy.saw("via.events.undecodable"), "a dropped poison record must emit via.events.undecodable")
}

// A record written by a NEWER binary must HALT this key's projector rather than
// mis-fold it: the projection freezes at the last good value, and even a later
// well-formed event is not applied (roll forward, not back). A
// via.events.forward_incompatible metric records the halt.
func TestProjectorHaltsOnForwardIncompatibleRecord(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()
	bindLog(app, "k")
	ctx := context.Background()

	_, _ = app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 1})) // offset 1
	require.Eventually(t, func() bool { return len(projection(app, "k")) == 1 },
		2*time.Second, 10*time.Millisecond, "first good event must fold")

	_, _ = app.backplane.Append(ctx, "k", futureEnv(t, envEv{N: 2})) // offset 2 — halts the key
	_, _ = app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 3}))   // offset 3 — must NOT be folded

	require.Eventually(t, func() bool { return spy.saw("via.events.forward_incompatible") },
		2*time.Second, 10*time.Millisecond, "a forward-incompatible record must emit the metric")

	// The halt is durable: projection frozen at [1] AND the cursor frozen at
	// offset 1 (NOT advanced past the forward-incompatible record), so a
	// roll-forward redeploy resumes correctly from there.
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, []int{1}, projection(app, "k"), "a halted projector must freeze at the last good value")
	require.Equal(t, Offset(1), logCursor(app, "k"), "a halted projector must NOT advance the cursor (roll-forward-only)")
}
