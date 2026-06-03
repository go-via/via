package maplibre

import (
	"encoding/json"
	"fmt"

	"github.com/go-via/via"
)

// GeoJSON builders. All coordinates are [lng, lat] (longitude first) per
// RFC 7946 — the same order MapLibre uses everywhere.

// Point returns a GeoJSON Point geometry at (lng, lat).
func Point(lng, lat float64) map[string]any {
	return map[string]any{"type": "Point", "coordinates": []float64{lng, lat}}
}

// LineString returns a GeoJSON LineString geometry through coords, each an
// [lng, lat] pair.
func LineString(coords [][]float64) map[string]any {
	return map[string]any{"type": "LineString", "coordinates": coords}
}

// Polygon returns a GeoJSON Polygon geometry from rings — the first ring is
// the outer boundary, any others are holes. Each ring is a closed loop of
// [lng, lat] pairs (first == last).
func Polygon(rings [][][]float64) map[string]any {
	return map[string]any{"type": "Polygon", "coordinates": rings}
}

// Feature wraps a geometry (e.g. from [Point]) with properties, which layer
// paint expressions can read via ["get","key"]. A nil props is fine.
func Feature(geometry, props map[string]any) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	return map[string]any{"type": "Feature", "geometry": geometry, "properties": props}
}

// PointFeature is sugar for a [Feature] wrapping a [Point] — the common case
// of a labelled pin or data point.
func PointFeature(lng, lat float64, props map[string]any) map[string]any {
	return Feature(Point(lng, lat), props)
}

// FeatureCollection bundles features into the top-level object a GeoJSON
// source expects. Call with no args for an empty collection (a source to fill
// later via [Map.SetGeoJSON]).
func FeatureCollection(features ...map[string]any) map[string]any {
	if features == nil {
		features = []map[string]any{}
	}
	return map[string]any{"type": "FeatureCollection", "features": features}
}

// LayerOption configures a layer spec from the typed layer builders.
type LayerOption func(map[string]any)

// Paint sets a paint property (e.g. "circle-color", "line-width"). Paint
// properties control appearance and can be data-driven expressions.
func Paint(key string, value any) LayerOption {
	return func(spec map[string]any) { nestedSet(spec, "paint", key, value) }
}

// Layout sets a layout property (e.g. "line-cap", "visibility", "text-field").
// Layout properties affect how features are placed and laid out.
func Layout(key string, value any) LayerOption {
	return func(spec map[string]any) { nestedSet(spec, "layout", key, value) }
}

// Filter restricts the layer to features matching a MapLibre filter
// expression, e.g. []any{"==", []any{"get", "kind"}, "park"}.
func Filter(expr []any) LayerOption {
	return func(spec map[string]any) { spec["filter"] = expr }
}

func nestedSet(spec map[string]any, group, key string, value any) {
	g, ok := spec[group].(map[string]any)
	if !ok {
		g = map[string]any{}
		spec[group] = g
	}
	g[key] = value
}

func layer(kind, id, source string, opts []LayerOption) map[string]any {
	spec := map[string]any{"id": id, "type": kind, "source": source}
	for _, o := range opts {
		o(spec)
	}
	return spec
}

// CircleLayer renders a source's points as circles. Style with Paint, e.g.
// Paint("circle-radius", 6), Paint("circle-color", "#e55").
func CircleLayer(id, source string, opts ...LayerOption) map[string]any {
	return layer("circle", id, source, opts)
}

// LineLayer renders a source's lines. Style with Paint("line-color", …),
// Paint("line-width", …).
func LineLayer(id, source string, opts ...LayerOption) map[string]any {
	return layer("line", id, source, opts)
}

// FillLayer renders a source's polygons. Style with Paint("fill-color", …),
// Paint("fill-opacity", …).
func FillLayer(id, source string, opts ...LayerOption) map[string]any {
	return layer("fill", id, source, opts)
}

// SymbolLayer renders a source as icons and/or text labels. Configure via
// Layout, e.g. Layout("text-field", []any{"get", "name"}).
func SymbolLayer(id, source string, opts ...LayerOption) map[string]any {
	return layer("symbol", id, source, opts)
}

// SetGeoJSON replaces the data of the GeoJSON source named id — the primary
// way to push live data (markers-as-layer, routes, choropleths) to every
// connected tab. The source must already exist (declared via
// [WithGeoJSONSource] or added with [Map.AddGeoJSONSource]); if it doesn't,
// the call is a safe no-op. Returns an error only if data can't be
// marshalled.
func (m *Map) SetGeoJSON(ctx *via.Ctx, id string, data map[string]any) error {
	jdata, err := marshal(data)
	if err != nil {
		return err
	}
	m.execReady(ctx, fmt.Sprintf("var _s=_m.getSource(%s);if(_s)_s.setData(%s)", mustJSON(id), jdata))
	return nil
}

// AddGeoJSONSource adds a GeoJSON source at runtime (the [WithGeoJSONSource]
// equivalent for sources created after first paint). A no-op if a source with
// that id already exists. Returns an error only on marshal failure.
func (m *Map) AddGeoJSONSource(ctx *via.Ctx, id string, data map[string]any) error {
	jsource, err := marshal(geoJSONSource(data))
	if err != nil {
		return err
	}
	m.execReady(ctx, fmt.Sprintf("if(!_m.getSource(%s))_m.addSource(%s,%s)", mustJSON(id), mustJSON(id), jsource))
	return nil
}

// AddLayer adds a layer at runtime. Build spec with [CircleLayer] etc. A no-op
// if a layer with that id already exists. Returns an error only on marshal
// failure.
func (m *Map) AddLayer(ctx *via.Ctx, spec map[string]any) error {
	jspec, err := marshal(spec)
	if err != nil {
		return err
	}
	id, _ := spec["id"].(string)
	m.execReady(ctx, fmt.Sprintf("if(!_m.getLayer(%s))_m.addLayer(%s)", mustJSON(id), jspec))
	return nil
}

// SetPaintProperty updates one paint property of a layer at runtime (e.g.
// recolor a choropleth, fade a layer). Returns an error only if value can't
// be marshalled.
func (m *Map) SetPaintProperty(ctx *via.Ctx, layerID, property string, value any) error {
	jval, err := marshal(value)
	if err != nil {
		return err
	}
	m.execReady(ctx, fmt.Sprintf("_m.setPaintProperty(%s,%s,%s)", mustJSON(layerID), mustJSON(property), jval))
	return nil
}

// SetLayerVisibility shows or hides a layer without removing it.
func (m *Map) SetLayerVisibility(ctx *via.Ctx, layerID string, visible bool) {
	v := "none"
	if visible {
		v = "visible"
	}
	m.execReady(ctx, fmt.Sprintf("_m.setLayoutProperty(%s,'visibility',%s)", mustJSON(layerID), mustJSON(v)))
}

// RemoveLayer removes a layer, leaving its source in place.
func (m *Map) RemoveLayer(ctx *via.Ctx, layerID string) {
	m.execReady(ctx, fmt.Sprintf("if(_m.getLayer(%s))_m.removeLayer(%s)", mustJSON(layerID), mustJSON(layerID)))
}

// RemoveLayerAndSource removes a layer and then its source, in that order —
// MapLibre throws if a source is removed while a layer still references it.
func (m *Map) RemoveLayerAndSource(ctx *via.Ctx, layerID, sourceID string) {
	m.execReady(ctx, fmt.Sprintf(
		"if(_m.getLayer(%s))_m.removeLayer(%s);if(_m.getSource(%s))_m.removeSource(%s)",
		mustJSON(layerID), mustJSON(layerID), mustJSON(sourceID), mustJSON(sourceID)))
}

// marshal is the runtime counterpart to mustJSON: it returns the error rather
// than panicking, because the value may originate from user data at action
// time.
func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("maplibre: marshal: %v", err)
	}
	return string(b), nil
}
