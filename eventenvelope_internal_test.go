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

func (m *spyMetrics) Counter(name string, _ ...string) {
	m.mu.Lock()
	m.counters = append(m.counters, name)
	m.mu.Unlock()
}
func (m *spyMetrics) Gauge(string, float64, ...string)     {}
func (m *spyMetrics) Histogram(string, float64, ...string) {}
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
	app := New()
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
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
	app := New(WithMetrics(spy))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
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
	app := New(WithMetrics(spy))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
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

// renamed is a test event whose CURRENT (v2) shape renamed the field "msg" → "text".
// Its fold collects the texts. A v1 record stored "msg"; an upcaster rewrites it.
type renamed struct {
	Text string `json:"text"`
}

func (renamed) Fold(acc []string, e renamed) []string {
	return append(append([]string(nil), acc...), e.Text)
}

// A reshaped event (field renamed) must still fold old stored records: the
// registered upcaster migrates a v1 payload into the current v2 shape BEFORE the
// fold, so Fold only ever sees current-shape E. This is the whole point of the
// upcaster chain — events are immortal, so old wire bytes must keep decoding.
//
//nolint:paralleltest // mutates the process-global event-version registry
func TestRegisteredUpcasterMigratesOldRecordsBeforeFold(t *testing.T) {
	// v1 stored {"msg": X}; v2 is {"text": X}. Upcaster renames the field.
	RegisterEvent[renamed](1, func(old json.RawMessage) (json.RawMessage, error) {
		var v1 struct {
			Msg string `json:"msg"`
		}
		if err := json.Unmarshal(old, &v1); err != nil {
			return nil, err
		}
		return json.Marshal(renamed{Text: v1.Msg})
	})

	// Registration bumped the current version of `renamed` to 2.
	require.Equal(t, 2, currentVersionFor[renamed](), "one 1→2 upcaster makes the current version 2")

	app := New()
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	defer server.Close()
	var h StateAppEvents[renamed, []string]
	h.bindWireKey("r")
	h.bindApp(app)
	fold := app.logs["r"].foldBytes

	// A v1 envelope (old "msg" shape) must upcast then fold into the v2 value.
	v1d, _ := json.Marshal(map[string]string{"msg": "hello"})
	v1env, _ := json.Marshal(eventEnvelope{T: "renamed", V: 1, D: v1d})
	next, err := fold([]string(nil), v1env)
	require.NoError(t, err, "a v1 record must upcast, not error")
	require.Equal(t, []string{"hello"}, next, "v1 payload must fold into the current shape")

	// A current (v2) envelope folds directly, no upcasting.
	v2d, _ := json.Marshal(renamed{Text: "world"})
	v2env, _ := json.Marshal(eventEnvelope{T: "renamed", V: 2, D: v2d})
	next2, err := fold([]string{"hello"}, v2env)
	require.NoError(t, err)
	require.Equal(t, []string{"hello", "world"}, next2)
}

// A stored record we cannot migrate to the current version must be DROPPED
// (ErrUndecodable), never mis-folded — reusing drop-on-undecodable so one
// un-upcastable record can't wedge the key.
//
//nolint:paralleltest // mutates the process-global event-version registry
func TestUnbridgeableVersionGapIsUndecodable(t *testing.T) {
	// gapEvent's current version is 3 (steps 1→2 and... 2→3 missing on purpose
	// would leave a gap; here we register only 2→3 so 1→2 is missing).
	RegisterEvent[gapEvent](2, func(old json.RawMessage) (json.RawMessage, error) { return old, nil })
	require.Equal(t, 3, currentVersionFor[gapEvent](), "a 2→3 upcaster makes the current version 3")

	// Migrating from v1 needs a 1→2 step, which is NOT registered → ErrUndecodable.
	_, err := runUpcasters[gapEvent](1, currentVersionFor[gapEvent](), json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrUndecodable, "a missing upcaster step must be ErrUndecodable")

	// A failing upcaster is likewise ErrUndecodable.
	RegisterEvent[failEvent](1, func(json.RawMessage) (json.RawMessage, error) {
		return nil, ErrUndecodable
	})
	_, err = runUpcasters[failEvent](1, currentVersionFor[failEvent](), json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrUndecodable, "a failing upcaster must be ErrUndecodable")
}

type racedEvent struct{ N int }

func (racedEvent) Fold(acc int, e racedEvent) int { return acc + e.N }

// The registry guards itself so that a stray concurrent registration is
// race-clean (eventenvelope.go invariant). runUpcasters fetches the version
// info then walks its steps; if that walk escapes the lock while RegisterEvent
// writes the same steps map, -race trips on a concurrent map access. Running
// both against one event type under -race is what would surface that bug.
//
//nolint:paralleltest // mutates the process-global event-version registry
func TestConcurrentRegisterAndUpcastIsRaceClean(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			RegisterEvent[racedEvent](1, func(old json.RawMessage) (json.RawMessage, error) {
				return old, nil
			})
		}()
		go func() {
			defer wg.Done()
			_, _ = runUpcasters[racedEvent](1, 2, json.RawMessage(`{}`))
		}()
	}
	wg.Wait()
}

// multiEv's current (v3) shape is {v3}; it evolved v1 {a} → v2 {b} → v3 {v3}.
type multiEv struct {
	V3 string `json:"v3"`
}

func (multiEv) Fold(acc []string, e multiEv) []string {
	return append(append([]string(nil), acc...), e.V3)
}

// A type that evolved across MULTIPLE versions must run the FULL upcaster chain
// (v1→v2→v3), not just the last step — otherwise a record from two reshapes ago
// would decode into garbage. This exercises the chain loop more than once.
//
//nolint:paralleltest // mutates the process-global event-version registry
func TestMultiStepUpcasterChainRunsEveryStep(t *testing.T) {
	RegisterEvent[multiEv](1, func(old json.RawMessage) (json.RawMessage, error) {
		var v1 struct {
			A string `json:"a"`
		}
		if err := json.Unmarshal(old, &v1); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"b": v1.A})
	})
	RegisterEvent[multiEv](2, func(old json.RawMessage) (json.RawMessage, error) {
		var v2 struct {
			B string `json:"b"`
		}
		if err := json.Unmarshal(old, &v2); err != nil {
			return nil, err
		}
		return json.Marshal(multiEv{V3: v2.B})
	})
	require.Equal(t, 3, currentVersionFor[multiEv](), "two chained upcasters make the current version 3")

	app := New()
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	defer server.Close()
	var h StateAppEvents[multiEv, []string]
	h.bindWireKey("m")
	h.bindApp(app)
	fold := app.logs["m"].foldBytes

	// A v1 record ({"a":"hi"}) must traverse BOTH steps to the v3 shape.
	v1d, _ := json.Marshal(map[string]string{"a": "hi"})
	v1env, _ := json.Marshal(eventEnvelope{T: "multiEv", V: 1, D: v1d})
	next, err := fold([]string(nil), v1env)
	require.NoError(t, err)
	require.Equal(t, []string{"hi"}, next, "a v1 record must run the full v1→v2→v3 chain")
}

// The current version is 1+MAX(registered fromVersion): registering a LOWER
// fromVersion after a higher one must not lower it (registration order is the
// developer's, and gaps are filled, not regressed).
//
//nolint:paralleltest // mutates the process-global event-version registry
func TestCurrentVersionTakesTheMaxFromVersion(t *testing.T) {
	id := func(old json.RawMessage) (json.RawMessage, error) { return old, nil }
	RegisterEvent[maxEvent](3, id) // current → 4
	RegisterEvent[maxEvent](2, id) // out-of-order, lower — must NOT lower current
	require.Equal(t, 4, currentVersionFor[maxEvent](), "current version must be 1+max(fromVersion), order-independent")
}

type maxEvent struct{ N int }

func (maxEvent) Fold(acc int, e maxEvent) int { return acc + e.N }

type gapEvent struct{ N int }

func (gapEvent) Fold(acc int, e gapEvent) int { return acc + e.N }

type failEvent struct{ N int }

func (failEvent) Fold(acc int, e failEvent) int { return acc + e.N }

// An event type with NO registered upcaster has current version 1, so Append
// stamps v1 and foldBytes folds directly — the common, zero-ceremony case must
// be untouched by the registry.
//
//nolint:paralleltest // mutates the process-global event-version registry
func TestUnregisteredTypeStaysAtVersionOne(t *testing.T) {
	require.Equal(t, 1, currentVersionFor[envEv](), "an unregistered event type stays at version 1")
}
