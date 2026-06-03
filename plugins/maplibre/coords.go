package maplibre

// LngLat is a geographic point. Its named fields defuse the classic MapLibre
// foot-gun — coordinates are [lng, lat] (longitude first), the inverse of the
// lat/lng most humans say — so the camera, marker, and center APIs take this
// instead of a bare (lng, lat) pair. Construct it with keyed fields in any
// order for self-documenting safety, or with the [At] shorthand:
//
//	maplibre.LngLat{Lat: 51.5, Lng: -0.12} // order-independent, unswappable
//	maplibre.At(-0.12, 51.5)               // terse: longitude first
type LngLat struct {
	Lng float64
	Lat float64
}

// At builds a [LngLat] from a longitude and latitude (longitude FIRST). Prefer
// the keyed LngLat{Lng:…, Lat:…} literal when you want the order checked at the
// call site; At is the terse form for when you're sure of the order.
func At(lng, lat float64) LngLat { return LngLat{Lng: lng, Lat: lat} }

// pair returns the [lng, lat] slice MapLibre expects.
func (p LngLat) pair() []float64 { return []float64{p.Lng, p.Lat} }

// Bounds is a geographic box. Named edges defuse the west/south/east/north
// ordering foot-gun for [Map.FitBounds] and [WithMaxBounds].
type Bounds struct {
	West  float64
	South float64
	East  float64
	North float64
}

// sw / ne return the corner pairs MapLibre expects ([[sw],[ne]]).
func (b Bounds) sw() []float64 { return []float64{b.West, b.South} }
func (b Bounds) ne() []float64 { return []float64{b.East, b.North} }
