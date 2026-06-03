package maplibre_test

import (
	"testing"

	"github.com/go-via/via/plugins/maplibre"
	"github.com/stretchr/testify/assert"
)

func TestAt_ordersLongitudeFirst(t *testing.T) {
	t.Parallel()
	// At's whole job is to pin the [lng, lat] order, so the foot-gun lives in
	// exactly one documented place instead of every call site.
	p := maplibre.At(-0.12, 51.5)
	assert.Equal(t, -0.12, p.Lng, "first arg is longitude")
	assert.Equal(t, 51.5, p.Lat, "second arg is latitude")
}

func TestLngLat_keyedLiteralIsOrderIndependent(t *testing.T) {
	t.Parallel()
	// The safe form: written lat-first by a human who thinks lat/lng, still
	// correct because the fields are named.
	p := maplibre.LngLat{Lat: 51.5, Lng: -0.12}
	assert.Equal(t, maplibre.At(-0.12, 51.5), p)
}
