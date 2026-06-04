package via

import (
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

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

	var server *httptest.Server
	app := New(WithTestServer(&server))
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

	var server *httptest.Server
	app := New(WithTestServer(&server))
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
func TestUnregisteredTypeStaysAtVersionOne(t *testing.T) {
	require.Equal(t, 1, currentVersionFor[envEv](), "an unregistered event type stays at version 1")
}
