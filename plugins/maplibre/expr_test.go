package maplibre_test

import (
	"testing"

	"github.com/go-via/via/plugins/maplibre"
	"github.com/stretchr/testify/assert"
)

func TestGet_readsAFeatureProperty(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []any{"get", "name"}, []any(maplibre.Get("name")))
}

func TestFeatureState_readsAFeatureStateKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []any{"feature-state", "hover"}, []any(maplibre.FeatureState("hover")))
}

func TestZoom_isTheZoomInputExpression(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []any{"zoom"}, []any(maplibre.Zoom()))
}

func TestBoolean_coercesWithAFallback(t *testing.T) {
	t.Parallel()
	// A feature-state read can be undefined on un-hovered features; boolean()
	// with a fallback is what makes a case test safe.
	assert.Equal(t, []any{"boolean", maplibre.FeatureState("hover"), false},
		[]any(maplibre.Boolean(maplibre.FeatureState("hover"), false)))
}

func TestCase_emitsWhenThenPairsThenFallback(t *testing.T) {
	t.Parallel()
	got := maplibre.Case("base",
		maplibre.Branch{When: "w1", Then: "t1"},
		maplibre.Branch{When: "w2", Then: "t2"},
	)
	assert.Equal(t, []any{"case", "w1", "t1", "w2", "t2", "base"}, []any(got))
}

func TestCase_withNoBranchesIsAValidFallbackExpression(t *testing.T) {
	t.Parallel()
	// MapLibre's case requires at least one when/then pair before the fallback:
	// ["case", fallback] is rejected at style-parse time ("Expected at least 4
	// arguments"). With no branches we must not emit that invalid shape — a
	// scalar fallback becomes ["literal", fallback], a valid constant expression.
	assert.Equal(t, []any{"literal", "base"}, []any(maplibre.Case("base")))
}

func TestCase_withNoBranchesPassesThroughAnExpressionFallback(t *testing.T) {
	t.Parallel()
	// An expression fallback is already valid on its own, so return it as-is
	// rather than wrapping it in a degenerate (and invalid) case.
	assert.Equal(t, []any{"get", "color"},
		[]any(maplibre.Case(maplibre.Get("color"))))
}

func TestInterpolate_linearlyRampsOutputsAcrossInputStops(t *testing.T) {
	t.Parallel()
	// The workhorse for zoom-responsive sizing — line width 2 at z5, 6 at z12.
	got := maplibre.Interpolate(maplibre.Zoom(),
		maplibre.Stop{At: 5, Value: 2},
		maplibre.Stop{At: 12, Value: 6},
	)
	assert.Equal(t, []any{"interpolate", []any{"linear"}, []any{"zoom"}, 5.0, 2, 12.0, 6},
		[]any(got))
}

func TestStep_emitsBaseThenStops(t *testing.T) {
	t.Parallel()
	got := maplibre.Step(maplibre.Zoom(), "small",
		maplibre.Stop{At: 8, Value: "big"},
	)
	assert.Equal(t, []any{"step", []any{"zoom"}, "small", 8.0, "big"}, []any(got))
}

func TestWhenHovered_isDropInForTheHandWrittenHoverExpression(t *testing.T) {
	t.Parallel()
	// This MUST equal the nested expression the example used by hand, so the
	// sugar is a true drop-in and a future refactor can't silently change the
	// shape MapLibre receives.
	handWritten := []any{"case",
		[]any{"boolean", []any{"feature-state", "hover"}, false},
		"#ffcc00", "#5856d6"}
	assert.Equal(t, handWritten, []any(maplibre.WhenHovered("#ffcc00", "#5856d6")))
}

func TestWhenState_generalizesHoverToAnyStateKey(t *testing.T) {
	t.Parallel()
	// Selection highlighting reuses the same shape with a different key.
	assert.Equal(t, []any{"case",
		[]any{"boolean", []any{"feature-state", "selected"}, false},
		"#f00", "#999"},
		[]any(maplibre.WhenState("selected", "#f00", "#999")))
}

func TestWhenHovered_dropsIntoPaintWithIdenticalJSON(t *testing.T) {
	t.Parallel()
	// Backward-compatible: feeding the sugar to Paint must marshal identically
	// to feeding the raw []any, so existing layers keep working.
	sugar := maplibre.FillLayer("zones", "zones",
		maplibre.Paint("fill-color", maplibre.WhenHovered("#ffcc00", "#5856d6")))
	raw := maplibre.FillLayer("zones", "zones",
		maplibre.Paint("fill-color", []any{"case",
			[]any{"boolean", []any{"feature-state", "hover"}, false},
			"#ffcc00", "#5856d6"}))
	assert.JSONEq(t, jsonOf(t, raw), jsonOf(t, sugar))
}
