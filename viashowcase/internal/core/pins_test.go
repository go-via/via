package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPinFold(t *testing.T) {
	t.Parallel()
	out := Pin{}.Fold(nil, Pin{Room: "r1", Lng: 1, Lat: 2})
	require.Equal(t, []LngLat{{1, 2}}, out.For("r1"))

	out = Pin{}.Fold(out, Pin{Room: "r1", Lng: 3, Lat: 4})
	require.Equal(t, []LngLat{{1, 2}, {3, 4}}, out.For("r1"))

	out = Pin{}.Fold(out, Pin{Room: "r2", Lng: 5, Lat: 6})
	require.Equal(t, []LngLat{{5, 6}}, out.For("r2"))
	require.Len(t, out.For("r1"), 2)
}

func TestPinFoldCopyOnWrite(t *testing.T) {
	t.Parallel()
	acc := PinSets{"r1": {{1, 2}}}
	out := Pin{}.Fold(acc, Pin{Room: "r1", Lng: 3, Lat: 4})
	require.Len(t, acc["r1"], 1, "original acc must be untouched")
	require.Len(t, out["r1"], 2)
}

func TestPinFoldCap(t *testing.T) {
	t.Parallel()
	var acc PinSets
	for i := 0; i < MaxPins+50; i++ {
		acc = Pin{}.Fold(acc, Pin{Room: "r1", Lng: float64(i), Lat: 0})
	}
	got := acc.For("r1")
	require.Len(t, got, MaxPins, "capped at MaxPins")
	// oldest dropped: first retained is the 50th pin (index 50)
	require.Equal(t, float64(50), got[0].Lng)
	require.Equal(t, float64(MaxPins+49), got[MaxPins-1].Lng)
}

func TestPinSetsForNilSafe(t *testing.T) {
	t.Parallel()
	var p PinSets
	require.Nil(t, p.For("missing"))
}
