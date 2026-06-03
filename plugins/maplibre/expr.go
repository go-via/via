package maplibre

// Expr is a MapLibre style expression — the JSON array form MapLibre evaluates
// for data-driven paint and layout values (see the MapLibre "expressions"
// docs). It is a type alias for []any, so the builders below remain fully
// interchangeable with raw []any: you can nest a hand-written []any inside a
// [Case], or pass a builder result straight to [Paint] / [Layout] / [Filter].
type Expr = []any

// Get reads a feature property, e.g. Get("population") -> ["get","population"].
func Get(property string) Expr { return Expr{"get", property} }

// FeatureState reads a feature-state key set at runtime (see [WithFeatureHover]),
// e.g. FeatureState("hover") -> ["feature-state","hover"].
func FeatureState(key string) Expr { return Expr{"feature-state", key} }

// Zoom is the current map zoom as an expression input — pair with [Interpolate]
// or [Step] for zoom-responsive styling.
func Zoom() Expr { return Expr{"zoom"} }

// Boolean coerces value to a boolean, using fallback when it is null/undefined.
// It's what makes a [FeatureState] read safe inside a [Case] test, since the
// state is undefined on features the user hasn't touched.
func Boolean(value any, fallback bool) Expr { return Expr{"boolean", value, fallback} }

// Branch is one when→then arm of a [Case].
type Branch struct {
	When any // a boolean expression
	Then any // the value to use when When is true
}

// Case picks the Then of the first Branch whose When is true, else fallback —
// ["case", w1, t1, …, fallback].
//
// MapLibre requires at least one when→then pair, so a bare ["case", fallback]
// is rejected at style-parse time. With no branches Case therefore yields a
// valid standalone expression instead: an expression fallback is returned
// unchanged, and a scalar fallback is wrapped as ["literal", fallback].
func Case(fallback any, branches ...Branch) Expr {
	if len(branches) == 0 {
		// Expr is an alias for []any, so this also matches a builder result
		// (e.g. Get) passed straight in as the fallback.
		if e, ok := fallback.([]any); ok {
			return e
		}
		return Expr{"literal", fallback}
	}
	out := Expr{"case"}
	for _, b := range branches {
		out = append(out, b.When, b.Then)
	}
	return append(out, fallback)
}

// Stop is one input→output stop of an [Interpolate] or [Step] ramp.
type Stop struct {
	At    float64 // the input value (e.g. a zoom level)
	Value any     // the output at that input
}

// Interpolate linearly ramps output values across input stops — the workhorse
// for zoom-responsive sizing, e.g. line width 2 at z5 growing to 6 at z12:
//
//	maplibre.Interpolate(maplibre.Zoom(),
//	    maplibre.Stop{At: 5, Value: 2}, maplibre.Stop{At: 12, Value: 6})
func Interpolate(input Expr, stops ...Stop) Expr {
	out := Expr{"interpolate", Expr{"linear"}, input}
	for _, s := range stops {
		out = append(out, s.At, s.Value)
	}
	return out
}

// Step produces a stepped (non-interpolated) ramp: base below the first stop,
// then each stop's Value at and above its input — ["step", input, base, …].
func Step(input Expr, base any, stops ...Stop) Expr {
	out := Expr{"step", input, base}
	for _, s := range stops {
		out = append(out, s.At, s.Value)
	}
	return out
}

// WhenState returns on while the feature-state key is truthy, else off — the
// data-driven highlight pattern. Pair the key with the runtime state you set
// (e.g. "hover" from [WithFeatureHover], or your own "selected").
func WhenState(key string, on, off any) Expr {
	return Case(off, Branch{When: Boolean(FeatureState(key), false), Then: on})
}

// WhenHovered returns on while the feature is hovered (via [WithFeatureHover]),
// else off:
//
//	maplibre.Paint("fill-color", maplibre.WhenHovered("#ffcc00", "#5856d6"))
func WhenHovered(on, off any) Expr { return WhenState("hover", on, off) }
