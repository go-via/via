package via

import (
	"reflect"
	"testing"
)

// A composite signal must adopt the client's value wholesale, just like a
// scalar one does. If the client removed a map key, the server-side signal
// must drop it too — otherwise stale state the client believes is gone keeps
// driving the action, silently diverging from what the user sees.
func TestComposite_mapDecodeReplacesRatherThanMerges(t *testing.T) {
	t.Parallel()
	m := map[string]int{"stale": 1, "kept": 2}
	// Client now only sends "kept" — "stale" was removed in the browser.
	decodeScalarInto(reflect.ValueOf(&m).Elem(), map[string]any{"kept": float64(2)})

	if _, ok := m["stale"]; ok {
		t.Errorf("stale key survived composite decode: %v — decode must replace, not merge", m)
	}
	if m["kept"] != 2 {
		t.Errorf("kept key missing or wrong after decode: %v", m)
	}
}
